# Roadmap

What the engine delivers, in the order it was built, and what comes next. The scope is set by [0020](decisions/0020-v1-scope-engine-and-pack.md), widened by [0034](decisions/0034-replace-mode-in-v1.md) and [0043](decisions/0043-local-web-ui.md); the code structure by [0028](decisions/0028-plan-centric-hexagonal-engine.md). How contributions are made is in [CONTRIBUTING.md](../CONTRIBUTING.md).

Every demo below uses the example pack in the repository ([examples/minimal-pack](../examples/minimal-pack)) and a temporary home directory, never a real harness configuration.

Status: **done** is on `main`; **next** is the current focus; **later** is planned, in priority order, but not started.

## v1

| # | Deliverable | What can be seen or tried | Status |
|---|---|---|---|
| 1 | Skeleton: Go module, `cmd/dotai`, versioned `--json` output, CI on Windows, Linux and macOS | `dotai version` and `dotai version --json` | done |
| 2 | Pack loading: the manifest, layers and `dotai:` blocks, and the example pack | `dotai validate examples/minimal-pack`; a broken manifest reports file and line | done |
| 3 | Layer merge rules ([0023](decisions/0023-flat-profiles-and-layer-merge-rules.md), [0040](decisions/0040-list-files-merged-as-a-union.md)) and one staging tree per scope | `dotai validate` rejects duplicate names and undeclared overrides; `dotai stage --out <dir>` shows the merged tree | done |
| 4 | Pinned rulesync: download, embedded checksums, cache, run into staging ([0021](decisions/0021-go-engine-orchestrates-rulesync.md), [0029](decisions/0029-rulesync-checksums-embedded-in-the-engine.md)) | `dotai stage` shows the real harness output (`CLAUDE.md`, `AGENTS.md`, agents) | done |
| 5 | Per-harness settings, path variables such as `${DOTAI_TOOLS}`, and tools ([0030](decisions/0030-tools-paths-and-project-config.md), [0037](decisions/0037-tool-commands-resolved-per-os.md)) | Staging contains the Claude Code and agy `settings.json` | done |
| 6 | JSON and JSONC edit layer over hujson | Golden tests show before and after: comments, key order and CRLF kept | done |
| 7 | The plan and the harness adapters as data ([0028](decisions/0028-plan-centric-hexagonal-engine.md)) | `dotai install --dry-run` prints the exact plan, readable or as `--json` | done |
| 8 | Backup, install state, isolated install and `dotai run` ([0031](decisions/0031-isolated-install-root.md), [0032](decisions/0032-install-state-backup-and-isolated-launch.md), [0033](decisions/0033-restore-from-the-install-history.md)) for Claude Code, agy and OpenCode | `dotai install --mode isolated`, then `dotai run <harness>`; `dotai restore` puts everything back | done |
| 9 | Replace mode ([0038](decisions/0038-replace-mode-resets-harness-to-fresh-install.md)): keep-lists, harness detection and selection, backup by move, consent, the running-harness check, moves across volumes | `dotai install --mode replace` into a temporary home's real harness paths, then `dotai restore` brings the original back | done |
| 10 | Project scope ([0041](decisions/0041-project-scope-local-and-untracked.md)): local targets per harness, tracked files never written, `.git/info/exclude`, the project record, `dotai restore --project` | Apply a profile to a test repository: `git status` stays clean; `dotai restore --project` leaves the project as before | done |
| 11 | Mods as project-scoped plugins under `.claude/skills/<mod>/` ([0027](decisions/0027-loose-files-and-plugins-only-for-mods.md)) | Apply a profile with a mod and see it load in Claude Code after workspace trust | done |
| 12 | `dotai doctor`, read-only, with stable finding codes, and `--fix` for exclude entries | Edit an installed file by hand and see `dotai doctor` report it | done |
| 13 | Local web UI, `dotai ui` ([0043](decisions/0043-local-web-ui.md)): boards for the packs, the install plan, the repositories, the backups and `doctor`; every action shown as its plan first | Install, apply, restore and fix from the browser, each carried out only as the plan it showed | done |
| 14 | Packs as git repositories, github.com only ([0044](decisions/0044-git-pack-distribution.md)): `dotai pack add`, `update`, `remove` and `list`, the registry, and the UI screens to add a pack and review an update | `dotai pack update` shows what changed in hooks, MCP servers, settings, tools and mods before it swaps the clone | done |
| 15 | Project onboarding owned by dotai ([0045](decisions/0045-dotai-owned-project-onboarding.md)): a generated profiling skill and a session-start hook | A session in a new repository proposes profiles; the user approves the plan | done |
| 16 | Pack template and wizard ([0046](decisions/0046-pack-templates-and-wizard.md)): `dotai pack new` and the UI's Create a pack step | `dotai pack new my-pack --name my-pack --harness claudecode`, then `dotai validate my-pack` | done |
| 17 | Release tooling ([0042](decisions/0042-tag-driven-release-binaries.md)): binaries per platform from a `vX.Y.Z` tag, with `SHA256SUMS` | `scripts/release.sh --self-test`; the release workflow runs CI first | done |
| 18 | First release, [`v0.1.0`](https://github.com/Lartweib/dotai/releases/tag/v0.1.0), through release-please ([0052](decisions/0052-release-please-owns-versions-and-changelog.md)) | Install `dotai` from a release archive and repeat the demo of row 8 | done |
| 19 | OpenCode out of preview: rulesync 43.12.0's generated OpenCode hooks plugin passes a `sessionStart` hook's output to the model and registers it without waiting on earlier events ([dyoshikawa/rulesync#3926](https://github.com/dyoshikawa/rulesync/pull/3926), [#3932](https://github.com/dyoshikawa/rulesync/pull/3932)); what remains is a live OpenCode session that confirms it ([HARNESS-NOTES](HARNESS-NOTES.md#rulesync-43120)) | The hook checks of [HARNESS-NOTES](HARNESS-NOTES.md#hook-commands) pass on OpenCode | next |

Decision records written during development cite the pull-request numbers of an earlier version of this roadmap: 8, 8b, 8c and 9 are row 8, 9b-1 to 9b-4 the replace mode (row 9), 10-1 to 10-4 the project scope and mods (rows 10 and 11), 11 `doctor` (row 12), 12 the release tooling (row 17), and 13 and 14 the web UI (row 13).

## After v1

In priority order:

| # | Deliverable | Status |
|---|---|---|
| 1 | A `doctor` check of the always-on context budget, split into rules, the skill and agent listing, and the total, against caps the pack declares ([0007](decisions/0007-always-on-context-budget.md)) | later |
| 2 | `doctor` checks the external skills a profile expects (`capabilities`) | later |
| 3 | `claudecode.skillOverrides` merged per skill across layers | later |
| 4 | Support for a newer rulesync (43.x), with its checksums embedded ([0029](decisions/0029-rulesync-checksums-embedded-in-the-engine.md)) | later |
| 5 | The rest of [0044](decisions/0044-git-pack-distribution.md): the pack identity in the install state, the backups and the project records, with its flat form for `.dotai/packs/<flat>/` and the rules file; a JSON Schema of `dotai.yaml`; the `validate` warnings `layer-undeclared` and `flat-name-ambiguous` | later |
| 6 | Engine support for stack detection during onboarding ([0020](decisions/0020-v1-scope-engine-and-pack.md)) | later |
