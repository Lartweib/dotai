---
status: accepted
date: 2026-10-24
tags: [harnesses, adapters, scope, v1]
---

# 0019 · v1 harness targets: Claude Code, agy and OpenCode

## Context

A verification of the most used coding-agent harnesses (2026-10-24) showed that GitHub Copilot, Codex CLI and Cursor have a large user base and file-based configuration that dotai could render. Others (Gemini CLI, Pi, Cline, Kilo, Junie, Kiro, Windsurf, Goose, Qwen Code) follow. Each harness is an adapter with its own formats, owned paths and isolation level ([0018](0018-install-modes-and-mandatory-backup.md)). Supporting a harness well requires using and testing it for real.

## Decision

v1 targets Claude Code, Google Antigravity CLI (agy) and OpenCode, the harnesses that can be used and tested daily during development.

The adapter interface is designed from the start so that adding a harness means adding an adapter, without changes to the engine or to packs. GitHub Copilot, Codex CLI and Cursor are the first candidates after v1.

## Considered options

- **A (chosen):** three harnesses in v1, adapter interface ready for more.
- **B:** six harnesses in v1 (adding Copilot, Codex and Cursor). Rejected: three more adapters that would not be tested daily, with partial or unverified isolation.

## Consequences

- The adapter interface must cover what the six candidates need, even if only three are implemented: owned and shared paths, isolation mechanism and level, config formats (JSON, TOML, Markdown with frontmatter), non-file state.
- Kilo is a fork of OpenCode, so it may later reuse most of the OpenCode adapter.
- A REPLACE in agy must never touch Gemini CLI files under the shared `~/.gemini` directory, even though Gemini CLI is not a target.

## Evidence

- Internal research (not published): harness install modes verification, 2026-10-24.

Update 2026-11-18: [0047](0047-harness-selection-and-preview-harnesses.md) gives each harness a status, `supported` or `preview`; OpenCode is a preview harness until the pinned rulesync fixes its hooks plugin and its notes are verified on a real install.

Update 2026-11-22: OpenCode is `supported` since rulesync 43.12.0 and a live session confirmed its `sessionStart` output reaches the model ([0047](0047-harness-selection-and-preview-harnesses.md)).
