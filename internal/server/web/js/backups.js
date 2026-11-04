// The Backups board: every backup, newest first, the home ones and those
// of each project. A home backup restores through a preview first: the
// restore carries out only the preview it shows, by its digest. A project
// is restored as a whole, through the preview of restore --project.

import { busy, h, kv, mount, notice, op, plural, sec, warning, when } from "./dom.js";
import { api, post, query, tilde } from "./store.js";

const titles = { install: "Install", restore: "Restore", apply: "Apply", fix: "Fix exclude entries" };

export async function render(root, ctx) {
  const list = h("section", { class: "listpane", "aria-labelledby": "bk-h" });
  const detail = h("section", { class: "detail", "aria-label": "Selected backup" });
  mount(root, list, detail);
  mount(list, busy("Reading the backups..."));
  const res = await api("/api/backups");
  if (ctx.stale()) return;
  if (!res.ok) {
    mount(list, notice("bad", res.error.code, res.error.message));
    return;
  }
  const groups = [{ label: "Home", dir: "~/.dotai/backups", project: "", items: res.data.home },
    ...res.data.projects.map((p) => ({ label: p.project ? tilde(p.project) : "unknown project", dir: tilde(p.path), project: p.project, error: p.error, items: p.backups }))];
  const all = groups.flatMap((g) => g.items.map((b) => ({ b, g })));
  ctx.setCount("backups", all.length);
  let cur = all.find((x) => x.b.id === ctx.arg) || all[0];

  const draw = () => {
    mount(list,
      h("div", { class: "listhead" }, h("h1", { class: "h-list", id: "bk-h", text: "Backups" }), h("span", { class: "t-cap", text: "newest first" })),
      groups.map((g) => h("div", {},
        h("div", { class: "gh" }, h("h2", { text: g.label }), h("span", { class: "t-cap", text: String(g.items.length) }), h("span", { class: "t-cap dir", text: g.dir })),
        g.error ? notice("bad", "unreadable", g.error) : null,
        h("ul", { class: "rows" }, g.items.length ? g.items.map((b) => h("li", {},
          h("button", { class: "rowbtn", type: "button", "aria-pressed": String(cur && cur.b === b), onclick: () => { cur = all.find((x) => x.b === b); draw(); } },
            h("span", { class: "main" },
              h("span", { class: "title" }, h("b", { text: title(b) }), badge(b) ? h("span", { class: "pill sm state", text: badge(b) }) : null),
              h("span", { class: "t-path", text: b.id })),
            h("span", { class: "when", text: when(b.createdAt) }))))
          : h("li", { class: "t-cap empty", text: "No backup yet." })))));
    show(detail, cur, ctx);
  };
  draw();
}

function title(b) {
  const what = titles[b.operation] || b.operation;
  return b.pack && b.pack.name ? what + " " + b.pack.name : what;
}

function badge(b) {
  if (!b.valid) return "invalid";
  if (b.head) return "current";
  if (b.restored) return "restored";
  return "";
}

function show(detail, cur, ctx) {
  if (!cur) {
    mount(detail, h("div", { class: "col intro" }, h("h2", { class: "h-detail", text: "No backups" }),
      h("p", { class: "lead", text: "An install, an apply, a restore and doctor --fix each take a backup before they write. None has run here yet." })));
    return;
  }
  const { b, g } = cur;
  const home = !g.project && g.label === "Home";
  const action = h("div", { class: "sc stack" });
  mount(detail,
    h("div", { class: "col intro tight" },
      h("div", { class: "row" }, h("span", { class: "t-path", text: b.id }), h("span", { class: "pill state", text: b.operation }), badge(b) ? h("span", { class: "pill state", text: badge(b) }) : null),
      h("h2", { class: "h-detail", text: headline(b, g) }),
      b.error ? notice("bad", "invalid", b.error) : null),
    h("div", { class: "col" },
      sec("Covers", kv([
        ["created", new Date(b.createdAt).toUTCString()],
        ["pack", b.pack && b.pack.name ? b.pack.name + " " + b.pack.version + ", " + tilde(b.pack.path) : ""],
        ["variant", b.variant],
        ["mode", b.mode],
        ["harnesses", (b.harnesses || []).join(", ")],
        ["project", g.project ? tilde(g.project) : ""],
        ["directory", tilde(b.path)],
      ])),
      sec("Changes", b.targets.length ? h("ul", {}, b.targets.map((t) => op("covers", tilde(t)))) : h("p", { class: "quietp", text: "The manifest lists no target." })),
      h("div", { class: "sec" }, h("h2", { text: "Restore" }), action)));
  if (home) restoreHome(action, b, ctx);
  else if (g.project) restoreProject(action, g.project, ctx);
  else mount(action, h("p", { class: "body", text: "The project of this backup is unknown, so it cannot be restored from here." }));
}

function headline(b, g) {
  const what = titles[b.operation] || b.operation;
  if (b.operation === "install" && b.pack && b.pack.name) return (b.mode === "replace" ? "Replace install" : "Install") + " of " + b.pack.name + " " + b.pack.version + (b.mode ? ", " + b.mode : "");
  if (g.project) return what + " in " + tilde(g.project);
  return what;
}

