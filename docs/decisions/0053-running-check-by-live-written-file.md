---
status: accepted
date: 2026-11-22
tags: [engine, install, restore, replace, harnesses, web-ui]
---

# 0053 · A running harness stops a replace install only for the files it rewrites itself

## Context

[0038](0038-replace-mode-resets-harness-to-fresh-install.md) refuses a replace install, and [0018](0018-install-modes-and-mandatory-backup.md) rule 5 its restore, while a harness it writes into is running, "because some harnesses rewrite their files while running". That is right for a reset, which moves whole trees out from under the harness. It is wrong for a later install that only updates ([0051](0051-later-replace-install-updates-unless-reset.md)).

Observed on the maintainer's machine, 2026-11-22: the maintainer runs many agent sessions, and a real pack update replaced `agents/worker.md`, `skills/delegate/SKILL.md` and `skills/dotai-analyze/references/detection.md` of each harness, and `~/.dotai/tools/statusline.mjs` and `delegate-backend.mjs`. No running harness writes any of them: it reads them when a session starts. Yet every session had to be closed for each pack update.

The risk the check guards against is narrower: a file the harness itself rewrites while it runs, from its own copy in memory, so that dotai's change is lost or the harness's change is.

## Decision

**Live-written files, as data.** Each harness adapter lists in `Real.LiveWritten` the files, relative to the home directory, that the harness or a program beside it rewrites while it runs. They are the files already owned key by key ([0035](0035-settings-files-owned-by-key.md), [0049](0049-claude-user-mcp-servers-in-claude-json.md), [0050](0050-hook-entries-owned-one-by-one.md)), since a harness writes into them; a file dotai never writes is not listed:

| Harness | Live-written files | Evidence |
|---|---|---|
| Claude Code | `~/.claude/settings.json`, `~/.claude.json` | 0035: Claude Code added `theme` to `settings.json` on its first launch, and `/model` writes `model`; HARNESS-NOTES: a running session rewrites `.claude.json`, and an external write that lands between its read and its write is lost |
| agy | `~/.gemini/antigravity-cli/settings.json`, `~/.gemini/config/hooks.json` | settings: 0035 (not seen written by agy at runtime: inferred); hooks: HARNESS-NOTES, a program that registers hooks adds its own top-level key at each of its starts. agy's `config.json` (plugin enablement and "always allow" rules, documented in its changelog) is left out: dotai never writes it |
| OpenCode | `~/.config/opencode/opencode.jsonc` | 0035: its settings file, written by OpenCode's own commands (inferred); plugins, agents, skills and rules are read when it starts |

Everything else, rules, agents, skills, commands, plugins and the tools in `~/.dotai/tools`, is read at the start of a session.

**The rule.** For each harness an install or restore writes into:

- A harness it **wipes** (a first replace install, `--reset`, or a restore that moves wiped entries or keys back) is refused while it runs, whole, as before.
- A harness it does **not** wipe is refused while it runs only if the operation **writes** (create, replace or remove, a file or a key) one of its live-written files. The refusal, `harness-running`, names the harness and the files in `data.files`.
- Otherwise it goes on, and the plan carries the warning `harness-running-update`, per running harness: the running session keeps what it loaded and new sessions pick the changes up.
- `--ignore-running` overrides all of it, as before. When the processes cannot be listed, the operation is refused if it wipes or writes a live-written file, as a running harness would, and goes on otherwise. A write that names no harness is attributed from its target, and one whose target cannot be related to the home directory counts as a live-written file, so the check fails closed. Names compare without case on Windows and macOS.

The running-harness reminder (a harness may run unseen, since it is found by process name) is now given for the harnesses the operation is refused for when they run, no longer for every harness.

**Same rule for restore**, which already checked: it puts back files and keys of an update, which a running harness does not rewrite, and moves entries back after a reset, which it does. The generic `close <harnesses> before restoring` warning stays.

**Isolated mode** has no running check ([0018](0018-install-modes-and-mandatory-backup.md) rule 5 decided it: a harness running outside the isolation does not read the isolated directories), and the same files in `~/.dotai/isolated/` are not live-written by a harness that runs outside it. It stays unchanged.

**Unchanged:** consent, the backup, the journal, rollback and the exact restore. Files are written atomically (a temporary file and a rename). On Windows a file a program holds open still fails the write with the in-use hint and rolls the install back, so a harness that holds a file the update writes does not corrupt it.

**Web UI.** The plan shows `harness-running-update` as any plan warning, in both dictionaries. It is decided while planning, from the processes at that moment, so a plan made while a harness runs has another digest than one made after it stops: an action that finds the harness closed in between stops with `plan-changed` and shows the new plan, as for any change between the plan and the action ([0043](0043-local-web-ui.md)).

## Considered options

- **A (chosen):** a list of live-written files per adapter, refused only when the plan writes one.
- **B:** keep refusing for any running harness. Rejected: it is what was observed, every session closed for an update of read-only content.
- **C:** drop the check for updates. Rejected: a running harness rewrites `settings.json` and `.claude.json`, and dotai's keys there can be lost.
- **D:** refuse for any file in a harness's directory that has a key-by-key owner. Rejected: the same as A without naming the evidence, and it would include files no harness writes.

## Consequences

- Good: a pack update that touches skills, agents, rules and tools no longer needs every session closed, and a refusal says which file and why.
- Accepted cost: a running session keeps the old skills and agents it loaded until it starts again, which the warning says.
- Accepted cost: the list is evidence-based and incomplete: a file missing from it is updated under a running harness; a file in it that the harness does not in fact rewrite refuses without need (agy's and OpenCode's settings files are inferred). `--ignore-running` gets past a refusal.
- Accepted cost: a settings key change in a later update, such as a new `statusLine`, still needs the harness closed.
- Partially supersedes [0038](0038-replace-mode-resets-harness-to-fresh-install.md) ("Refusals") and [0018](0018-install-modes-and-mandatory-backup.md) rule 5, and amends [0051](0051-later-replace-install-updates-unless-reset.md) (the running-harness check it keeps).

## Evidence

- Observation on the maintainer's real update, 2026-11-22.
- Tests: `TestALaterUpdateRefusesOnlyForTheFilesARunningHarnessRewrites`, `TestAResetStillRefusesWhileTheHarnessRuns`, `TestAnUpdateThatCannotListProcessesGoesOnUnlessItWritesALiveFile`, `TestARestoreRefusesOnlyForTheFilesARunningHarnessRewrites` and `TestAnUpdateThatWritesNoLiveFileGoesOnWhileAHarnessRuns`.
