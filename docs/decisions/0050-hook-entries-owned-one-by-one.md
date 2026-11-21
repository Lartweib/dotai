---
status: accepted
date: 2026-11-21
tags: [engine, install, restore, doctor, hooks, harnesses]
---

# 0050 · dotai owns its own hook entries, not the hook files other programs also write

## Context

[0035](0035-settings-files-owned-by-key.md) owns a settings file key by key, and [0049](0049-claude-user-mcp-servers-in-claude-json.md) owns each MCP server of `.claude.json` as a key of its own. Hooks were still owned coarsely, in two places:

- Claude Code's `settings.json` holds all hooks under one key, `hooks`, an object of events, each an array of matcher groups. dotai owned that key whole, so any entry another program appended to any event changed its value.
- agy's `config/hooks.json`, where rulesync writes a `rulesync` key, was not a settings file for dotai and was owned whole, by the SHA-256 of the file.

A terminal and agent manager that the maintainer uses re-registers its own status hooks every time it starts: it appends a group, `{"type":"command","command":"<its binary> claude","timeout":10}`, to many events of Claude Code's `settings.json` (`SessionStart`, `PreToolUse` with the matcher `*`, `PostToolUse`, `Stop`, `UserPromptSubmit`, `SubagentStart`), and adds a top-level key of its own beside `rulesync` in agy's `hooks.json`. After a replace install, `dotai doctor` then reported `key-changed` on `hooks` and `file-changed` on `hooks.json`, and every later install, such as a switch of variant, stopped on them, again at each start of that program. Any other program that registers hooks does the same.

OpenCode has no such shared file: rulesync's hooks plugin, `plugins/rulesync-hooks.js`, is a file of its own, and other plugins are separate files or entries of the `plugin` list of its settings file, which is already owned by key ([HARNESS-NOTES](../HARNESS-NOTES.md#hook-files-other-programs-write-to); not checked at runtime).

## Decision

dotai owns the hook entries it writes, and the top-level keys it writes in a hooks file, never the file or the `hooks` value as a whole.

- **Claude Code's `hooks`.** Each matcher group of an event is a key of its own, `["hooks", EVENT, MATCHER, ID]`: `MATCHER` is the group's `matcher`, `""` when it has none, and `ID` the command of its first command hook, or a hash of the group when it has none; a group that repeats an identity gets `#2`, `#3` after it. Its value is the group, hashed as canonical JSON. An event that is not an array stays one key, `["hooks", EVENT]`. This is the per-entry ownership of [0049](0049-claude-user-mcp-servers-in-claude-json.md), with an array element in place of an object member; the edit layer gains the edit of one element of an array, which keeps the bytes around it.
- **agy's `config/hooks.json`.** The adapter names it (`Hooks`), and it is owned key by key as a settings file is: dotai owns `rulesync` and any other top-level key the pack stages, and keeps the rest.
- **Install and update.** dotai sets each staged entry and each owned entry it still holds; an owned entry that is gone is written again; one whose group was edited stops the install, naming it, as a changed key does. An entry the pack dropped is deleted, and an event or `hooks` that this empties goes. Everything else, the foreign groups and keys, stays, with its comments, order and line endings.
- **Doctor.** `key-changed` and `key-missing` are reported for dotai's own entries only. Foreign entries are never reported.
- **Restore.** For each entry an install owned, restore puts back what the file held before it, or deletes the entry. A foreign entry added after the install stays, and a file that holds one is not removed. An own entry edited since is a conflict under [0033](0033-restore-from-the-install-history.md)'s consent, as any changed key.
- **Replace mode.** The reset is unchanged: hooks present before the install are wiped with the file and backed up. This applies to what is added after dotai wrote.
- **Records from before this decision.** A record that owns `hooks` as one key cannot tell dotai's groups from another program's: install drops that key and records the entries the pack stages, doctor does not report it, and restore asks as it always did. A record that owns `hooks.json` whole is handled the same way by install when the file changed; doctor and restore still report it, as a whole file, until the next install records its keys.

## Considered options

- **A (chosen):** own each hook group and each top-level key of the hooks file.
- **B:** declare the other program's hooks in the pack. Rejected: the program rewrites them when it starts, and they carry machine paths that tie the pack to one machine.
- **C:** leave it as it is. Rejected: doctor reports a problem at every start of that program, and install stops until the user moves the file away.
- **D:** own each command hook inside a group. Rejected: the program appends groups of its own, never commands to ours (observed on the maintainer's install); it would need edits inside nested arrays and would split groups when one is rewritten.
- **E:** identify a group by the hash of its content. Rejected: an edit of one of dotai's groups would read as a deleted entry plus a foreign one, not as the changed entry it is.

## Consequences

- Partially supersedes [0035](0035-settings-files-owned-by-key.md) and [0049](0049-claude-user-mcp-servers-in-claude-json.md): the `hooks` key of Claude Code's settings is owned entry by entry, and agy's hooks file is a file owned by key.
- Good: install, update and restore coexist with other programs' hooks, and plan, backup, journal, rollback and restore stay exact for dotai's part.
- Accepted cost: a program that appends a command hook into a group that dotai wrote changes that group, which is an edit of dotai's entry, reported and refusing an install. None of the observed programs does it.
- Accepted cost: when the pack changes the first command of a group, its old group is deleted and the new one added, not replaced in place; the group moves to the end of its event.
- Accepted cost: a pack that drops a hook while an install before this decision is recorded leaves the old group behind, since that record cannot say which group was dotai's. The next install records entries.
- Accepted cost: a foreign group whose matcher and first command equal one of dotai's is that entry for dotai.

## Evidence

- Observation on the maintainer's real install, 2026-11-21: `dotai doctor` after a replace install reported `key-changed` on `hooks` of `~/.claude/settings.json` and `file-changed` on `~/.gemini/config/hooks.json`, written by another program's start.
- [HARNESS-NOTES](../HARNESS-NOTES.md#hook-files-other-programs-write-to): the shapes of those files.
