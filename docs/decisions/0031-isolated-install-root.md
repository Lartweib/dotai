---
status: accepted
date: 2026-10-29
tags: [engine, install, isolation, harnesses]
---

# 0031 · Isolated installs live under `~/.dotai/isolated/`

> **Partially superseded by [0034](0034-replace-mode-in-v1.md): isolated is no longer the only mode in v1;** the replace mode is part of v1 too. Everything else is still in force.

## Context

[0018](0018-install-modes-and-mandatory-backup.md) decides that the isolated mode installs a pack "into a separate configuration directory per harness", and names each harness's relocation mechanism, but not where those directories are. The plan ([0028](0028-plan-centric-hexagonal-engine.md)) needs an absolute target for every staged file before anything is written. The user-scope staging tree holds each harness's output at the path it has under the home directory (`.claude/`, `.gemini/`, `.config/opencode/`) and the tools at `.dotai/tools/` ([CLI-JSON](../CLI-JSON.md), `stage`). Isolated is the only mode in v1.

## Decision

The isolated mode installs the user scope into one directory per harness under `~/.dotai/isolated/`, and points the harness at it:

| Harness | Directory | Pointed at by | Staging path → install path |
|---|---|---|---|
| Claude Code | `~/.dotai/isolated/claude/` | `CLAUDE_CONFIG_DIR` | `.claude/<rest>` → `<dir>/<rest>` |
| agy | `~/.dotai/isolated/gemini/` | `--gemini_dir` | `.gemini/<rest>` → `<dir>/<rest>` |
| OpenCode | `~/.dotai/isolated/opencode/` | `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME` set to `<dir>/config`, `<dir>/data`, `<dir>/state`, `<dir>/cache` | `.config/opencode/<rest>` → `<dir>/config/opencode/<rest>` |

Tools are not isolated: `.dotai/tools/<rest>` installs to `~/.dotai/tools/<rest>`, which dotai already owns ([0030](0030-tools-paths-and-project-config.md)). Each harness adapter declares its directory and mapping as data. A staged user-scope file that no harness of the pack and no tools rule maps is an error; nothing is dropped silently. rulesync's sources (`.rulesync/`) are the input of the render and are not installed.

## Considered options

- **A (chosen):** one directory per harness under the dotai directory.

The sources record no other option.

## Consequences

- Good: everything an isolated install writes is under `~/.dotai/`, and the user's own configuration is never a target.
- Good: a new harness adds a directory and a mapping in its adapter; the plan does not change.
- Pending: the alias command of [0018](0018-install-modes-and-mandatory-backup.md) (for example `claude-dotai`) and launching a harness isolated, including OpenCode's disable variables and service port, come with roadmap PRs 8 and 9.
- Pending: a separate OpenCode data root per major version ([0018](0018-install-modes-and-mandatory-backup.md)).

## Evidence

- [CLI-JSON](../CLI-JSON.md), `stage`: the paths of the user-scope staging tree.
- [0018](0018-install-modes-and-mandatory-backup.md): the relocation mechanism of each harness.

Update 2026-10-30: an adapter may also move a staged directory to another path of its install, as data. agy's skills install at `<dir>/config/skills/<rest>` instead of `<dir>/antigravity-cli/skills/<rest>`, where rulesync writes them: on its first run, agy (1.4.7) moves `antigravity-cli/skills` to `config/skills`, writes `config/.migrated` and then fails to link the old path back on Windows. A pack that stages a skill at both paths is an error, since both install to the same target.
