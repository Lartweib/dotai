---
status: accepted
date: 2026-11-21
tags: [engine, install, backup, restore, replace, cli, web-ui]
---

# 0051 · A later replace install only updates the pack, unless it is asked to reset again

## Context

[0038](0038-replace-mode-resets-harness-to-fresh-install.md) decides that every replace install wipes again: each one moves into the backup whatever the user or another tool put in a harness's own configuration since the last one. That is right for the first replace install, which takes the harness back to a fresh install, and wrong for the ones after it.

Observed on the maintainer's machine, 2026-11-21: after `dotai install <pack> --mode replace`, a `dotai pack update` and a second `dotai install ... --mode replace --variant orca` wiped again everything that had appeared since the first one, 14 entries: another tool's OpenCode status plugins, `~/.claude/plugins`, `~/.claude/keybindings.json`, an agent view file, agy's `mcp_config.json` and more. Each pack update would do the same, so a customization such as a keybinding goes to the backup every time. The user wants to refresh the pack, not to start over.

The human output also printed the whole plan twice: before the consent question, and again, in full, after `installed`.

## Decision

**Per harness, first or later.** A replace install is a *later* install of a harness when the install state records the mode `replace`, lists the harness in its `harnesses`, and is settled for it: no wipe and no rewritten split file of its journal, if any, belongs to its reset (found by the adapter's areas, leak entries and split files; one no adapter claims counts against every harness), since an install that stopped midway while it reset did not finish the first one. The journal records every split file the reset rewrites, folded into the pack's write or not. Anything else is a *first* install of that harness: no state, an isolated state, a harness newly added to the selection, or one an earlier install left out. The state needs no new field; its `mode` and `harnesses` give it, and the schema stays `dotai.state/v1`. One install can mix both: the harnesses with a first install are reset as ever, the others are handled as below.

**First install.** Unchanged: the full reset of [0038](0038-replace-mode-resets-harness-to-fresh-install.md).

**Later install, by default: update only.** The harness's own configuration is treated as an isolated-style update: dotai writes its own files and keys, removes the ones the pack dropped, and wipes nothing. Nothing is classified, so no keep-list or credential-name refusal applies. Everything else a replace install has stays: the running-harness check, the refusal for a relocation variable, the consent for overwriting a file dotai does not own, or a key of `~/.claude.json` that it does not own, the backup of each file it overwrites or removes, the journal, the rollback, and the exact restore. The backup of an update is a replace backup in the history the pinned first backup leads, and it never takes the pin.

**Reset again.** `--reset` takes the later harnesses through the reset of [0038](0038-replace-mode-resets-harness-to-fresh-install.md), as a first install does, with the same consent. It does nothing for a harness that has no replace install yet, and in the isolated mode.

**Who chooses.**

- On a terminal, without `--yes`, `--reset` or `--json`, the install asks while it plans, before it writes, when a reset would wipe something: it lists what the reset would wipe and asks `Reset ... again ...? [y/N]`. The default, no, updates only. A yes goes on to the usual consent question for what a reset wipes.
- `--yes`, `--json`, no terminal, and `--dry-run` never ask and update only. `--yes` consents, it does not choose a reset; `--reset` chooses, it does not consent.
- `--dry-run` shows the plan the flags imply: update only without `--reset`, with a line per harness, `wipes nothing; --reset would wipe N entries and M keys`, and the reset with `--reset`.
- The web UI sends `reset` with the install and plan requests. Its install step shows the choice for a replace plan with later harnesses, updating only by default; a change plans again, so the digest the user confirms is that of the choice.

**JSON.** The install plan gains `reinstall`, the managed harnesses that are later installs; `reset`, whether the plan resets them; and `wouldReset`, when `reinstall` is not empty and `reset` is false, what a reset would do: `wipes`, `wipeKeys` and `refusals`, as the plan has them, `null` otherwise. `wipes`, `keeps` and `wipeKeys` hold what the plan does, so an update has none.

**Plan printed once.** In text, an install that asked a question printed its plan before it, and after `installed` prints only the summary line, the backup path, the state path and the warnings. An install that asked nothing prints the plan once, after. `--json` is unchanged. `apply` had the same duplication and gets the same fix. `restore` prints its conflicts before the question and its result after, which are not the same text, and stays.

## Considered options

- **A (chosen):** update by default, `--reset` and a question on a terminal to reset again.
- **B:** keep every later replace install a reset. Rejected: it is what was observed, a loss to the backup with every pack update.
- **C:** a flag `--update` and reset by default. Rejected: the safe path should not need a flag, and scripts and the web UI would reset silently.
- **D:** always ask, also without a terminal. Rejected: a script cannot answer, and an unattended install must not wipe.
- **E:** a mode per harness in a new state schema (`dotai.state/v2`). Rejected: what is needed, whether the harness has a settled replace install, follows from `mode` and `harnesses`.
- **F:** keep a list of the user's own additions and wipe only what is not on it. Rejected: it moves the problem to what counts as the user's.
- **G:** print the whole plan after `installed` as before. Rejected: the same text twice buries the result.

## Consequences

- Partially supersedes [0038](0038-replace-mode-resets-harness-to-fresh-install.md) ("Every replace wipes"): only a first install of a harness, or `--reset`, wipes. Amends [0018](0018-install-modes-and-mandatory-backup.md) and [0043](0043-local-web-ui.md) (the request field and the install step).
- Good: a pack update no longer sends the user's later additions to the backup, and the reset stays one flag away, informed by the list of what it wipes.
- Good: an update keeps all the safeties, so the restore of it is exact and goes back one install at a time.
- Accepted cost: something a harness or another tool added that dotai would have wiped now stays, until the user resets. The dry run says how much.
- Accepted cost: a user who answers yes gets two questions on a terminal, the choice and the consent for what the reset wipes.
- Accepted cost: the harness whose reset was interrupted is settled by a reset again, since its state cannot tell that it never finished the first one; the others in the same state stay later installs. One that stopped while it only updated is not.
- Accepted cost: a harness dropped from the selection and added again later is a first install for it.

## Evidence

- Observation on the maintainer's real install, 2026-11-21: the second replace install wiped the 14 entries added since the first, and the plan printed twice.
- Tests: `TestALaterReplaceInstallWipesNothingByDefault`, `TestResetWipesAsTodayOnALaterInstall`, `TestTheFirstReplaceInstallStillResets`, `TestALaterReplaceInstallAsksWhichOneOnATerminal`, `TestADryRunShowsThePlanOfWhatTheFlagsImply`, `TestThePlanIsPrintedOnce` and `TestRunApplyPrintsThePlanOnce`.

Update 2026-11-22: the running-harness check this decision keeps for a later install is decided file by file in [0053](0053-running-check-by-live-written-file.md): an update goes on while a harness runs, with the warning `harness-running-update`, unless it writes one of that harness's live-written files; `--reset` is refused as a first install is.

Update 2026-11-22: the question reads `Only update the pack in <harnesses>, wiping nothing? [Y/n]`, with a line before it saying that `n` resets instead. Observed on the maintainer's machine: `Reset ... again, wiping what is listed above, instead of only updating the pack? [y/N]` was answered `y` as if it meant "go on", and a reset attempt followed. The safe choice is now the obvious yes and the default: Enter, `y` and any answer but `n` or `no` update only, and only an explicit `n` or `no` resets, which still goes on to the usual consent question for what it wipes. Nothing else changes: what is listed, `--reset`, `--yes`, `--json`, no terminal and `--dry-run`.
