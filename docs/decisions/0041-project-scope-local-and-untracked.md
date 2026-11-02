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

**Tracked files are never modified.** Before writing, the plan asks git whether each target is tracked. dotai writes each harness's project-local variant (table below). A tracked target is never written, merged or removed. When the harness has no other local place, the plan lists by name each item it leaves uninstalled, such as each hook when `.agents/hooks.json` is tracked, not only the file. An existing untracked file that dotai does not own is merged by key when it is a settings or configuration file ([0013](0013-renderer-merges-by-key.md), [0035](0035-settings-files-owned-by-key.md)) and is a conflict otherwise.

**Where project output goes.** The engine moves rulesync's project output to these targets:

| Kind | Claude Code | OpenCode | agy |
|---|---|---|---|
| Instructions (root rule) | `.claude/rules/<pack>.md` | a file in `.opencode/`, named in `instructions` of `.opencode/opencode.json` | `.agents/rules/<pack>.md`, `trigger: always_on` |
| Other rules | `.claude/rules/` | `.opencode/memories/`, named in `instructions` | `.agents/rules/` |
| Settings and permissions | `.claude/settings.local.json`, by key | `.opencode/opencode.json`, by key | no local slot: the plan warns |
| Hooks | `.claude/settings.local.json` | `.opencode/plugins/rulesync-hooks.js` | `.agents/hooks.json` |
| Agents, skills, commands | `.claude/agents/`, `skills/`, `commands/` | `.opencode/agents/`, `skills/`, `commands/` | `.agents/agents/`, `skills/`; agy has no commands |
| MCP | `~/.claude.json`, `projects["<path>"].mcpServers` | `.opencode/opencode.json`, by key | `.agents/mcp_config.json`, by key |

`AGENTS.md`, `CLAUDE.md`, `CLAUDE.local.md`, `.mcp.json`, `.claude/settings.json` and the root `opencode.json(c)` are never targets: they are the user's or the team's.

**Claude Code instructions.** `.claude/rules/<pack>.md` has no `paths:` frontmatter, so it always loads. When the project has an `AGENTS.md` that no `CLAUDE.md` imports, the file adds `@../../AGENTS.md`: an import in a rules file resolves from the file. `CLAUDE.local.md` is never written, since it may be the user's own file.

**Claude Code project MCP.** Servers go to `~/.claude.json` under `projects["<path with forward slashes>"].mcpServers`, Claude Code's local scope; the backslash form of the path is ignored. dotai edits the file through its JSONC layer, never through `claude mcp add-json`, so the plan, the backup and the restore show the change. It owns only the server names the pack declares, recorded in `.dotai/`; a foreign entry with the same name is refused. Writing while Claude Code runs is allowed, and the running-harness refusal does not apply here: Claude Code re-reads the file before each write, but its read and write are not atomic, so the writer retries on `EPERM`, re-reads after writing to confirm its keys, and rewrites once if they were lost. `dotai doctor` reports owned keys that went missing later, and orphan entries whose path no longer exists. Each worktree is its own path, with its own entry and its own `.dotai/`; `.git/info/exclude` is shared between them. A replace install treats these keys as dotai's and never wipes them.

**Merges.** Settings merge by key ([0013](0013-renderer-merges-by-key.md), [0035](0035-settings-files-owned-by-key.md)); hooks and permissions merge as a union ([0040](0040-list-files-merged-as-a-union.md)). The ownership record of the project lives in `.dotai/`.

**First apply and the pack's directory.** `dotai apply --json` reports whether this apply is the first in the project. `.dotai/packs/<pack>/` is reserved for the pack's own files, such as the marker of its onboarding and its analysis of skills and configuration that clash with the pack. The engine never touches it in apply or replace; undoing the project apply removes it through the backup like any removal.

**Outside a git repository.** Apply writes anyway, warns, and records the exclude entries as pending; the next apply or `dotai doctor` adds them once `.git` exists. Apply refuses in the home directory and at the root of a drive, where the project scope would overlap the user scope.

## Considered options

- **A (chosen):** local variants, `.git/info/exclude`, tracked files untouched.
- **B:** write the files rulesync renders and let the user commit them. Rejected: absolute local paths and personal tooling reach shared repositories, and tracked team files change.
- **C:** edit `.gitignore`. Rejected: it is a tracked file.
- **D:** an engine-owned marker that decides when to run the pack's analysis. Rejected: the engine cannot run an agent; the pack decides.
- **E:** `CLAUDE.local.md` for Claude Code instructions, or `claude mcp add-json` for MCP. Rejected: the first may be the user's file; the second hides the change from the plan and the backup.

## Consequences

- Partially supersedes [0012](0012-rules-file-per-harness.md) at project scope (instructions go to rules files, not `CLAUDE.md` or `AGENTS.md`) and [0030](0030-tools-paths-and-project-config.md) (`.dotai/` is local and excluded). Amends [0011](0011-mods-always-project-scoped.md), [0013](0013-renderer-merges-by-key.md) and [0038](0038-replace-mode-resets-harness-to-fresh-install.md) (dotai owns per-project MCP keys in `~/.claude.json`, which replace keeps).
- Good: applying a profile leaves `git status` clean and never changes what the team shares.
- Accepted cost: a fresh clone or `git clean -fdx` loses `.dotai/`, `config.yaml` included; an export can come later.
- Accepted cost: agy gets no project settings or permissions in v1.
- Accepted cost: OpenCode also reads the project's `.claude/skills` and `.agents/skills`, so a skill applied for every harness appears more than once there. A user-level Claude Code skill hides a project skill of the same name; in agy the project one wins. The plan names each clash it can see.
- Accepted cost: an edit Claude Code makes to `~/.claude.json` during dotai's write can still drop a key after the check; `doctor` reports it.
- Pending: the name of the record in `.dotai/`; follow-ups in [ROADMAP](../ROADMAP.md) (`${DOTAI_TOOL:<name>}` in hook commands, identical MCP definitions across profiles, the shared-and-per-harness hook warning, agy `SessionStart` output in PACK-FORMAT).

## Evidence

- Maintainer decision, 2026-11-02.
- [HARNESS-NOTES](../HARNESS-NOTES.md#project-scope-inventory): what each harness reads in a project, excluded files loading, `~/.claude.json` keys and concurrent writes.
- [ROADMAP](../ROADMAP.md): rows 10-1 to 10-4.

Update 2026-11-02: the plan of roadmap PR 10-2 settles what this record left open or did not say. The project record is `.dotai/state.json`, as `~/.dotai/state.json` is at user level. Claude Code's project MCP servers go to the `.claude.json` it reads: `~/.claude.json`, or `~/.dotai/isolated/claude/.claude.json` when the install state records an isolated install of Claude Code, since `CLAUDE_CONFIG_DIR` moves the file ([HARNESS-NOTES](../HARNESS-NOTES.md#project-scope-inventory)). The project is the current directory, not the top of its repository, and its exclude entries are anchored at the top of the repository. OpenCode's hooks plugin goes to `.opencode/plugins/` as rendered, and agy gets the commands as skills, as rulesync renders them. Claude Code's `.claude/rules/<pack>.md` exists only to import `AGENTS.md`, since profiles carry no root rule, and a `CLAUDE.md` that is a link counts as one that imports it. An existing file that dotai does not own and that is not merged by key is planned as a `conflict` and left as it is. The plan and its codes are in [CLI-JSON](../CLI-JSON.md).
