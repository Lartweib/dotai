---
status: accepted
date: 2026-10-19
tags: [standards, agents-md, skills, mcp]
---

# 0003 · Align with official standards; do not reinvent

## Context

Before creating an own format or mechanism, it is better to use the standard or official feature that already exists. Own pieces are built only where nothing exists. There are neighboring projects that solve parts of the same problem: `rulesync`, an npm package that keeps dotfiles for AI agents, and an existing skill installer that installs skills and profiles across clients.

## Decision

Adopted as-is:

- `AGENTS.md` as the single source of rules. Claude Code loads it with the bundled plugin `cc-plugin-agents-md`. No `CLAUDE.md` is generated.
- `SKILL.md` according to the open standard (agentskills.io), with per-harness overlays for each harness's own fields.
- MCP.
- Plugin + marketplace as the distribution unit in Claude Code.
- The `rulesync` folder structure as the reference for the source schema.

## Considered options

- **A (chosen):** use the existing standard or official feature; build only where nothing exists.
- **B:** create own formats or mechanisms by default. Rejected: it is what the principle of this decision avoids (not reinventing).

## Consequences

- What the project contributes: profiles per project type, a project analyzer, a web review module, delegation between harnesses, guards and a cost policy.
- Pending: before designing the schema, study `rulesync` and the other two neighboring projects (what they solve, what they do not, whether it makes sense to build on top of them).

## Evidence

- Internal research (not published): state of the art.

## Update 2026-10-23

Partially superseded by [0012](0012-rules-file-per-harness.md) (rules file per harness). The pending study of neighboring projects was closed in [0009](0009-own-go-engine-rulesync-schema.md), since superseded by [0021](0021-go-engine-orchestrates-rulesync.md).
