# Roadmap

The delivery plan for the engine, as a sequence of pull requests. Every PR is a deliverable: it leaves something that can be run or inspected, does one thing, and carries its own tests. PRs are stacked on `main`: each one merges into `main` and the next one starts from there. Rows split into parts, such as 9b-1 to 9b-4, are sequential PRs in the same way.

Demos use the minimal example pack in the repository (`examples/minimal-pack`) and a temporary home directory. They never touch a real harness configuration.

The scope is set by [0020](decisions/0020-v1-scope-engine-and-pack.md) and the code structure by [0028](decisions/0028-plan-centric-hexagonal-engine.md).

## v1

| # | Pull request | What can be seen or tried |
|---|---|---|
| 1 | Skeleton: Go module, `cmd/dotai`, versioned `--json` output, CI on Windows, Linux and macOS | `dotai version` and `dotai version --json` |
| 2 | `pack`: load the manifest, layers and `dotai:` blocks; add the example pack | `dotai validate examples/minimal-pack`; a broken manifest reports file and line |
| 3 | `merge`: layer merge rules ([0023](decisions/0023-flat-profiles-and-layer-merge-rules.md)) and one staging tree per scope | `dotai validate` rejects duplicate names and undeclared overrides; `dotai stage --out <dir>` shows the merged tree |
| 4 | `rulesync`: download the pinned binary, verify its checksum, cache it and run it into staging | `dotai stage` shows the real harness output (`CLAUDE.md`, `AGENTS.md`, agents) |
| 5 | `render`: per-harness settings, path variables such as `${DOTAI_TOOLS}`, and tools | Staging contains the Claude Code and agy `settings.json` |
| 6 | JSON/JSONC edit layer over hujson | Golden tests show before and after: comments, key order and CRLF kept |
| 7 | `plan` and the adapters as data | `dotai install --dry-run` prints the exact plan, readable or as `--json` |
| 8 | `backup`, `state` and isolated execution for Claude Code | `dotai install --mode isolated`, then open Claude Code isolated with the pack; `dotai restore` puts everything back |
| 9 | Isolated mode for agy and OpenCode | The same demo on the other two harnesses |
| 9b-1 | Replace mode decision ([0038](decisions/0038-replace-mode-resets-harness-to-fresh-install.md)) and the verification of what each harness reads and creates at user level | [HARNESS-NOTES](HARNESS-NOTES.md#replace-inventory): each fact verified with its version and OS, or marked unverified |
| 9b-2 | Replace layout in the adapters: keep-lists and leak entries, harness detection and selection (`--harness`), `optional-harnesses` in the manifest, the refusal on relocation variables in the plan, the mode default and switch rules, `~/.agents/skills` per variant | `dotai install --mode replace --dry-run` lists what would be wiped, kept and written per harness, and the detection of each harness |
| 9b-3 | Backup by move, wipe operations, journal and rollback, restore of wipes, the pinned first replace backup | Tests in a temporary home: a wipe moves entries into the backup, a failure rolls them back, and `dotai restore` moves them back exactly |
| 9b-4 | Replace enabled: consent, running-harness check, refusals enforced, the `dotai run` message, the move across volumes (copy, sync and delete), CLI-JSON | `dotai install --mode replace` into a temporary home's real harness paths, then `dotai restore` brings the original back |
| 10-1 | Project scope decision ([0041](decisions/0041-project-scope-local-and-untracked.md)) and the verification of what each harness reads in a project, excluded files and hook commands included | [HARNESS-NOTES](HARNESS-NOTES.md#project-scope-inventory): each fact verified with its version and OS, or marked unverified |
| 10-2 | Project layout in the adapters: the local target of each kind per harness, the tracked-file check through git with each item left uninstalled named, the warnings for a harness with no local slot, the `.git/info/exclude` entries (pending outside a git repository), the refusal in the home directory and at a drive root, same-name clashes across scopes | `dotai apply --profile <p> --dry-run` in a test repository lists, per harness, each target, the items skipped because git tracks their file, and the exclude entries |
| 10-3 | `dotai apply --profile` enabled: writes with merge by key and union, the exclude block, the project record in `.dotai/` with its ownership record, rollback, per-project MCP keys in `~/.claude.json` (retry on `EPERM`, re-read, one rewrite), the reserved `.dotai/packs/<pack>/`, the first-apply flag in CLI-JSON, replace keeping the owned MCP keys | Apply a profile to a test repository: `git status` stays clean, foreign content is kept, and a second apply reports it is not the first |
| 10-4 | Mods as plugins under `.claude/skills/<mod>/` ([0027](decisions/0027-loose-files-and-plugins-only-for-mods.md)), excluded like the rest | Apply a profile with a mod and see it load in Claude Code after workspace trust |
| 11 | `doctor` | Edit an installed file by hand and see `dotai doctor` report it; it also reports owned per-project MCP keys that went missing, orphan entries whose path no longer exists, and completes pending exclude entries ([0041](decisions/0041-project-scope-local-and-untracked.md)) |
| 12 | Release: binaries per platform | Install `dotai` from a release and repeat the PR 8 demo |

Follow-ups from [0041](decisions/0041-project-scope-local-and-untracked.md), not decided yet, to be placed in a row when they are:

- `${DOTAI_TOOL:<name>}` in hook commands, with the quoting per harness of [0037](decisions/0037-tool-commands-resolved-per-os.md): agy runs hooks through `cmd /c` and escapes quotes, so a path without spaces only ([HARNESS-NOTES](HARNESS-NOTES.md#hook-commands)).
- Done: identical MCP definitions in several profiles, or in two layers of a scope, accepted and kept once ([PACK-FORMAT](PACK-FORMAT.md#merge-rules)).
- Done: a `validate` warning, `hooks-shadowed`, when the same hook event is in the shared block and a per-harness block of `hooks.jsonc`, since rulesync 41 drops the shared one for that harness.
- agy `SessionStart` hooks must print JSON (`injectSteps`); document it for pack authors in [PACK-FORMAT](PACK-FORMAT.md).

Checkpoints: PR 4 is the first real harness output, PR 8 the first real use with a safe way back, PR 9b-4 the first install over the real configuration, and PR 10-4 closes the v1 scope.

## After v1

The UI is a thin layer over the same use cases as the CLI ([0026](decisions/0026-json-cli-and-ui-after-v1.md)). `dotai ui` runs inside the same binary and calls the `app` package directly, returning the same structures as `--json`. It listens only on `127.0.0.1`, uses a random token and embeds its assets.

| # | Pull request | What can be seen or tried |
|---|---|---|
| 13 | `dotai ui`, read-only | Installed state, the current plan and the `doctor` result in the browser |
| 14 | UI actions | Install, apply profiles and restore from the UI; each action shows its plan and asks for confirmation |

Also after v1: engine support for stack detection during onboarding ([0020](decisions/0020-v1-scope-engine-and-pack.md)).
