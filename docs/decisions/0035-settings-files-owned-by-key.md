---
status: accepted
date: 2026-10-30
tags: [engine, install, restore, settings, state]
---

# 0035 · dotai owns only its keys in a harness's settings file

## Context

[0032](0032-install-state-backup-and-isolated-launch.md) records each installed file with the SHA-256 of the whole file and refuses to overwrite or remove an owned file that changed; [0033](0033-restore-from-the-install-history.md) checks restores the same way. Harnesses write into their own settings file while they run: in a manual test, Claude Code added `"theme": "dark"` to the isolated `settings.json` on its first launch, and the next `dotai install --variant tmux` was refused. Any install after using a harness failed. [0013](0013-renderer-merges-by-key.md) already asks for merge by key with a record of owned keys, which 0032 left to the project scope.

## Decision

- **Scope.** The settings files of the isolated installs: Claude Code's `settings.json`, agy's `antigravity-cli/settings.json` and OpenCode's `config/opencode/opencode.jsonc`. Each adapter names its file. Every other file is still owned whole.
- **Identity.** A key is a top-level key, or a variable of Claude Code's `env` (`env.FOO`), the identity the settings merge uses ([0023](0023-flat-profiles-and-layer-merge-rules.md), [0030](0030-tools-paths-and-project-config.md)). dotai owns the keys of the staged file.
- **Record.** Each settings file in `state.json` gains `keys`: the path and the SHA-256 of the value as canonical JSON. The journal records the keys an install is writing. The schema stays `dotai.state/v1`: the field is additive, nothing is released yet, and a record without `keys` reads as a file owned whole.
- **Install.** dotai sets each staged key, deletes each key it owned that the pack dropped and leaves every other key, through the JSONC edit layer, so comments, order and formatting stay. A key it owns whose value changed is refused, naming the file and the key, unless it already holds the value the pack now stages, which dotai then records; a missing key is set again. Deleting a key also deletes the comments on their own lines right above it, a known limit of the edit layer; the backup of the whole file keeps them. A key the harness wrote that the pack now sets for the first time is overwritten: the pack wins, after the whole file is backed up.
- **Emptied file.** A file left with no key at all is removed. A file in which dotai keeps no key, but that holds keys the harness wrote, stays without dotai's keys and is no longer owned.
- **Restore.** For every key an install owned before or after it, restore puts back what the file held before that install, from the backup copy, or deletes it; harness keys stay. A changed dotai key is a conflict under 0033's consent. Undoing every install removes the file only when no key is left. A file that is no longer JSON, or a record from before this decision, is restored whole.
- **Backups.** Still copies of the whole file; the checks between planning and writing still compare the whole file.

## Considered options

- **A (chosen):** own keys in settings files, keep whole-file ownership elsewhere.
- **B:** stop recording settings files. Rejected: an uninstall would leave dotai's keys behind.
- **C:** the harness wins a colliding key. Rejected: in isolated mode the pack's setup is the point.

## Consequences

- Good: a reinstall or a restore after using a harness works, and keeps what the harness wrote.
- Accepted cost: `/model` in Claude Code writes `model`, a key dotai owns, so the next install refuses it, naming `model`. That is intended: the user changed a dotai key. Delete the key to take the pack's value, put dotai's back, or set the model in the pack.
- Partially supersedes 0032 (whole-file ownership, for settings files) and 0033 (whole-file checks, for settings files); resolves 0013's pending record of owned keys for these files.

## Evidence

- Manual test of `dotai install` over a Claude Code launch, 2026-10-30.
- [CLI-JSON](../CLI-JSON.md): `install`, `restore` and the install state.

Update 2026-11-02 (roadmap PR 10-3): at project scope, the record in `.dotai/state.json` owns keys the same way, each with the value it held before dotai first set it, so that a key of the user's that a profile changed gets its value back from a later apply without that profile or from `dotai restore --project` ([0041](0041-project-scope-local-and-untracked.md)).
