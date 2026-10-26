---
status: superseded
date: 2026-10-20
tags: [product, layers, roadmap]
superseded-by: 0020
---

# 0006 · Five-layer product and v1 build order

> **Superseded by [0020](0020-v1-scope-engine-and-pack.md)** (2026-10-24). Kept as history; only 0020 is binding.

## Context

The profiles and the build order in [0004](0004-installer-language-and-shape.md) did not match the target project types. A product plan was needed that said what to build first.

## Decision

dotai is built in five layers, in this order: (1) global base, (2) cross-harness delegation, (3) global panel, (4) onboarding with profiles, (5) parity when agy is the hub. The content of each layer (base rules, profile list, delegation details) is reference pack content.

## Considered options

- **A (chosen):** five layers, built in order 1 → 5.
- **B:** the build order and profiles from [0004](0004-installer-language-and-shape.md). Rejected: the profiles did not match the target project types.

## Consequences

- Partially supersedes 0004 (build order and profiles). The Go language and the `apply`/`doctor` shape are kept.
- Most of the five layers are pack content, not engine features; [0020](0020-v1-scope-engine-and-pack.md) replaced this plan for that reason.

## Evidence

- Internal research (not published): component catalog and per-profile studies.
