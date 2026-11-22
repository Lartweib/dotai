---
status: accepted
date: 2026-10-24
tags: [engine, packs, variants, orca]
---

# 0017 · Packs declare variants; the engine applies them as layers

## Context

An earlier plan defined two execution modes (without Orca / with Orca) as part of dotai. After [0010](0010-engine-and-pack-separated.md) separated the engine from the packs, that placement is wrong: the engine would know about one specific tool (Orca), and every pack author would inherit one pack's choice of environments. Other packs may need different splits (another orchestrator, a CI-only setup, no split at all).

## Decision

The engine supports **variants** as a generic capability and knows nothing about Orca.

- A pack may declare zero or more named variants. Each variant is a layer applied on top of the pack's base, using the same merge-by-key rules as the renderer ([0013](0013-renderer-merges-by-key.md)).
- The variant is chosen explicitly at install time (for example `dotai install --variant <name>`). A pack may also declare a detection rule (for example "the `orca` command exists") to suggest or preselect a variant; the user can always override it.
- Variants are orthogonal to profiles: a profile depends on the project type, a variant depends on the environment. Both are layers, so combinations are not written by hand.
- Anything that must hold in every variant (for example a cost policy) belongs in the pack's base, so no variant can drop it.

For example, a pack might declare a `standalone` variant and an `orca` variant for machines where Orca is installed.

## Considered options

- **A (chosen):** variants as a generic engine capability, defined by each pack.
- **B:** keep the two Orca modes in the engine. Rejected: it couples the engine to one tool and to one setup.
- **C:** one pack per environment. Rejected: it duplicates the shared base and drifts over time.

## Consequences

- The source schema must define how a pack declares variants, their layers and their optional detection rule (pending, together with the rest of the schema).
- The engine records which variant was installed, so that `dotai doctor` and updates reapply the same one.

Update 2026-11-22: the variant is sticky, as the mode is ([0038](0038-replace-mode-resets-harness-to-fresh-install.md)). Observed on the maintainer's machine: the state recorded `orca` in the replace mode, and a plain `dotai install <pack>` installed `standalone`, the default, and rewrote the state and the backup manifest with it, silently. Without `--variant`, install now uses the variant the install state records for the same pack (by name) when the pack still declares it, and otherwise the order above: the default. A recorded variant the pack dropped gives the warning `recorded-variant-missing` and the default. The web UI's install step gets this through the plan, whose `variant` it preselects. `--variant` always wins. `apply` already worked this way.
