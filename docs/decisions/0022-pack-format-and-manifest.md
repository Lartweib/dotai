---
status: accepted
date: 2026-10-25
tags: [pack, format, manifest, profiles, variants, rulesync]
---

# 0022 · Pack format: uniform layers and a `dotai.yaml` manifest

> **Partially superseded by [0023](0023-flat-profiles-and-layer-merge-rules.md)** (one staging tree per scope) **and [0024](0024-models-declared-per-harness-no-tiers.md)** (no `model_tier`). The rest is in force.

## Context

Packs are separate from the engine ([0010](0010-engine-and-pack-separated.md)), declare variants ([0017](0017-pack-variants-as-layers.md)) and are rendered through a pinned rulesync ([0021](0021-go-engine-orchestrates-rulesync.md)). rulesync covers rules, commands, subagents, skills, MCP, hooks and permissions, but not settings such as `statusLine`, `env` or model defaults. rulesync strips a top-level `dotai:` frontmatter block from its output. Profiles are chosen by the agent during onboarding, after analyzing the project and asking the user ([0008](0008-onboarding-by-detection-and-base-profile.md)).

## Decision

A pack is a directory with a `dotai.yaml` manifest and uniform layers. The full format is in [PACK-FORMAT.md](../PACK-FORMAT.md).

- **Uniform layers.** The base, each profile and each variant have the same shape: a `.rulesync/` tree, rendered by rulesync, and a `dotai/` directory with what rulesync does not cover (settings, tools, runners).
- **Layer order.** Base, then profile, then variant. The engine merges the layers into one staging tree with its own merge-by-key rules ([0013](0013-renderer-merges-by-key.md)) and runs rulesync once over it. It does not rely on rulesync's `--input-roots` overlays.
- **The manifest** declares the pack's identity, the engine range, the exact rulesync version the pack was tested with, the target harnesses, the profiles and the variants.
- **Profiles are a catalog for the agent.** Each profile has a `description` and optional `hints` that the onboarding agent uses as evidence. The engine never selects a profile on its own; it applies the one passed explicitly (`--profile`).
- **Variants may declare a mechanical detection rule** (for example a command on `PATH`), because they depend on the environment, not on the project. Detection only preselects; the user can always override it.
- **The `dotai:` frontmatter block** carries only what rulesync cannot express: `model_tier` and abstract `capabilities`. The engine reads it from the sources before rendering. Per-harness tuning uses rulesync's own per-target blocks.

## Considered options

- **A (chosen):** uniform layers merged by the engine, profiles as an agent catalog.
- **B:** layers through rulesync's `--input-roots`. Rejected: same-name conflict behavior is unverified and may change across majors.
- **C:** mechanical profile detection by file patterns in the engine. Rejected: contradicts [0008](0008-onboarding-by-detection-and-base-profile.md); the agent reads content and asks, which file patterns cannot do.

## Consequences

- A pack works without dotai's onboarding: profiles can always be chosen by hand.
- Adding a harness does not change the pack format, only rulesync targets and dotai's settings mapping.
- Pending: the per-harness mapping of `dotai/settings.yaml`, and the `model_tier` values and their mapping per harness (agy only accepts `inherit`, `flash` or `pro`).

## Evidence

- Internal research (not published): pack format verifications, 2026-10-24.