function restoreHome(action, b, ctx) {
  if (b.restored) {
    mount(action, h("p", { class: "body", text: "Already restored: a restored install cannot be restored twice." }),
      h("div", { class: "row" }, h("button", { class: "btn primary", type: "button", disabled: true, text: "Restored" })));
    return;
  }
  const preview = () => confirmFlow(action, {
    load: () => api(query("/api/restore/preview", { id: b.id })),
    lead: (r) => "Restore undoes " + plural(r.undone.length, "install") + (r.uninstalled ? ", and no install is left after it." : ", and puts back the install before them."),
    rows: (r) => [
      ...r.ops.map((o) => op(o.action === "replace" ? "overwrite" : o.action, tilde(o.target))),
      ...r.wipes.map((w) => op("create", tilde(w.path), "moved back from the backup")),
      ...r.wipeKeys.map((k) => op("replace", tilde(k.file) + " · " + k.key.join("."), "key put back")),
    ],
    label: "Restore",
    says: "Restore first takes a backup of its own, <time>_restore, of every file it removes or overwrites.",
    execute: (r, yes) => post("/api/restore", { id: b.id, digest: r.digest, yes }),
    ctx,
  });
  mount(action, h("p", { class: "body", text: "A restore puts the files of this install back as they were before it. It shows what it writes first." }),
    h("div", { class: "row" }, h("button", { class: "btn primary", type: "button", onclick: preview, text: "Preview restore" })));
}

function restoreProject(action, project, ctx) {
  const preview = () => confirmFlow(action, {
    load: () => api(query("/api/restore/project/preview", { project })),
    lead: () => "A project is restored as a whole: every apply recorded there is undone, so the project, its exclude file and the keys dotai owns are as before the first apply.",
    rows: (r) => [
      ...r.ops.map((o) => op(o.conflict ? "conflict" : o.action === "replace" ? "overwrite" : o.action, tilde(o.target))),
      r.exclude.file ? op("replace", tilde(r.exclude.file), "the project's block leaves it") : null,
    ],
    label: "Restore project",
    says: "Runs dotai restore --project for " + tilde(project) + ", after a backup of what it writes.",
    execute: (r, yes) => post("/api/restore/project", { project, digest: r.digest, yes }),
    ctx,
  });
  mount(action, h("p", { class: "body", text: "A project is restored as a whole: dotai restore --project undoes every apply recorded there." }),
    h("div", { class: "row" }, h("button", { class: "btn primary", type: "button", onclick: preview, text: "Preview restore of the project" })));
}

// confirmFlow shows a preview, with its conflicts and the consent they
// need, and carries out exactly that preview. A preview that changed
// before the restore wrote anything is shown again, new.
export async function confirmFlow(el, f, msg) {
  mount(el, busy("Previewing, nothing is written..."));
  const res = await f.load();
  if (!res.ok) {
    mount(el, notice("bad", res.error.code, res.error.message), conflictList(res.data && res.data.conflicts));
    return;
  }
  showPreview(el, f, res.data, msg);
}

function showPreview(el, f, r, msg) {
  const conflicts = r.conflicts || [];
  const yes = h("input", { type: "checkbox" });
  const out = h("div");
  const go = h("button", { class: "btn primary", type: "button", text: f.label });
  const sync = () => { go.disabled = conflicts.length > 0 && !yes.checked; };
  yes.addEventListener("change", sync);
  sync();
  go.addEventListener("click", async () => {
    go.disabled = true;
    mount(out, busy("Restoring..."));
    const res = await f.execute(r, yes.checked);
    if (res.ok) {
      await f.ctx.refresh();
      mount(el, notice("good", "restored", res.data.backup ? "Done. The backup " + res.data.backup.id + " holds what the restore overwrote or removed." : "Done. The restore wrote no file."),
        (res.data.warnings || []).length ? h("ul", {}, res.data.warnings.map((w) => warning("warning", w))) : null,
        h("div", { class: "row" }, h("button", { class: "btn", type: "button", onclick: () => f.ctx.again(), text: "Show the backups again" })));
      return;
    }
    if (res.error.code === "plan-changed") {
      showPreview(el, f, res.data, notice("bad", res.error.code, res.error.message));
      return;
    }
    mount(out, notice("bad", res.error.code, res.error.message), conflictList(res.data && res.data.conflicts));
    sync();
  });
  const rows = f.rows(r).filter(Boolean);
  mount(el, msg || null,
    h("p", { class: "body", text: f.lead(r) }),
    h("ul", {}, rows.length ? rows : h("li", { class: "quietp", text: "Nothing to write." })),
    (r.warnings || []).length ? h("ul", {}, r.warnings.map((w) => warning("warning", w))) : null,
    conflicts.length ? h("div", { class: "well" }, h("p", { class: "t-label", text: plural(conflicts.length, "conflict") }), conflictList(conflicts)) : null,
    conflicts.length ? h("label", { class: "check" }, yes, h("span", { text: "Overwrite the conflicts. The restore copies each one into its own backup first." })) : null,
    h("div", { class: "row" }, go), h("p", { class: "t-cap", text: f.says }), out);
}

function conflictList(conflicts) {
  if (!conflicts || conflicts.length === 0) return null;
  return h("ul", {}, conflicts.map((c) => warning(c.key ? "key-changed" : "file-changed",
    tilde(c.target) + (c.key ? " · " + c.key : "") + ": changed since dotai wrote it.")));
}
