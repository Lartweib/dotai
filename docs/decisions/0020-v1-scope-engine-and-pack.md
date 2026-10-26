---
status: accepted
date: 2026-10-24
tags: [product, scope, roadmap, v1]
supersedes: 0006
---

# 0020 · v1 scope of the engine

## Context

[0006](0006-five-layer-product-and-v1-order.md) defined five product layers (global base, delegation, global panel, onboarding with profiles, agy-hub parity) and built them in that order. Since then the engine and the packs were separated ([0010](0010-engine-and-pack-separated.md)), review left the engine ([0016](0016-review-via-external-tracker.md)), and variants ([0017](0017-pack-variants-as-layers.md)) and install modes ([0018](0018-install-modes-and-mandatory-backup.md)) appeared. Most of the five layers are pack content, not engine features.

## Decision

v1 is the minimum path to use dotai for real with a pack, without risk to the user's existing configuration.

**Engine v1:**

1. Load a pack (manifest, base, profiles, variants) and apply it as layers.
2. Adapters for Claude Code, agy and OpenCode ([0019](0019-v1-harness-targets.md)), with merge by key ([0013](0013-renderer-merges-by-key.md)).
3. Mandatory backup with `dotai restore`, and the isolated install mode.
4. `dotai doctor`: what is installed, with which variant, and whether anything changed outside dotai.

**After v1:** the replace mode, and engine support for stack detection during onboarding.

The reference pack's own v1 scope is recorded in the pack repository.

## Considered options

- **A (chosen):** v1 split into engine and pack, isolated mode first.
- **B:** keep the five-layer order of 0006. Rejected: it mixes engine and pack, and it predates variants and install modes.

## Consequences

- Supersedes [0006](0006-five-layer-product-and-v1-order.md).
- The isolated mode lets a user test a pack next to their current configuration before adopting it.
