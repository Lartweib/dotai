---
status: accepted
date: 2026-11-02
tags: [engine, project-scope, git, harnesses, merge, onboarding]
---

# 0041 · The project scope stays local: excluded from git, never in tracked files

## Context

Roadmap PR 10 applies profiles to a project (`dotai apply --profile`). [0030](0030-tools-paths-and-project-config.md) gives the project a `.dotai/` directory with the engine's record of applied profiles, `config.yaml` of the user and the pack, and `tools/`. Project outputs can hold absolute local paths through `${DOTAI_PROJECT_TOOLS}`, and a work or shared repository must not see one person's tooling. rulesync renders project output to the files a team usually commits: `CLAUDE.md`, `AGENTS.md` (shared by agy and OpenCode), `.mcp.json`, `.claude/settings.json` and the root `opencode.jsonc` ([HARNESS-NOTES](../HARNESS-NOTES.md#rulesync-4100)).

What each harness reads in a project was checked in sessions on Windows ([HARNESS-NOTES](../HARNESS-NOTES.md#project-scope-inventory)). The fact the design rests on: a file listed in `.git/info/exclude` loads as usual in all three harnesses.

## Decision

**`.dotai/` stays local.** It is never committed. On apply the engine adds `.dotai/` to `.git/info/exclude` (the path `git rev-parse --git-path info/exclude` gives). It never edits `.gitignore`.

**Created files are excluded too.** Every file or directory dotai creates in the project is added to `.git/info/exclude`, in one block that dotai owns and removes entries from when it removes the file.

**Tracked files are never modified.** Before writing, the plan asks git whether each target is tracked. dotai writes each harness's project-local variant (table below). A target that is tracked is never written, merged or removed; if the harness has no other local place for that item, the plan skips it and warns, naming the harness, the item and the tracked file. An existing untracked file that dotai does not own is merged by key when it is a settings or configuration file ([0013](0013-renderer-merges-by-key.md), [0035](0035-settings-files-owned-by-key.md)) and is a conflict otherwise. Outside a git repository there is nothing to exclude from, and the plan says so.

**Where project output goes.** The engine moves rulesync's project output to these targets:

| Kind | Claude Code | OpenCode | agy |
|---|---|---|---|
| Instructions (root rule) | `.claude/rules/<pack>.md`; with `@../../AGENTS.md` when the project has `AGENTS.md` and no `CLAUDE.md` | a file in `.opencode/`, named in `instructions` of `.opencode/opencode.json` | `.agents/rules/<pack>.md`, `trigger: always_on` |
| Other rules | `.claude/rules/` | `.opencode/memories/`, named in `instructions` | `.agents/rules/` |
| Settings and permissions | `.claude/settings.local.json`, by key | `.opencode/opencode.json`, by key | no local slot: warn |
| Hooks | `.claude/settings.local.json` | `.opencode/plugins/rulesync-hooks.js` | `.agents/hooks.json` |
| Agents, skills, commands | `.claude/agents/`, `skills/`, `commands/` | `.opencode/agents/`, `skills/`, `commands/` | `.agents/agents/`, `skills/`; agy has no commands |
| MCP | `~/.claude.json`, `projects["<path>"].mcpServers`, by key | `.opencode/opencode.json`, by key | `.agents/mcp_config.json`, by key |

`AGENTS.md`, `CLAUDE.md`, `CLAUDE.local.md`, `.mcp.json`, `.claude/settings.json` and the root `opencode.json(c)` are never targets: they are the user's or the team's.

**Merges.** Settings merge by key ([0013](0013-renderer-merges-by-key.md), [0035](0035-settings-files-owned-by-key.md)); hooks and permissions merge as a union ([0040](0040-list-files-merged-as-a-union.md)). The ownership record of the project lives in `.dotai/`.

**Marker for the pack's first-run analysis.** The engine supports it generically and nothing more: the project record in `.dotai/` is the marker of a first apply, and `dotai apply --json` reports whether this apply is the first in the project. The analysis itself, of skills and configuration that clash with the pack and the cleanup it suggests, is pack content: the pack's onboarding agent runs it, and keeps its own "done" file in `.dotai/`, which the engine excludes with the rest and never reads.

## Considered options

- **A (chosen):** local variants, `.git/info/exclude`, tracked files untouched.
- **B:** write the files rulesync renders and let the user commit them. Rejected: absolute local paths and personal tooling reach shared repositories, and tracked team files change.
- **C:** edit `.gitignore`. Rejected: it is a tracked file.
- **D:** a marker the engine owns and checks before running the analysis. Rejected: the engine cannot run an agent; the pack decides when the analysis is done.

## Consequences

- Partially supersedes [0012](0012-rules-file-per-harness.md) at project scope (instructions go to rules files, not `CLAUDE.md` or `AGENTS.md`) and [0030](0030-tools-paths-and-project-config.md) (`.dotai/` is local and excluded). Amends [0011](0011-mods-always-project-scoped.md), [0013](0013-renderer-merges-by-key.md) and [0038](0038-replace-mode-resets-harness-to-fresh-install.md) (dotai owns per-project MCP keys in `~/.claude.json`).
- Good: applying a profile leaves `git status` clean and never changes what the team shares.
- Accepted cost: a fresh clone or `git clean -fdx` loses `.dotai/`, `config.yaml` included; an export can come later.
- Accepted cost: agy gets no project settings or permissions, and an item whose only local place is a tracked file is skipped with a warning.
- Accepted cost: OpenCode also reads the project's `.claude/skills` and `.agents/skills`, so a skill applied for every harness appears more than once there. A user-level Claude Code skill hides a project skill of the same name; in agy the project one wins. The plan names each clash it can see.
- Pending: whether Claude Code loads a project `AGENTS.md` in a normal configuration, which decides whether the import is needed; `${DOTAI_TOOL:<name>}` in hook commands, with the quoting per harness checked in [HARNESS-NOTES](../HARNESS-NOTES.md#hook-commands); the name of the record in `.dotai/`.

## Evidence

- Maintainer decision, 2026-11-02.
- [HARNESS-NOTES](../HARNESS-NOTES.md#project-scope-inventory): what each harness reads in a project, and excluded files loading.
- [ROADMAP](../ROADMAP.md): rows 10-1 to 10-4.
