---
status: accepted
date: 2026-11-20
tags: [engine, install, restore, harnesses, mcp]
---

# 0049 · Claude Code's user-scope MCP servers are owned server by server in its `.claude.json`

## Context

Claude Code reads user-scope MCP servers only from `~/.claude.json`, at the root of the home directory, not from `~/.claude/` ([HARNESS-NOTES](../HARNESS-NOTES.md)). For a pack with MCP servers, rulesync 43.10.0 renders that file into the user-scope staging tree as `.claude.json`, beside `.claude/`. The Claude Code adapter mapped only paths under `.claude/`, so every such install failed with `path-unmapped`, in both modes, with a hint that asked to add a harness the pack already had.

`.claude.json` mixes configuration with the state Claude Code writes on every run: the login, onboarding, statistics and per-project trust and history. [0038](0038-replace-mode-resets-harness-to-fresh-install.md) says dotai never owns `~/.claude.json` at user level, and only wipes its configuration keys in replace mode. With `CLAUDE_CONFIG_DIR` set, Claude Code reads `<dir>/.claude.json` instead: verified on 2.1.316 on Windows 11 ([HARNESS-NOTES](../HARNESS-NOTES.md#project-scope-inventory)), and documented ("The same applies to `.claude.json`", [Explore the .claude directory](https://code.claude.com/docs/en/claude-directory), read 2026-11-20).

## Decision

- **Mapping.** The adapter names the harness's file at the root of the home directory, `HomeConfig`. The staged `.claude.json` installs to `~/.dotai/isolated/claude/.claude.json` in the isolated mode, which `dotai run` points Claude Code at, and to the real `~/.claude.json` in the replace mode.
- **Ownership by key.** The file is owned key by key, as a settings file is under [0035](0035-settings-files-owned-by-key.md). In Claude Code's files each server of `mcpServers` is a key of its own, as each variable of `env` already is: dotai sets the servers the pack stages, deletes the ones it owned that the pack dropped, and keeps every other key and server, with its comments, order and line endings, through the JSONC edit layer. An `mcpServers` that a deletion empties goes. The file is created when it is missing, removed only when dotai's keys were all it held, and never replaced or removed whole: a record that owns it whole is an edited state, and stops the plan. A file that is not a JSON object stops the install, naming it.
- **Plan, backup, restore, doctor.** The plan lists a create, replace or remove per server; the install backs up the whole file and journals the op; `dotai restore` puts back what each server held before, or deletes it, and removes a file the install created when nothing else was written into it; `dotai doctor` reports a server dotai owns that changed or is gone.
- **Replace mode.** The reset still wipes the user's own `mcpServers` and per-project configuration keys, and keeps the rest of the file. It keeps the servers the previous install owns, as it keeps a project apply's. The wipe and the pack's servers are one write of the file, planned from what the wipe leaves; the backup holds one copy, which is the split's copy and the op's, so a rollback and a restore give the file back exactly.

## Considered options

- **A (chosen):** map the file and own each server by key, in both modes.
- **B:** write the servers into `settings.json`. Rejected: Claude Code does not load `mcpServers` there.
- **C:** own the whole file. Rejected: it holds the login and history, and Claude Code rewrites it constantly.
- **D:** drop the staged file with a warning. Rejected: the pack's servers would silently not install.

## Consequences

- Partially supersedes [0038](0038-replace-mode-resets-harness-to-fresh-install.md): at user level dotai now owns the MCP servers it writes into `~/.claude.json`, server by server, and nothing else of the file.
- Good: packs with MCP servers install in both modes, and a restore keeps what Claude Code wrote since.
- Accepted cost: a server dotai owns that the user edits with `claude mcp` is a conflict at the next install, as a changed settings key is.
- Accepted cost: in replace mode a running Claude Code can lose dotai's write if it rewrites the file at the same moment; the replace install refuses while Claude Code runs.
- Accepted cost: the same holds in the isolated mode while a Claude Code session started with `dotai run` is open, and an isolated install does not refuse while Claude Code runs. The window is the gap between the check of the file's hash and the atomic rename over it, and an install run again puts the servers back.
- Good: a `.claude.json` that dotai creates is written with mode `0600`, as Claude Code writes it, since a server may carry tokens in its `env` or headers; a file that is there keeps its mode.

## Evidence

- Bug report with a reproduction: a pack with one stdio server failed with `path-unmapped` in both modes, 2026-11-20.
- [HARNESS-NOTES](../HARNESS-NOTES.md): `.claude.json` under `CLAUDE_CONFIG_DIR`, and concurrent writes.
