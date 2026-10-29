---
status: accepted
date: 2026-10-30
tags: [engine, restore, backup, state]
---

# 0033 · Restore follows the install history kept in the backups

## Context

[0018](0018-install-modes-and-mandatory-backup.md) says `dotai restore` brings a backup back, checking hashes first and asking before it overwrites a file changed after the install. [0032](0032-install-state-backup-and-isolated-launch.md) fixed the backup and state formats and left restore to roadmap PR 8b. The backups of PR 8a record each operation and what was installed, but not the ownership record the install replaced, so a restore could put files back and still leave `state.json` describing the install it undid.

## Decision

**Command.** `dotai restore [backup-id] [--yes]`. Without an id it undoes the install the state names, the last one that wrote files. With one it undoes every install from the newest down to the one that took that backup, newest first, so the disk ends as it was before it; the oldest backup undoes everything dotai installed.

**Prior state.** Each install backup keeps `state.json`, the state the install started from (settled, without a journal), synced with the copies, and the manifest records it as `priorState` (its file and SHA-256, or `none`). Restore puts it back, or deletes `state.json` for `none`. The installs to undo are found through it: the state names the last backup, and each backup's prior state names the one before. A backup without `priorState`, taken before this change, is refused and named; restore does not rebuild a state it cannot know. A backup that was pruned stops a restore that would go past it, so retention protects this history: restored installs and restore backups are pruned first, oldest first, and the history only beyond its newest 10 backups (besides the oldest, never deleted).

**Checks first.** Before any write: each manifest is one an install wrote for this home; every target is inside a harness's isolated directory or the tools directory; every copy, and the prior state, has the SHA-256 its manifest records. Each target is then followed from the disk through each install, newest first. A target that holds neither what that install wrote nor what the restore leaves there is a conflict; a missing file is not.

**Consent.** On a terminal without `--json`, the conflicts are listed and asked about once (y/N), before `~/.dotai/lock` is taken; otherwise the restore refuses with exit code 1 unless `--yes`, and the JSON error carries the conflicts. Under the lock the plan is made again; if the state, the conflicts or any target changed, the restore refuses and asks to run it again.

**Restore backup.** Every file the restore replaces or deletes, conflicts or not, is first copied into a backup `<timestamp>_restore` of the same format, whose prior state is the state before the restore. It then writes a journal and writes as an install does; a failure or Ctrl-C rolls back from that backup.

**Journal.** A journal is settled by restore itself, without the pack. An interrupted install is undone as the newest install: what it replaced or removed comes back from its copies, what it created goes only when it holds what it wrote, and a target it had not reached yet holds what it found; anything else is a conflict. An interrupted restore is rolled back from its `_restore` backup the same way, and the restore goes on, so that without an id it undoes the install the state names. An install that settles a restore's journal keeps the backup the state names.

**History.** Undone backups stay, marked `restored` in their manifest; retention still applies, and a restore prunes too, keeping its backup and the one the state names. Credentials are never in a manifest, so restore never touches them. When no install is left, the empty harness directories, the tools directory and the isolated root are removed; one that holds a login stays, and `backups/` and `~/.dotai` never go. A harness that may be running gets a warning, not a check (0018 rule 5).

## Considered options

- **A (chosen):** the prior state in each backup, and the chain it forms.
- **B:** rebuild the state of an older backup by dropping the undone ownership. Rejected as less safe: the variant, harnesses and files owned before are guesses.
- **C:** order backups by time instead of the chain. Rejected: restored and restore backups mix in, and a clock that went back reorders them.
- **D:** copy only the conflicts into the restore backup. Rejected: deleting a file an install created could not be rolled back.
- **E (chosen):** settle a journal inside restore, rather than refusing it until an install settles it, which needs the pack and takes two more restores to undo.

## Consequences

- Good: a restore is checked whole before it writes, and loses nothing, not even a file changed after the install.
- Accepted cost: after more than 10 installs in the history, the oldest backups beyond them cannot be reached.
- Out of scope: a command to undo a restore; its backup can be put back by hand.

## Evidence

- [CLI-JSON](../CLI-JSON.md): `restore`, the install state and the backup manifest.
