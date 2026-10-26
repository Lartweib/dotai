---
status: accepted
date: 2026-10-23
tags: [engine, packs, architecture, open-source]
---

# 0010 · Engine and pack separated from day one

## Context

dotai is an open-source product ([0002](0002-personal-project-publishable.md)). The structure has to serve even someone who does not want the reference pack's opinions. At the same time, building features "for others" before the tool serves one real setup is the classic trap of generalizing too early.

## Decision

Engine and packs are separated, as in LazyVim:

- **Engine:** the Go binary ([0009](0009-own-go-engine-rulesync-schema.md); since [0021](0021-go-engine-orchestrates-rulesync.md) it orchestrates a pinned rulesync). It contains nothing personal.
- **Pack:** content in YAML + Markdown. The reference pack holds a complete, real configuration.

Others can point the same binary at their own pack (for example `dotai apply --pack <source>`) or build one from scratch.

Rule: the boundary is built, not the features for others (pack registry, pack inheritance, etc.), until someone needs them.

## Considered options

- **A (chosen):** engine and pack separated from the first commit, without generic features up front.
- **B:** others fork and compile their own Go binary. Rejected: it is heavy, and content should be data, not code.
- **C:** generic features from the start (registry, pack inheritance). Rejected: it is generalizing before it works for one real setup.

## Consequences

- Good: anything personal hardcoded in Go is a defect; the boundary can be checked in code review.
- Good: the reference pack exercises the pack format in real use from day one.
- Accepted cost: a pack manifest has to be designed even though there is only one pack for now.
- Pending: final name for "pack" and manifest format (internal research on neighboring engines, not published, §6).

## Evidence

- Internal research (not published) on neighboring engines, §5 (caution 1).
