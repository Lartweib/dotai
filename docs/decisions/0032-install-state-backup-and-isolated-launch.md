---
status: accepted
date: 2026-10-29
tags: [engine, install, backup, state, isolation]
---

# 0032 · Install state, backup layout and the isolated launch command

## Context

[0018](0018-install-modes-and-mandatory-backup.md) requires a backup before any write and an alias command per harness, and sketches the backup format. [0013](0013-renderer-merges-by-key.md) requires an ownership record but leaves where it is stored pending. [0028](0028-plan-centric-hexagonal-engine.md) fixes the pipeline (plan → backup → execute → record state) and that execution stops when a target changed after planning. [0031](0031-isolated-install-root.md) leaves the alias command pending. Roadmap PR 8 is split into 8a (`dotai install` executes the isolated plan), 8b (`dotai restore`) and 8c (launching a harness isolated).

## Decision

**Launch.** A harness is launched isolated with `dotai run <harness> [args]` (PR 8c), not with a shim on `PATH`. A shim such as `claude-dotai` may come later as a shortcut to it.

**State.** `~/.dotai/state.json`, schema `dotai.state/v1`, readable by the user only (`0600`) and always written atomically (a temporary file in `~/.dotai/` renamed over it). It holds the pack (name, version, absolute path), the variant, the mode (`isolated`), when it was installed, the harnesses, the id of the backup of the last install that wrote files, and the ownership record: every file dotai installed, with its target, harness, SHA-256 and exec bit.

**Journal.** Before its first write, an install writes into the state a `pending` journal: its backup and each operation with the content before and after. The final state, written last, drops it; so does a successful rollback, which puts back the state the install started from (or removes it when there was none). A journal that is still there means the install was killed or its rollback failed. The next install then treats a target of the journal as dotai's when it holds what the record or the journal says dotai wrote, and as the user's own file, neither edited nor removed, when it still holds what dotai found there and never owned; anything else is an edit. So a crash never wedges later installs.

**Backup.** As in 0018: `~/.dotai/backups/<timestamp>_install/manifest.json` and `files/<target relative to the home directory>`. The timestamp is UTC with milliseconds (`20261029T101500.000Z`), so names are valid on Windows. The manifest, schema `dotai.backup/v1`, holds the operation, mode, pack, variant, harnesses, when it was taken and, per written target, its action, the SHA-256 and exec bit before (absent for a create) and after (absent for a remove). Only replaced and removed targets have a copy; created targets are recorded so that restore can delete them. Nothing that is not a plan target is ever copied, so credentials in an isolated directory are never backed up. Backup directories are `0700` and their files `0600`; every copy, the manifest and their directories are synced to the disk before the first write. An install that writes no file takes no backup.

**Ownership.** A target owned by the previous install and absent from the new plan is removed, backed up like a replace, and the directories it leaves empty are removed too, below a harness's isolated directory or the tools directory, never those directories themselves. An owned target whose content differs from the record (and from the journal, see above) was edited by the user: the install refuses before writing anything and names the files. A `--force` that overrides this is out of scope for now.

**Writes.** Every write into the home directory goes through an `os.Root` opened at the harness's isolated directory, the tools directory, `~/.dotai` or the backup, which refuses a name or link that leads outside it, so a directory swapped for a link or a junction after the checks cannot redirect a write. The residual race is the moment between checking the directories from the home directory down to that root and opening it. A replaced file keeps the stricter of its permissions and the planned ones, with the exec bit as planned. On Windows, a move or a removal that another program blocks for a moment (access denied, sharing violation) is retried a few times with a short backoff, and a read-only attribute is cleared before a target is replaced or removed.

**Rollback.** A failure or a cancellation stops the install and rolls back, with a context of its own bounded to a minute, so that Ctrl-C still undoes what was written. A target that is neither what the install wrote nor what was there is left and reported.

**Retention.** After a successful install, only the last 10 complete backups are kept, plus the oldest complete one, which is never deleted: it holds what was there before dotai's first write. Backups are ordered by the time their manifest records, then by id, never only by name, so a clock that went back does not reorder them. The backup just taken and the one the state names are never deleted. An incomplete backup (no manifest) does not count and is deleted: under the lock, only an install that stopped can have left it.

**Concurrency.** An install plans and validates first and writes nothing for a pack with problems. Only when there is something to write does it take `~/.dotai/lock`, created exclusively, and check under it that the state is still the one it planned from. A lock that is already there is an error naming its path, the process id written in it and its age; dotai never breaks a lock by itself.

**Warnings.** A cleanup that fails after the install succeeded (removing the temporary staging, pruning backups, removing the lock) is a warning in the result, not a failure.

## Considered options

- **A (chosen):** `dotai run` as the launch command, a state file and plain-file backups under `~/.dotai/`.
- **B:** a shim per harness on `PATH`. Not chosen for now: it means writing outside `~/.dotai/` and managing `PATH`; it can be added later on top of `dotai run`.

The sources record no other option for the state and backup layout, which follows 0018.

## Consequences

- Resolves the pending item of [0013](0013-renderer-merges-by-key.md) for files: the ownership record lives in `state.json`. Owned keys come with the project scope.
- Resolves the alias command left pending by [0031](0031-isolated-install-root.md).
- Good: a backup and the state can be read and undone by hand.
- Accepted cost: a lock left by a dotai that was killed must be removed by hand; the error names its process and age to help decide.
- Pending: `dotai restore` (PR 8b) and `dotai run` (PR 8c).

## Evidence

- [0018](0018-install-modes-and-mandatory-backup.md), [0028](0028-plan-centric-hexagonal-engine.md), [0031](0031-isolated-install-root.md).
- [CLI-JSON](../CLI-JSON.md): the formats of `state.json` and the backup manifest.
