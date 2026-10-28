---
status: accepted
date: 2026-10-28
tags: [engine, pack-format, tools, settings]
---

# 0030 · Tool paths, one tools directory, settings overrides and project config

## Context

A pack's hooks, skills and scripts need to call the tools the pack installs, but `${DOTAI_TOOLS}` was only expanded in `dotai/settings.yaml`. Setting it as an environment variable through the harness is not portable: OpenCode drops a top-level `env` key silently, and agy is unverified ([HARNESS-NOTES](../HARNESS-NOTES.md)). [0022](0022-pack-format-and-manifest.md) lists `dotai/tools/` and `dotai/runners/`, but no record gives them different behavior. [0023](0023-flat-profiles-and-layer-merge-rules.md) requires a declared override to replace a settings value, and `settings.yaml` has no frontmatter to declare it in. Tools that run at user scope also need per-project configuration, and the project must record its profiles somewhere.

## Decision

- **Path variables.** While staging, the engine replaces `${DOTAI_TOOLS}` with the absolute path of the user-scope tools directory and `${DOTAI_PROJECT_TOOLS}` with the project's, in every text file of the staged layers, before rulesync runs. Only these exact tokens are replaced; files that are not UTF-8 text are copied as-is. Nothing depends on the harness passing an environment variable.
- **One tools directory.** `dotai/runners/` is removed; `dotai/tools/` holds every script, binary and config file a layer installs, executable or not. User-scope tools install to `~/.dotai/tools/`, project-scope tools to `.dotai/tools/`. The same path in two layers of a scope is always an error; variants do not clash with each other because only one is applied. A layer that adjusts another layer's tool config ships a file with a different name, and the tool combines them.
- **Settings overrides.** `dotai/settings.yaml` declares its own overrides in a top-level block, `dotai: { overrides: [claudecode.statusLine] }`, using `<harness>.<key>` paths. The 0023 rules apply as written.
- **Project config.** A project has a `.dotai/` directory. The engine records the applied profiles in a file it owns there. `.dotai/config.yaml` belongs to the user and the pack: the engine never writes it, and tools find it by walking up from the current directory to the nearest `.dotai/`.

## Considered options

- **A (chosen):** expand the path tokens at stage time in every text file.
- **B:** export `DOTAI_TOOLS` through each harness's environment settings. Rejected: OpenCode drops it, and agy support is unverified.
- **C:** keep `runners/` apart from `tools/`. Rejected: no record defines a difference.
- **D:** a file override mechanism for tool configs. Rejected: the engine would have to interpret configs it does not own; distinct names cover the need.
- **E:** one project file shared by the engine and the user. Rejected: it mixes owners, against [0013](0013-renderer-merges-by-key.md).

## Consequences

- Partially supersedes [0022](0022-pack-format-and-manifest.md) (`dotai/runners/`) and refines [0023](0023-flat-profiles-and-layer-merge-rules.md) (how settings declare overrides).
- Good: tool paths work the same on every harness and in skill text.
- Bad or accepted cost: installed files contain absolute paths, so moving the dotai directory requires reinstalling.
- Pending: the name of the engine's profile record in `.dotai/`, decided with project scope (roadmap PR 10).

## Evidence

- [HARNESS-NOTES](../HARNESS-NOTES.md): OpenCode drops a top-level `env` key.
- [0021](0021-go-engine-orchestrates-rulesync.md), [0022](0022-pack-format-and-manifest.md), [0028](0028-plan-centric-hexagonal-engine.md): `runners` always appears next to `tools`, with no separate semantics.
