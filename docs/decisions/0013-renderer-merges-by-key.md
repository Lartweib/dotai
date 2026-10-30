---
status: accepted
date: 2026-10-23
tags: [renderer, settings, plugins, doctor]
---

# 0013 · The renderer merges by key and never rewrites files with foreign content

> **Partially superseded by [0027](0027-loose-files-and-plugins-only-for-mods.md): content goes to Claude Code as loose files, and plugins carry only mods.** Still in force: merge by key, the ownership record, and `permissions`, `env` and `statusLine` in settings.

## Context

A user's `~/.claude/settings.json` often already holds hooks from other tools (for example Orca's agent hooks), as well as permission lists, `enabledPlugins` and `extraKnownMarketplaces` (internal research on harness surfaces, not published, §1). Rewriting that file would break those tools.

In addition, a Claude Code plugin cannot carry `permissions`, `env` or `statusLine` (same research, §0.6 and §5.1).

## Decision

- The renderer merges by key. It records which keys belong to it, so `dotai doctor` can diff and remove only its own, and it never deletes foreign entries.
- `permissions`, `env` and `statusLine` go into settings through that merge.
- Skills, commands, agents, hooks, MCP and mods go into one plugin per pack.

## Considered options

- **A (chosen):** merge by key with an ownership record, and one plugin per pack for whatever a plugin can carry.
- **B:** rewrite generated files in full. Rejected: it breaks other tools that write to the same file, such as Orca.
- **C:** write everything as loose files. Rejected: it does not allow a clean uninstall (inferred).

## Consequences

- Good: dotai coexists with Orca and other tools on the same `settings.json`.
- Good: uninstalling a pack means removing its plugin and its recorded keys.
- Accepted cost: inside the plugin, skills get the `/<plugin>:<skill>` namespace and agents become `<plugin>:<agent>`, which affects hook matchers (same research, §5.1).
- Pending: where and how the record of owned keys is stored (inferred: not defined in the sources).

## Evidence

- Internal research (not published) on harness surfaces, §0.6, §1 (local state), §5.1 and §6.4 (recommendations 2 and 3).

Update 2026-10-30: for the settings files of the isolated installs, the record of owned keys lives in `state.json`, as [0035](0035-settings-files-owned-by-key.md) decides.
