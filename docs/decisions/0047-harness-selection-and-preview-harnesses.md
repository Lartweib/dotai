---
status: accepted
date: 2026-11-18
tags: [harnesses, pack-format, manifest, install, ui, cli]
---

# 0047 · Packs declare compatible harnesses; the user picks; preview harnesses opt in

## Context

`harnesses` must be non-empty and means required; `optional-harnesses` are skipped when missing ([0038](0038-replace-mode-resets-harness-to-fresh-install.md)). So every pack requires a harness; `pack new` writes its choices as required. The UI install has no harness choice. In OpenCode, hooks that read stdin hang and rulesync's plugin enforces no hook timeouts, yet the code presents it as supported. No release exists yet.

## Decision

Names are proposed.

- **Status in the adapter data:** `supported` or `preview`, with a reason; OpenCode is `preview`. A preview harness is never selected by default, only when `--harness` names it or the UI ticks it; the plan then warns `harness-preview` with the reason. Promotion: the pinned rulesync has the fix, HARNESS-NOTES is verified on a real install (SessionStart output delivery included, [0045](0045-dotai-owned-project-onboarding.md)), and the field flips.
- **Manifest:** `harnesses` lists the compatible ones, non-empty; optional `requires` is a subset, possibly empty; `optional-harnesses` goes. An unknown name is `harness-unknown`, a repeat `harness-duplicate`, a `requires` name not in `harnesses` `harness-not-compatible`.
- **Selection.** Install plans, in the CLI and a new UI step, list every harness: compatible, detected, required, preview. Default: the saved choice, else the compatible, detected, supported ones. A missing required harness stops, naming it; nothing compatible chosen is an error. Apply follows the install's choice.
- `pack new` and the wizard ask compatible (supported by default) and required (none) harnesses.
- **Docs:** the README table shows the status (it already does); CONTRIBUTING says new harnesses start as preview.

## Considered options

- **(a) Compatible plus `requires` (chosen):** the engine already uses the union; one field changes meaning, and no release breaks.
- **(b) Allow an empty `harnesses` beside `optional-harnesses`:** rejected; the primary field stays "required".
- **(c) As is:** rejected; every pack must require a harness.

## Consequences

- Amends [0019](0019-v1-harness-targets.md), [0022](0022-pack-format-and-manifest.md) with PACK-FORMAT and CLI-JSON, 0038, [0043](0043-local-web-ui.md) and [0046](0046-pack-templates-and-wizard.md).
- Migration: `examples/minimal-pack` and the template keep their lists, now compatible; comments and READMEs change. Those files are unhashed, so a new template version is appended only if a hashed file changes.
- Pending: whether `pack.Harnesses`, a second list of ids, derives from the adapters; the plan JSON of non-compatible harnesses; codes for the install refusals.

## Evidence

- `internal/pack/manifest.go:23,35-38,56,133-157`; `internal/pack/yaml.go:151-152`.
- `internal/app/detect.go:147-193`; `project.go:224-232`; `install.go:122-123,433`; `stage.go:134`; `internal/pack/pack.go:179`.
- `internal/cli/install.go:62`; `internal/packtemplate/files/dotai.yaml:7-8`; `packtemplate.go:68-74,118`; `hashes.txt:6-20`.
- `internal/server/web/js/plan.js:34,66`; `packnew.js:19`; `internal/server/server.go:296-303`.
- `README.md:26-32`; `git tag` lists none.

Update 2026-11-18: implemented as [PACK-FORMAT](../PACK-FORMAT.md#manifest) and [CLI-JSON](../CLI-JSON.md) describe, with the names above. Where this record left a choice open:

- The status is the adapter's `Status` and `StatusReason` (`internal/harness`); `/api/harnesses`, the install plan's `harnesses` and its `harness-preview` warning carry them. An apply plan carries the warning too, for a preview harness the install's choice names. Any status but `supported` counts as a preview, and a test fails for an adapter whose status is neither.
- `optional-harnesses` is `field-unknown`, with a hint to list the compatible harnesses in `harnesses` and the needed ones in `requires`. A repeat in either list is `harness-duplicate`.
- A preview harness that a pack requires is still never selected by default: without a choice that names it, the install is refused with `harness-preview-required`, with `harness-missing` too when it is not installed either. The web UI shows it unticked, with its reason; only the user's tick opts it in, and planning again with no tick changed sends no choice.
- A saved choice reuses a preview harness it names, as an explicit choice. A preview harness that the previous install of the same pack managed without a saved choice, as every install from before this decision did, stays managed, with `harness-preview` and a notice, so that an upgrade never drops OpenCode, or removes its files, unasked.
- An apply takes no `--harness`: it follows the install's choice, and its notices and refusals say to choose with `dotai install --harness`, then apply again; the web UI's apply says to choose on the Install plan board.
- Pending items: `pack.Harnesses` does not derive from the adapters, since `internal/harness` imports `internal/pack`; a test fails when the two lists differ (closed). The plan lists every harness dotai knows, a harness the pack is not compatible with after the compatible ones, `compatible: false`, never managed, with its detection, where a configuration directory that cannot be checked counts as not found (closed). The install refusals are the envelope code `harness-refused`, whose `refusals` carry `harness-unknown`, `harness-not-compatible`, `harness-required`, `harness-missing`, `harness-preview-required`, `harness-not-installed` and `harness-none-selected` (closed). An apply plan lists only the harnesses it plans, as before, not every harness with its state (open).
