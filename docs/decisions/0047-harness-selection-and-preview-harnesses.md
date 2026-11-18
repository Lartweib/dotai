---
status: proposed
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
