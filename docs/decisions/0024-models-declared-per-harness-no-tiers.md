---
status: accepted
date: 2026-10-25
tags: [pack, models, cost-policy, subagents]
---

# 0024 · Models declared per harness, without tiers

## Context

[0022](0022-pack-format-and-manifest.md) planned a `model_tier` field in the `dotai:` block, translated by the engine into a model per harness through a central mapping. That adds a file, a concept and a translation step to the engine, and hides the actual model from whoever reads a subagent file.

Facts verified on 2026-10-24 and 2026-10-25:

- In Claude Code, the orchestrator chooses the model when it launches a subagent, from the aliases in the `Agent` tool. A `PreToolUse` hook can see that no model was sent and reject the launch.
- Effort cannot be set at launch time in Claude Code, only in the subagent definition.
- rulesync 41.0.0 passes the model from per-target subagent blocks: `claudecode: { model, effort }`, `opencode: { model }` and `antigravity-cli: { model }`. A block named `antigravity:` is dropped silently for subagents.
- Model aliases (`haiku`, `sonnet`, `opus` in Claude Code; `flash`, `pro` in agy) keep working when new models are released.

## Decision

There are no model tiers.

- **Runtime:** the orchestrator chooses the model for each delegation, guided by the pack's cost rule. The pack's hook rejects a delegation without an explicit model. For delegations to agy, the `delegate` skill lists agy's models.
- **Definitions:** each subagent declares its default model, and its effort where the harness supports it, directly in rulesync's per-target blocks. Packs should use aliases rather than exact model ids.
- **The engine does not translate models.** `dotai doctor` reports a subagent that has no model for a target harness.

## Considered options

- **A (chosen):** models declared per harness in each subagent, chosen at runtime by the orchestrator.
- **B:** abstract tiers mapped by the pack ([0022](0022-pack-format-and-manifest.md)). Rejected: with aliases, the central mapping would rarely change, and it hides the model from the subagent file.
- **C:** a tier-to-model mapping shipped by the engine. Rejected: every new model would need an engine release, and model choice is cost policy, which belongs to the pack ([0010](0010-engine-and-pack-separated.md)).
- **D:** resolve tiers at runtime with the hook. Rejected: the hook cannot set effort, and failures would appear in the middle of a task instead of at install time.

## Consequences

- Partially supersedes [0022](0022-pack-format-and-manifest.md): `model_tier` is removed from the `dotai:` block.
- A subagent available in three harnesses repeats its model in three blocks. If that becomes a real burden, tiers can be added later without breaking existing packs.
- Model names for agy subagents remain `inherit`, `flash` or `pro`.

## Evidence

- Internal research (not published): pack format verifications, 2026-10-24.
- rulesync 41.0.0 subagent generation test with per-target model blocks, 2026-10-25.
