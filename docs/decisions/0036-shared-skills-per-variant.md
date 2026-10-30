---
status: accepted
date: 2026-10-31
tags: [engine, pack, install, isolation, harnesses, skills]
---

# 0036 · A variant can share the user's skills folder in isolated mode

## Context

Other tools install skills into `~/.agents/skills`, the user's shared skills folder, often as links. The isolated mode ([0018](0018-install-modes-and-mandatory-backup.md), [0031](0031-isolated-install-root.md)) hides that folder: Claude Code and agy have no such folder, and `dotai run opencode` points `OPENCODE_TEST_HOME` at an empty directory so that OpenCode does not load it. A pack variant built around those tools needs their skills; every other variant must keep the isolated install closed. This answers open question 4: skills that other tools install in `~/.agents/skills` are not visible in isolated mode unless the variant asks for them.

## Decision

- **Manifest.** A variant declares `shared-skills: true` in `dotai.yaml`. Absent or `false`, the install is closed: only the pack's content is visible. Only variants take the field, and dotai never detects it.
- **State.** `state.json` records `sharedSkills` for the installed variant.
- **OpenCode** reads `~/.agents/skills` itself, so `dotai run opencode` does not set `OPENCODE_TEST_HOME` when the state shares skills, and OpenCode uses the real home directory. An inherited `OPENCODE_TEST_HOME` is removed in both modes.
- **Claude Code and agy.** `install` links each entry of `~/.agents/skills` that is a skill, a directory with a `SKILL.md`, followed through links, into the isolated skills directory: `isolated/claude/skills/<name>` and `isolated/gemini/config/skills/<name>`. Each link points at the entry in `~/.agents/skills`, not at what it resolves to. Windows gets a directory junction, which needs no administrator rights; other systems a symbolic link. Hidden entries are ignored; any other entry that is not a skill is skipped with a note. A missing folder links nothing, with a note.
- **Collision.** When the pack has a skill of the same name for that harness, the pack's skill wins and install warns, naming the shared skill it skipped. An error would block installing the variant whenever another tool adds a skill with a name the pack also uses, which the user cannot fix in the pack.
- **Ownership.** A link is owned like a file: the state records it as `kind: link` with its destination, the plan creates, removes or leaves it unchanged, and a reinstall adds and removes links as the folder changed. Its content is its destination: a link dotai owns that points elsewhere is an edit and is refused, a real file or directory where a link goes is refused, and nothing ever follows a link to remove, hash, back up or check it. A backup records a link in its manifest, without a copy, and restore and rollback recreate it from there.
- **Freshness.** A skill added to `~/.agents/skills` after an install shows up after the next install.

## Considered options

- **A (chosen):** opt-in per variant, links per skill, owned like files.
- **B:** always share. Rejected: a closed isolated install is the point of trying a pack.
- **C:** detect the tools that install skills. Rejected: the maintainer wants the pack to declare it.
- **D:** one link for the whole folder. Rejected (inferred): it would hide the pack's own skills, which install into the same directory.

## Consequences

- Good: the variant that uses those tools sees their skills in every harness, and every other variant stays closed.
- Accepted cost: with shared skills, OpenCode's home is the real one, so whatever else OpenCode reads from the home directory loads too; the leaks say so.
- Accepted cost: when the pack drops a skill that the folder also has, the link to it is made by the install after the one that removes the pack's skill.
- Amends [0018](0018-install-modes-and-mandatory-backup.md) and [0031](0031-isolated-install-root.md) (what the isolated mode hides), [0032](0032-install-state-backup-and-isolated-launch.md) (the ownership record and backups hold links) and [0033](0033-restore-from-the-install-history.md) (restore puts links back).
- Pending: `dotai doctor` reporting skills added since the install.

## Evidence

- [HARNESS-NOTES](../HARNESS-NOTES.md): OpenCode reads `~/.agents/skills` from its home directory.
- [CLI-JSON](../CLI-JSON.md): `install`, `run`, the install state and the backup manifest.
