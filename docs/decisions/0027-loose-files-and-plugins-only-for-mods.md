---
status: accepted
date: 2026-10-26
tags: [renderer, plugins, mods, claude-code, rulesync]
---

# 0027 · Claude Code content as loose files; plugins only for mods

## Context

[0013](0013-renderer-merges-by-key.md) put skills, commands, agents, hooks, MCP and mods into one Claude Code plugin per pack, so that uninstalling a pack meant removing its plugin and its recorded keys. That predates [0021](0021-go-engine-orchestrates-rulesync.md), where a pinned rulesync renders the content.

A verification on rulesync 41.0.0 and Claude Code 2.1.310 (2026-10-26) found:

- rulesync has a `claudecode-plugin` target. It writes the plugin's default folders (`skills/`, `agents/`, `commands/`, `hooks/hooks.json`, `.mcp.json`), but no `.claude-plugin/plugin.json`, no rules and no permissions. With a minimal manifest added, `claude plugin validate` passes.
- Inside a plugin, names change: skills become `/<plugin>:<skill>`, agents `<plugin>:<agent>`, and MCP tools `mcp__plugin_<plugin>_<server>__<tool>`. Hook matchers and permission rules written against the plain names must be rewritten, and rulesync does not do it.
- A plugin can stay inside the project only when it lives under `.claude/skills/<name>/` with a `.claude-plugin/plugin.json`; it loads with `scope: project` after workspace trust. Installing from a local marketplace with `--scope project` writes plugin state under the user configuration directory.
- A mod is a plugin with a hooks module (JavaScript hooks that can draw panes and add commands), so a mod can only be delivered as a plugin.

The clean uninstall that motivated the plugin is also covered by the ownership record of [0013](0013-renderer-merges-by-key.md), which lists every file and key dotai wrote.

## Decision

- Skills, commands, agents, hooks and MCP go to Claude Code as loose files, from rulesync's `claudecode` target. Uninstall removes what the ownership record lists.
- Mods are the only content delivered as plugins. rulesync's `claudecode-plugin` target renders the folders, dotai writes `.claude-plugin/plugin.json`, and the plugin is placed under the project's `.claude/skills/<mod>/`, never at user scope ([0011](0011-mods-always-project-scoped.md)).
- The rest of [0013](0013-renderer-merges-by-key.md) stays in force: merge by key, the ownership record, and `permissions`, `env` and `statusLine` in settings.

## Considered options

- **A (chosen):** loose files, plugins only for mods.
- **B:** one plugin per pack, as in 0013. Rejected: every name gets a prefix, the pack's hook matchers and permission rules must be rewritten, rules and permissions stay loose anyway, and the clean uninstall is already covered by the ownership record.
- **C:** no plugins at all. Rejected: mods cannot be delivered any other way.

## Consequences

- Partially supersedes [0013](0013-renderer-merges-by-key.md): its third bullet and the namespace cost.
- Good: skills, agents and MCP tools keep their plain names, and the same-name rules of [0023](0023-flat-profiles-and-layer-merge-rules.md) apply to them as written.
- Accepted cost: the namespace and matcher rewrite remain for mods, and a mod does not load until the user trusts the workspace.
- Pending: where mods live in the pack layout ([PACK-FORMAT](../PACK-FORMAT.md) does not define it yet), and whether `SubagentStart`/`SubagentStop` matchers need the `<plugin>:<agent>` form (unverified).

## Evidence

- Internal research (not published): rulesync plugin output and Claude Code plugin loading, 2026-10-26.
- Claude Code docs, [Plugins overview](https://code.claude.com/docs/en/plugins): "A plugin that has one [a hooks module] is called a mod".
