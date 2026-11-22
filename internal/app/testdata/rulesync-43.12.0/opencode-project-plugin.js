export default {
  id: "rulesync-hooks:" + import.meta.url,
  setup: async (ctx) => {
    const directory = ctx.location?.directory ?? process.cwd();

    const __rulesyncRead = (stream, limit = 1048576) => {
      const chunks = [];
      let size = 0;
      const reader = stream.getReader();
      let reading = true;
      const done = (async () => {
        for (let read; reading && !(read = await reader.read()).done; ) {
          if (reading && (size += read.value.length) <= limit) chunks.push(read.value);
        }
      })().catch(() => {});
      const stop = () => {
        if (reading) (reading = false), reader.cancel().catch(() => {});
      };
      return { done, stop, text: () => Buffer.concat(chunks).toString(), overflow: () => size > limit };
    };
    let __rulesyncCanExec;
    let __rulesyncRetryAt = 0;
    // pid -> [ppid, start time], from procfs or else ps; a reused PID gets a new start time.
    const __rulesyncProcesses = () => {
      const processes = new Map();
      try {
        const fs = require("node:fs");
        for (const pid of fs.readdirSync("/proc").filter((name) => /^\d+$/.test(name))) {
          try {
            const stat = fs.readFileSync("/proc/" + pid + "/stat", "utf8");
            const fields = stat.slice(stat.lastIndexOf(")") + 2).split(" ");
            processes.set(pid, [fields[1], fields[19]]);
          } catch {}
        }
        if (processes.size > 0) return processes;
      } catch {}
      try {
        // The C locale keeps `lstart` in one format; it is compared as text, never parsed.
        const env = { ...process.env, LC_ALL: "C", LANG: "C" };
        const ps = Bun.spawnSync(["ps", "-A", "-o", "pid=,ppid=,lstart="], { env, timeout: 5000 });
        if (ps.exitCode === 0) {
          for (const line of ps.stdout.toString().trim().split("\n")) {
            const [pid, ppid, ...started] = line.trim().split(/\s+/);
            processes.set(pid, [ppid, started.join(" ")]);
          }
          return processes;
        }
      } catch {}
      console.error("[rulesync-hooks] neither /proc nor ps is available: only the hook process is killed");
    };
    const __rulesyncTree = (roots, processes) => {
      const tree = [...roots];
      for (let size = -1; size !== tree.length; ) {
        size = tree.length;
        for (const [pid, [ppid]] of processes ?? []) if (tree.includes(ppid) && !tree.includes(pid)) tree.push(pid);
      }
      return tree;
    };
    // The child itself is signalled only through its handle, a no-op once Bun has reaped it, so a
    // reused PID is never signalled; its descendants only while it is running (not reaped, its PID
    // cannot be reused), and in the SIGKILL round only with both snapshots: the first snapshot's
    // descendants that are the same processes (same start time), and what they and the child,
    // if it ran across the second snapshot, have started since.
    const __rulesyncKillTree = (proc) => {
      const kill = (pids, signal) => pids.forEach((pid) => { try { process.kill(Number(pid), signal); } catch {} });
      const running = () => proc.exitCode === null && proc.signalCode === null;
      const root = String(proc.pid);
      const before = running() ? __rulesyncProcesses() : undefined;
      const first = before && running() ? __rulesyncTree([root], before).slice(1) : [];
      proc.kill("SIGTERM");
      kill(first, "SIGTERM");
      setTimeout(() => {
        const alive = running();
        const now = before && __rulesyncProcesses();
        if (!now) return proc.kill("SIGKILL");
        // Every first-snapshot descendant that is still the same process, wherever it was
        // reparented (to PID 1 or a subreaper), and the child if it survived SIGTERM, with what
        // they have started since. The child is killed first, so it starts nothing more.
        const same = first.filter((pid) => now.get(pid)?.[1] === before.get(pid)?.[1]);
        const own = alive && running() && now.get(root)?.[1] === before.get(root)?.[1];
        proc.kill("SIGKILL");
        kill(__rulesyncTree(own ? [root, ...same] : same, now).filter((pid) => pid !== root), "SIGKILL");
      }, 2000);
    };
    // `proc.kill()` is TerminateProcess on the child alone. A libuv program (node) that dies takes
    // the children it spawned along through its job object, but not ones started through another
    // program (cmd.exe) or detached. taskkill /T ends the tree as linked by parent PID at that
    // moment; System32's copy, so a taskkill earlier on PATH is never run.
    const __rulesyncKillWindowsTree = (proc) => {
      if (proc.exitCode === null && proc.signalCode === null) {
        const taskkill = (process.env.SystemRoot ?? "C:\\Windows") + "\\System32\\taskkill.exe";
        try {
          Bun.spawnSync([taskkill, "/T", "/F", "/PID", String(proc.pid)], { stdout: "ignore", stderr: "ignore", windowsHide: true, timeout: 5000 });
        } catch {}
      }
      proc.kill();
    };
    const __rulesyncRunCommand = async ({ command, input, seconds, inProcess = false, echo = false }) => {
      // A probe that printed `rulesync` is kept; one past its deadline, failing or printing
      // anything else means no Bun CLI for 5 minutes; one that threw is dropped, so the next
      // command probes again.
      if (!inProcess && (__rulesyncCanExec === undefined || (__rulesyncRetryAt && Date.now() >= __rulesyncRetryAt))) {
        __rulesyncRetryAt = 0;
        const probing = (__rulesyncCanExec = (async () => {
          const probe = Bun.spawn([process.execPath, "--no-env-file", "exec", "echo rulesync"], { cwd: directory, env: { ...process.env, BUN_BE_BUN: "1" }, stdin: "ignore", stdout: "pipe", stderr: "ignore", windowsHide: true });
          const output = __rulesyncRead(probe.stdout, 1024);
          let timer;
          // Settles on its own, even if the probe ignores the kill or a process holds its stdout.
          const deadline = new Promise((resolve) => (timer = setTimeout(resolve, 10000, null)));
          const exited = Promise.all([probe.exited, output.done]).then(([code]) => code);
          const exitCode = await Promise.race([exited, deadline]).finally(() => clearTimeout(timer));
          if (exitCode === null) probe.kill("SIGKILL"), output.stop();
          const found = exitCode === 0 && output.text().trim() === "rulesync";
          if (!found) __rulesyncRetryAt = Date.now() + 300000;
          return found;
        })().catch(() => {
          if (__rulesyncCanExec === probing) __rulesyncCanExec = undefined;
          return false;
        }));
      }
      let timer;
      const timeout = () => new Promise((resolve) => (timer = setTimeout(resolve, seconds * 1000, null)));
      if (inProcess || !(await __rulesyncCanExec)) {
        const run = Bun.$`echo ${input} | ${{ raw: command }}`.cwd(directory).nothrow();
        // A sessionStart command's stderr reaches the host when it ends, even after its timeout.
        const finished = echo === true ? run : run.quiet().then((done) => (echo && process.stderr.write(done.stderr), done));
        const result = await Promise.race([finished, timeout()]).finally(() => clearTimeout(timer));
        if (!result || echo === true) return result && { exitCode: result.exitCode };
        const { exitCode, stdout, stderr } = result;
        return { exitCode, stdout: stdout.toString(), stderr: stderr.toString(), overflow: stdout.length > 1048576 };
      }
      // An ordinary command writes to the host's stdout and stderr; a preToolUse one is read, and
      // a sessionStart one (`echo: "stderr"`) has its stdout read.
      const proc = Bun.spawn([process.execPath, "--no-env-file", "exec", "export BUN_BE_BUN=\n" + command], { cwd: directory, env: { ...process.env, BUN_BE_BUN: "1" }, stdin: new TextEncoder().encode(input + "\n"), stdout: echo === true ? "inherit" : "pipe", stderr: echo ? "inherit" : "pipe", windowsHide: true });
      const stdout = echo === true ? undefined : __rulesyncRead(proc.stdout);
      const stderr = echo ? undefined : __rulesyncRead(proc.stderr);
      const exitCode = await Promise.race([proc.exited, timeout()]).finally(() => clearTimeout(timer));
      if (exitCode === null) {
        stdout?.stop(), stderr?.stop();
        if (process.platform === "win32") __rulesyncKillWindowsTree(proc);
        else __rulesyncKillTree(proc);
        return null;
      }
      if (echo === true) return { exitCode };
      await Promise.race([Promise.all([stdout.done, stderr?.done]), Bun.sleep(1000)]);
      stdout.stop(), stderr?.stop();
      return { exitCode, stdout: stdout.text(), stderr: stderr?.text() ?? "", overflow: stdout.overflow() };
    };
    const __rulesyncRunHook = async ({ label, command, input, blocking, seconds, inProcess = false, context = false }) => {
      let result;
      try {
        result = await __rulesyncRunCommand({ command, input, seconds, inProcess, echo: context ? "stderr" : true });
      } catch (error) {
        if (blocking) throw error;
        console.error("[rulesync-hooks] " + label + " hook failed:", error);
        return;
      }
      if (result === null) {
        const message = "[rulesync-hooks] " + label + " hook timed out after " + seconds + "s";
        if (blocking) throw new Error(message);
        return console.error(message);
      }
      if (result.exitCode === 0) return result;
      const message = "[rulesync-hooks] " + label + " hook exited with code " + result.exitCode;
      if (blocking) throw new Error(message);
      console.error(message);
    };

    const __rulesyncSessionContexts = new Map();
    const __rulesyncKept = new WeakSet();
    const __rulesyncContextOf = (result) => {
      if (typeof result?.stdout !== "string") return "";
      if (result.overflow) {
        console.error("[rulesync-hooks] sessionStart hook wrote more than 1 MiB to stdout; it is ignored");
        return "";
      }
      let text = result.stdout;
      try {
        const parsed = JSON.parse(text);
        // Like Claude Code, only a JSON object counts as JSON output; anything else is plain text.
        if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) text = parsed.hookSpecificOutput?.additionalContext;
      } catch {}
      return typeof text === "string" ? text.trim().slice(0, 10000) : "";
    };
    const __rulesyncSessionStart = (sessionID, run) => {
      const results = [];
      const pending = run(results)
        .catch(() => {})
        .then(() => results.map(__rulesyncContextOf).filter(Boolean).join("\n\n"));
      if (typeof sessionID === "string") {
        __rulesyncSessionContexts.delete(sessionID);
        __rulesyncSessionContexts.set(sessionID, pending);
        // Only a non-empty context is kept, and only it counts towards the 100: a running
        // or empty one never evicts another session's context.
        pending.then((context) => {
          if (__rulesyncSessionContexts.get(sessionID) !== pending) return;
          if (!context) return void __rulesyncSessionContexts.delete(sessionID);
          __rulesyncKept.add(pending);
          // Evict in completion order: a context counts as used once it is ready.
          __rulesyncSessionContexts.delete(sessionID);
          __rulesyncSessionContexts.set(sessionID, pending);
          const kept = [...__rulesyncSessionContexts].filter(([, entry]) => __rulesyncKept.has(entry));
          if (kept.length > 100) __rulesyncSessionContexts.delete(kept[0][0]);
        });
      }
      return pending;
    };
    const __rulesyncSessionContext = async (sessionID) => {
      const pending = __rulesyncSessionContexts.get(sessionID);
      if (!pending) return "";
      __rulesyncSessionContexts.delete(sessionID);
      __rulesyncSessionContexts.set(sessionID, pending);
      return pending;
    };

    const __rulesyncRegistrations = [];
    try {
      __rulesyncRegistrations.push(
        await ctx.session.hook("context", async (event) => {
          const context = await __rulesyncSessionContext(event?.sessionID);
          if (context && Array.isArray(event?.system)) event.system.push({ type: "text", text: context });
        }),
      );
    } catch (error) {
      console.error("[rulesync-hooks] could not register the session context hook; sessionStart output does not reach the model:", error);
    }

    const __rulesyncEvents = new AbortController();
    let __rulesyncEventQueue = Promise.resolve();
    void (async () => {
      for await (const event of ctx.event.subscribe({
        types: ["session.created", "session.execution.succeeded", "session.deleted"],
        signal: __rulesyncEvents.signal,
      })) {
        if (event.type === "session.deleted") __rulesyncSessionContexts.delete(event.data?.sessionID);
        if (event.type === "session.created") {
          void __rulesyncSessionStart(event.data?.sessionID, async (__rulesyncResults) => {
            try {
              __rulesyncResults.push(await __rulesyncRunHook({ label: "sessionStart", command: "echo hi", input: "", blocking: false, seconds: 600, context: true }));
            } catch (error) {
              console.error("[rulesync-hooks] sessionStart hook failed:", error);
            }
          });
        } else if (event.type === "session.execution.succeeded") {
          __rulesyncEventQueue = __rulesyncEventQueue
            .then(async () => {
              if (__rulesyncEvents.signal.aborted) return;
              let __rulesyncStopInputCache;
              const __rulesyncStopInput = () =>
                (__rulesyncStopInputCache ??= JSON.stringify({
                  hook_event_name: "Stop",
                  session_id: event.data?.sessionID,
                  cwd: directory,
                }));
              try {
                await __rulesyncRunHook({ label: "stop", command: "echo bye", input: __rulesyncStopInput(), blocking: false, seconds: 600 });
              } catch (error) {
                console.error("[rulesync-hooks] stop hook failed:", error);
              }
            })
            .catch((error) => console.error("[rulesync-hooks] session.execution.succeeded event hook failed:", error));
        }
      }
    })().catch(() => {});

    return async () => {
      __rulesyncEvents.abort();
      await Promise.all(__rulesyncRegistrations.map((registration) => registration.dispose()));
    };
  },
};
