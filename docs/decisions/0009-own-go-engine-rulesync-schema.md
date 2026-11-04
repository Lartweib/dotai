---
status: superseded
date: 2026-10-23
tags: [engine, go, rulesync, source-schema]
superseded-by: 0021
---

# 0009 · Own minimal Go engine, using the rulesync schema

> **Superseded by [0021](0021-go-engine-orchestrates-rulesync.md)** (2026-10-25). Kept as history; only 0021 is binding.

## Context

[0003](0003-align-with-official-standards.md) left pending the study of `rulesync`, `tackroom` and `cinch` before designing the source schema, and deciding whether it made sense to build on top of one of them. Internal research on neighboring engines (not published) did so: rulesync is the only neighbor that covers dotai's three harnesses with all the relevant surfaces; tackroom has no Windows or Antigravity support; cinch only moves skills and agents. None covers what sets dotai apart (cost policy, quota-aware delegation, profiles, onboarding, `review-web`, statusline).

## Decision

An own minimal Go engine is built, with three renderers (Claude Code, agy and OpenCode). The source schema is modeled on `.rulesync/`: the same folder names and per-harness blocks in the frontmatter (`claudecode:`, `antigravity-cli:`, `opencode:`). dotai's own fields (`model_tier`, abstract capabilities) go in a `dotai:` block.

This is option 2 of that research.

## Considered options

- **A (chosen):** own Go engine with the rulesync schema as reference.
- **B:** rulesync as the engine and dotai as a distribution on top. Rejected: what sets dotai apart lies outside any engine; rulesync went from v1 to v40 in ~13 months with a single owner; and it is TypeScript/Node, which clashes with the single Go binary from [0004](0004-installer-language-and-shape.md).
- **C:** own engine without a reference schema. Rejected: it contradicts [0003](0003-align-with-official-standards.md) (don't reinvent).

## Consequences

- Good: packs stay nearly compatible with rulesync; anyone who wants more harnesses can run rulesync over the same tree without dotai depending on it.
- Bad or accepted cost: maintaining three renderers for fast-changing harnesses.
- Closes the "study neighbors" pending item from [0003](0003-align-with-official-standards.md).
- Pending: verify that rulesync tolerates an unknown `dotai:` block in a source file without an error or a warning; find out whether rulesync supports custom targets.

## Evidence

- Internal research (not published) on neighboring engines, §4 and §5.
