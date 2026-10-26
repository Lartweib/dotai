# Open questions

Questions about the engine that are not decisions yet. When one is resolved, it is recorded as an ADR in [decisions/](decisions/README.md) and removed from here. Questions about the reference pack's content live in the pack repository.

Renumbered on 2026-10-26, when the pack questions moved out. Resolved facts are in [HARNESS-NOTES.md](HARNESS-NOTES.md).

## Requires measurement

| # | What to measure | What it changes | Source |
|---|---|---|---|
| 1 | Evaluate Pi as a harness adapter, next to or instead of OpenCode | Harness targets ([0019](decisions/0019-v1-harness-targets.md)) | Proposal, 2026-10-23 |
| 2 | Whether `# Compact instructions` in `AGENTS.md` influences the summary after `/compact` | Rules file per harness | Internal research (not published): harness surfaces; [0012](decisions/0012-rules-file-per-harness.md) |
| 3 | Whether the caller of agy's `invoke_subagent` can set the subagent's model (its spec has `model` and `model_tier` fields) | Model declarations for agy subagents ([0024](decisions/0024-models-declared-per-harness-no-tiers.md)) | Internal research (not published): harness surfaces |
