---
status: accepted
date: 2026-10-25
tags: [pack, settings, statusline, harnesses]
---

# 0025 · Settings as per-harness blocks merged as-is

## Context

rulesync has no settings feature ([0021](0021-go-engine-orchestrates-rulesync.md)), so settings such as the statusline, environment variables and the default model are handled by dotai through `dotai/settings.yaml` ([0022](0022-pack-format-and-manifest.md)). A verification on 2026-10-25 showed that each harness stores them differently:

| | Claude Code | agy | OpenCode |
|---|---|---|---|
| User file | `~/.claude/settings.json` | `~/.gemini/antigravity-cli/settings.json` | `~/.config/opencode/opencode.json(c)` |
| Project file | `.claude/settings.json` | none | `opencode.json(c)` or `.opencode/opencode.json(c)` |
| Default model | `model`, alias or id | `model`, stored as a display label | `model`, `provider/model` |
| Environment | `env` object | none | none (a top-level `env` is dropped silently) |
| Statusline | `statusLine` command | `statusLine` command, with extra fields and a different `padding` meaning | none; only through a plugin |
| Schema | strict, rejects unknown keys | keeps unknown keys on rewrite | `https://opencode.ai/config.json` |

## Decision

`dotai/settings.yaml` holds one block per harness, keyed by rulesync target name. The engine does not interpret any setting.

```yaml
claudecode:
  model: sonnet
  statusLine: { type: command, command: "${DOTAI_TOOLS}/statusline" }
antigravity-cli:
  statusLine: { type: command, command: "${DOTAI_TOOLS}/statusline" }
opencode:
  model: anthropic/claude-sonnet-4-5
```

- **The engine knows only where each harness keeps its settings**, per scope, and the file format. It merges the block into that file by key, with the rules in [0023](0023-flat-profiles-and-layer-merge-rules.md), and never touches foreign keys.
- **Variables.** The engine expands a small, fixed set of its own variables, starting with `${DOTAI_TOOLS}` (where the pack's tools were installed). Nothing else is rewritten.
- **Scopes.** A block for a harness and scope with no settings file is a validation error. In particular, profiles cannot carry agy settings, because agy has no project settings file.
- **Shared files.** In `~/.gemini`, dotai writes only `antigravity-cli/settings.json`; `~/.gemini/settings.json` belongs to Gemini CLI.
- **Validation.** `dotai doctor` validates the merged result against the harness's JSON Schema where one exists (Claude Code, OpenCode).

## Considered options

- **A (chosen):** per-harness blocks merged as-is.
- **B:** abstract settings (`statusline`, `env`, `model`) translated by the engine. Rejected: the harnesses differ in shape and meaning, every new setting would need an engine release, and the engine would carry per-harness knowledge that changes often.

## Consequences

- A harness setting that appears tomorrow can be used by a pack without a dotai release.
- One statusline script can serve Claude Code and agy, because both pass similar session JSON on stdin; agy adds quota data.
- The reference pack should not set agy's default model: it is stored as a display label that changes with each model release.
- A statusline for OpenCode needs a plugin, outside v1.

## Evidence

- Internal research (not published): harness settings verification, 2026-10-25.
