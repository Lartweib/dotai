# Decision log

One file per decision, in a format based on MADR 4.0.0 ([template.md](template.md)). Only `accepted` decisions are binding; `proposed` is non-binding context; `superseded` is history only. An accepted record is not rewritten: a later record replaces it, in whole or in part, and dated Update notes at its end record what changed since.

In the docs and in code comments, "decision NNNN" or a bare number such as "0035" refers to the record `NNNN-*.md` in this directory. How to propose a new record is in [CONTRIBUTING.md](../../CONTRIBUTING.md#decision-records).

## Start here

Read these first for the shape of the engine:

- [0001](0001-own-configuration-engine.md): why an own configuration engine.
- [0010](0010-engine-and-pack-separated.md): the engine and packs are separate.
- [0019](0019-v1-harness-targets.md): the target harnesses.
- [0021](0021-go-engine-orchestrates-rulesync.md): the Go engine orchestrates a pinned rulesync.
- [0028](0028-plan-centric-hexagonal-engine.md): every command builds a plan first.
- [0018](0018-install-modes-and-mandatory-backup.md): install modes and the mandatory backup.
- [0041](0041-project-scope-local-and-untracked.md): the project scope stays local and untracked.
- [0044](0044-git-pack-distribution.md): packs are distributed as git repositories.
- [0045](0045-dotai-owned-project-onboarding.md): dotai owns project onboarding.
- [0046](0046-pack-templates-and-wizard.md): new packs start from an embedded template.

## In force, by topic

### Principles and project

| No. | Title | Status | Date |
|---|---|---|---|
| [0001](0001-own-configuration-engine.md) | Build an own configuration engine for AI coding harnesses | accepted | 2026-10-19 |
| [0002](0002-personal-project-publishable.md) | Personal project, done well, publishable | accepted | 2026-10-19 |
| [0003](0003-align-with-official-standards.md) | Align with official standards; do not reinvent (partially superseded by 0012) | accepted | 2026-10-19 |
| [0005](0005-working-name-dotai.md) | Working name: `dotai` | accepted | 2026-10-19 |
| [0014](0014-public-docs-in-english.md) | Public docs in English | accepted | 2026-10-23 |
| [0016](0016-review-via-external-tracker.md) | Review lives in external tools that the pack declares | accepted | 2026-10-24 |

### Engine architecture

| No. | Title | Status | Date |
|---|---|---|---|
| [0004](0004-installer-language-and-shape.md) | Installer language and shape (partially superseded by 0006) | accepted | 2026-10-19 |
| [0010](0010-engine-and-pack-separated.md) | Engine and pack separated from day one | accepted | 2026-10-23 |
| [0021](0021-go-engine-orchestrates-rulesync.md) | The Go engine orchestrates; a pinned rulesync renders | accepted | 2026-10-25 |
| [0028](0028-plan-centric-hexagonal-engine.md) | A plan-centric engine with a pure domain and ports (partially superseded by 0029) | accepted | 2026-10-26 |
| [0029](0029-rulesync-checksums-embedded-in-the-engine.md) | rulesync checksums are embedded in the engine | accepted | 2026-10-28 |

### Scope, harnesses and interfaces

| No. | Title | Status | Date |
|---|---|---|---|
| [0019](0019-v1-harness-targets.md) | v1 harness targets: Claude Code, agy and OpenCode | accepted | 2026-10-24 |
| [0020](0020-v1-scope-engine-and-pack.md) | v1 scope of the engine (partially superseded by 0034) | accepted | 2026-10-24 |
| [0026](0026-json-cli-and-ui-after-v1.md) | Every command has `--json`; a UI comes after v1 (partially superseded by 0043) | accepted | 2026-10-26 |
| [0034](0034-replace-mode-in-v1.md) | The replace install mode is part of v1 (partially superseded by 0038) | accepted | 2026-10-30 |
| [0043](0043-local-web-ui.md) | A local web UI in v1, served by `dotai ui` over the same use cases (partially superseded by 0044) | accepted | 2026-11-04 |
| [0047](0047-harness-selection-and-preview-harnesses.md) | Packs declare compatible harnesses; the user picks; preview harnesses opt in | accepted | 2026-11-18 |

### Pack format and authoring

| No. | Title | Status | Date |
|---|---|---|---|
| [0015](0015-pack-in-its-own-repository.md) | The reference pack lives in its own repository | accepted | 2026-10-23 |
| [0017](0017-pack-variants-as-layers.md) | Packs declare variants; the engine applies them as layers | accepted | 2026-10-24 |
| [0022](0022-pack-format-and-manifest.md) | Pack format: uniform layers and a `dotai.yaml` manifest (partially superseded by 0023, 0024 and 0030) | accepted | 2026-10-25 |
| [0023](0023-flat-profiles-and-layer-merge-rules.md) | Flat, combinable profiles and explicit layer merge rules | accepted | 2026-10-25 |
| [0024](0024-models-declared-per-harness-no-tiers.md) | Models declared per harness, without tiers | accepted | 2026-10-25 |
| [0025](0025-settings-as-per-harness-passthrough.md) | Settings as per-harness blocks merged as-is | accepted | 2026-10-25 |
| [0027](0027-loose-files-and-plugins-only-for-mods.md) | Claude Code content as loose files; plugins only for mods | accepted | 2026-10-26 |
| [0030](0030-tools-paths-and-project-config.md) | Tool paths, one tools directory, settings overrides and project config (partially superseded by 0041) | accepted | 2026-10-28 |
| [0036](0036-shared-skills-per-variant.md) | A variant can share the user's skills folder in isolated mode | accepted | 2026-10-31 |
| [0037](0037-tool-commands-resolved-per-os.md) | Tool commands in settings are resolved per OS | accepted | 2026-10-31 |
| [0039](0039-base-layer-installs-skills-at-user-level.md) | The base layer may install skills and agents at user level | accepted | 2026-11-01 |
| [0040](0040-list-files-merged-as-a-union.md) | Hooks and permissions merge as a union in layer order | accepted | 2026-11-01 |
| [0044](0044-git-pack-distribution.md) | Packs are distributed as git repositories, fetched with the user's git | accepted | 2026-11-16 |
| [0046](0046-pack-templates-and-wizard.md) | Packs start from an embedded template and a wizard | accepted | 2026-11-17 |

### Install, backup and restore

| No. | Title | Status | Date |
|---|---|---|---|
| [0013](0013-renderer-merges-by-key.md) | The renderer merges by key and never rewrites files with foreign content (partially superseded by 0027) | accepted | 2026-10-23 |
| [0018](0018-install-modes-and-mandatory-backup.md) | Install modes (isolated, replace, merge) with a mandatory backup (partially superseded by 0038) | accepted | 2026-10-25 |
| [0031](0031-isolated-install-root.md) | Isolated installs live under `~/.dotai/isolated/` (partially superseded by 0034) | accepted | 2026-10-29 |
| [0032](0032-install-state-backup-and-isolated-launch.md) | Install state, backup layout and the isolated launch command (partially superseded by 0035) | accepted | 2026-10-29 |
| [0033](0033-restore-from-the-install-history.md) | Restore follows the install history kept in the backups (partially superseded by 0035) | accepted | 2026-10-30 |
| [0035](0035-settings-files-owned-by-key.md) | dotai owns only its keys in a harness's settings file | accepted | 2026-10-30 |
| [0038](0038-replace-mode-resets-harness-to-fresh-install.md) | Replace mode resets each selected harness to a fresh install (partially superseded by 0049) | accepted | 2026-11-01 |
| [0049](0049-claude-user-mcp-servers-in-claude-json.md) | Claude Code's user-scope MCP servers are owned server by server in its `.claude.json` | accepted | 2026-11-20 |
| [0050](0050-hook-entries-owned-one-by-one.md) | dotai owns its own hook entries, not the hook files other programs also write | accepted | 2026-11-21 |

### Project scope and onboarding

| No. | Title | Status | Date |
|---|---|---|---|
| [0007](0007-always-on-context-budget.md) | Always-on context budget | proposed | 2026-10-20 |
| [0008](0008-onboarding-by-detection-and-base-profile.md) | Onboarding by detection and base profile (partially superseded by 0023, 0039 and 0045) | accepted | 2026-10-20 |
| [0011](0011-mods-always-project-scoped.md) | Mods always project-scoped | accepted | 2026-10-23 |
| [0012](0012-rules-file-per-harness.md) | Rules file per harness: `CLAUDE.md` for Claude Code, `AGENTS.md` for agy and OpenCode (partially superseded by 0041) | accepted | 2026-10-23 |
| [0041](0041-project-scope-local-and-untracked.md) | The project scope stays local: excluded from git, never in tracked files (partially superseded by 0045) | accepted | 2026-11-02 |
| [0045](0045-dotai-owned-project-onboarding.md) | Project onboarding is dotai's: a generated profiling skill and a session-start hook | accepted | 2026-11-17 |

### Release

| No. | Title | Status | Date |
|---|---|---|---|
| [0042](0042-tag-driven-release-binaries.md) | Tag-driven release binaries built by a script and published with gh | accepted | 2026-11-03 |
| [0048](0048-one-command-install-scripts.md) | One-command install scripts that verify the release checksum | proposed | 2026-11-19 |

## Superseded (history only)

| No. | Title | Status | Superseded by |
|---|---|---|---|
| [0006](0006-five-layer-product-and-v1-order.md) | Five-layer product and v1 build order | superseded | [0020](0020-v1-scope-engine-and-pack.md) |
| [0009](0009-own-go-engine-rulesync-schema.md) | Own minimal Go engine, using the rulesync schema | superseded | [0021](0021-go-engine-orchestrates-rulesync.md) |

## See also

- [ARCHITECTURE.md](../ARCHITECTURE.md): the engine as built.
- [PACK-FORMAT.md](../PACK-FORMAT.md): the pack contract.
- [ROADMAP.md](../ROADMAP.md): what is done and what comes next.
