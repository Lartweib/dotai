---
status: accepted
date: 2026-11-01
tags: [pack, profiles, skills, agents, scopes, cost]
---

# 0039 · The base layer may install skills and agents at user level

## Context

[0008](0008-onboarding-by-detection-and-base-profile.md) decides that skills and agents are installed per profile, never globally, because an item installed globally is paid for on every request, even in projects that never use it. [ARCHITECTURE](../ARCHITECTURE.md) lists that rule as an invariant.

The engine no longer follows it:

- The base and the variant are rendered at user level ([0023](0023-flat-profiles-and-layer-merge-rules.md), [PACK-FORMAT](../PACK-FORMAT.md#scopes)), skills and agents included.
- The replace mode ([0038](0038-replace-mode-resets-harness-to-fresh-install.md)) and shared skills ([0036](0036-shared-skills-per-variant.md)) work on the skills directories of each harness at user level.

The cost 0008 names is the per-request cost of items a project does not use. That is what the always-on context budget of [0007](0007-always-on-context-budget.md) bounds: the pack declares a cap for its base in each harness, and `dotai doctor` checks it.

[0001](0001-own-configuration-engine.md) lists "not redefining the setup for each new project" among the needs dotai is built for.

## Decision

- The pack's base layer, and its variants, may install skills and agents at user level, in every harness, as long as the result stays within the always-on budget that the pack declares for its base ([0007](0007-always-on-context-budget.md)).
- Profiles add the skills and agents specific to a kind of project, at project level, as before.
- The engine does not limit what the base holds; the budget is the limit, and it belongs to the pack.

## Considered options

- **A (chosen):** the base and variants install at user level within the always-on budget; profiles add the project-specific items.
- **B:** keep "never globally" and move every skill and agent to profiles. Rejected: the workflow skills and agents that a pack's frame needs in every project would be redefined per project, which [0001](0001-own-configuration-engine.md) sets out to avoid.

## Consequences

- Partially supersedes [0008](0008-onboarding-by-detection-and-base-profile.md): the rule "skills and agents are installed per profile, never globally" and the rejection of option B. Detection by content, minimal questions and approval before applying stay.
- Good: the docs match what the engine installs, and a pack's common workflow is written once.
- Accepted cost: every skill and agent in the base is paid for on every request in every project. A pack keeps there only what every project uses.
- Pending: the budget is still `proposed` in [0007](0007-always-on-context-budget.md), and neither how a pack declares the cap nor the `dotai doctor` check exists yet, so until then the limit is the pack author's judgment.

## Evidence

- Maintainer decision, 2026-11-01.
- [PACK-FORMAT](../PACK-FORMAT.md#scopes): the base and the chosen variant install at user level.
- [0001](0001-own-configuration-engine.md), [0007](0007-always-on-context-budget.md), [0023](0023-flat-profiles-and-layer-merge-rules.md), [0036](0036-shared-skills-per-variant.md), [0038](0038-replace-mode-resets-harness-to-fresh-install.md).
