---
status: accepted
date: 2026-10-30
tags: [product, scope, roadmap, v1, install]
---

# 0034 · The replace install mode is part of v1

> **Partially superseded by [0038](0038-replace-mode-resets-harness-to-fresh-install.md): replace wipes by keep-list instead of an allowlist,** splits `~/.claude.json` by key, and wipes generic paths such as `~/.agents/skills` for a closed variant. Still in force: replace in v1 for the three harnesses, after isolated mode for agy and OpenCode, isolated stays, and `dotai restore` is the way back.

## Context

[0018](0018-install-modes-and-mandatory-backup.md) defines three install modes. Replace backs up, removes the files the harness owns and applies the pack, working on an allowlist of owned paths declared by each adapter. [0020](0020-v1-scope-engine-and-pack.md) put the replace mode after v1, [0031](0031-isolated-install-root.md) states that isolated is the only mode in v1, and the [roadmap](../ROADMAP.md) followed them.

The maintainer's motivation for dotai is to replace an existing configuration with a clean one built for their own use. With isolated as the only v1 mode, v1 can install a pack next to the current setup but never for real.

What replace needs now exists: the isolated install ([0031](0031-isolated-install-root.md)), the plan, the backup, the journal and the install state ([0032](0032-install-state-backup-and-isolated-launch.md)), and `dotai restore` ([0033](0033-restore-from-the-install-history.md)).

## Decision

The replace mode is part of v1, for the three v1 harnesses ([0019](0019-v1-harness-targets.md)): Claude Code, agy and OpenCode. It comes after the isolated mode for agy and OpenCode (roadmap PR 9b, after PR 9).

- **Same pipeline.** Replace reuses the plan, the backup, the journal, the state and `dotai restore` as they are. Only the targets change: the harness's real configuration directory instead of its directory under `~/.dotai/isolated/`.
- **Allowlist.** Each adapter declares, as data, the paths it owns in the real configuration directory. Replace removes or writes only those paths, never a whole directory. The allowlist never includes credentials, `~/.claude.json`, or a shared path read by several harnesses, such as `~/.agents/skills` (0018 rules 2 and 3).
- **Harness closed.** The harness must be closed, as 0018 rule 5 requires for replace and restore.
- **Isolated stays.** Isolated remains the way to try a pack first, next to the current configuration.
- **The way back.** `dotai restore` undoes a replace like any other install: it brings back the user's original configuration from the backup.

This supersedes only the part of 0020 that puts the replace mode after v1, and the sentence of 0031 that makes isolated the only mode in v1. Everything else in 0020 and 0031 is still in force.

## Considered options

- **A (chosen):** replace in v1 for the three v1 harnesses, after isolated mode for agy and OpenCode.
- **B:** keep replace after v1, as 0020 decided. Rejected: v1 could not be used for real, which is the maintainer's reason for building it.
- **C:** replace for Claude Code only first. Rejected (inferred): agy and OpenCode reuse the same pipeline, and each adds only an allowlist in its adapter, so splitting them adds a step without lowering the risk of the pipeline.

## Consequences

- Partially supersedes [0020](0020-v1-scope-engine-and-pack.md): replace moves from "after v1" into v1. Partially supersedes [0031](0031-isolated-install-root.md): isolated is no longer the only v1 mode.
- Good: v1 can install a pack over the real configuration, and go back to it with `dotai restore`.
- Accepted cost: each adapter carries an allowlist of owned paths in the real configuration directory, and a wrong entry could remove a user's file. The backup and restore limit the damage, not the mistake.
- Pending: the allowlist of each adapter; how dotai checks that the harness is closed; the checks of [0033](0033-restore-from-the-install-history.md), which accept only targets inside an isolated directory or the tools directory, extended to the allowlist.

## Evidence

- Maintainer decision, 2026-10-30.
- [0018](0018-install-modes-and-mandatory-backup.md), [0020](0020-v1-scope-engine-and-pack.md), [0032](0032-install-state-backup-and-isolated-launch.md), [0033](0033-restore-from-the-install-history.md).

Update 2026-11-01: the pending items are decided by [0038](0038-replace-mode-resets-harness-to-fresh-install.md): each adapter declares a keep-list for its harness directories and explicit leak entries outside them, install and restore refuse while a selected harness is detected running unless `--ignore-running`, and restore's checks cover the replace targets. Roadmap row 9b is split into 9b-1 to 9b-4.
