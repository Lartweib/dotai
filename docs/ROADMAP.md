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
| 9b-4 | Replace enabled: consent, running-harness check, refusals enforced, the `dotai run` message, CLI-JSON | `dotai install --mode replace` into a temporary home's real harness paths, then `dotai restore` brings the original back |
| 10 | Project scope: `dotai apply --profile`, merge by key, mods as plugins ([0027](decisions/0027-loose-files-and-plugins-only-for-mods.md)) | Apply a profile to a test project and see foreign content kept |
| 11 | `doctor` | Edit an installed file by hand and see `dotai doctor` report it |
| 12 | Release: binaries per platform | Install `dotai` from a release and repeat the PR 8 demo |

Checkpoints: PR 4 is the first real harness output, PR 8 the first real use with a safe way back, PR 9b-4 the first install over the real configuration, and PR 10 closes the v1 scope.

## After v1

The UI is a thin layer over the same use cases as the CLI ([0026](decisions/0026-json-cli-and-ui-after-v1.md)). `dotai ui` runs inside the same binary and calls the `app` package directly, returning the same structures as `--json`. It listens only on `127.0.0.1`, uses a random token and embeds its assets.

| # | Pull request | What can be seen or tried |
|---|---|---|
| 13 | `dotai ui`, read-only | Installed state, the current plan and the `doctor` result in the browser |
| 14 | UI actions | Install, apply profiles and restore from the UI; each action shows its plan and asks for confirmation |

Also after v1: engine support for stack detection during onboarding ([0020](decisions/0020-v1-scope-engine-and-pack.md)).
