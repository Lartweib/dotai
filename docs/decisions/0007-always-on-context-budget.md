---
status: proposed
date: 2026-10-20
tags: [cost, context, budget, doctor]
---

# 0007 · Always-on context budget

## Context

A heavily configured setup can carry tens of thousands of always-on tokens per request, and that prefix is repeated in every subagent.

## Decision

Every always-on piece has to justify its cost. A pack declares a cap of always-on tokens for its base in each harness, and the fixed cost of each profile. `dotai doctor` measures the always-on tokens and fails if a cap is exceeded. The values belong to the pack, not to the engine.

## Considered options

The sources do not record rejected alternatives.

## Consequences

- The budget is verifiable with `dotai doctor`.
- Subagents pay the same prefix, so the savings multiply.
- Pending: how a pack declares the caps; [PACK-FORMAT.md](../PACK-FORMAT.md) does not define it yet.

## Evidence

- Internal research (not published): discovery of harness capabilities.
- Internal research (not published): component catalog, §1.
