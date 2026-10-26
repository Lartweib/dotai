---
status: accepted
date: 2026-10-26
tags: [engine, go, architecture, testing]
---

# 0028 · A plan-centric engine with a pure domain and ports

## Context

The engine's v1 behavior is decided ([0020](0020-v1-scope-engine-and-pack.md), [0021](0021-go-engine-orchestrates-rulesync.md), [PACK-FORMAT](../PACK-FORMAT.md)), but not the shape of its code. Several features need the same thing: an exact list of what a command will change before it changes it.

- Backup copies exactly what will be touched ([0018](0018-install-modes-and-mandatory-backup.md)).
- Every command prints its result as `--json` ([0026](0026-json-cli-and-ui-after-v1.md)).
- `doctor` reports what changed outside dotai ([0020](0020-v1-scope-engine-and-pack.md)).
- Merge by key removes only what dotai recorded as its own ([0013](0013-renderer-merges-by-key.md)).

## Decision

**Every command builds a plan, then executes it.** The pipeline is: resolve layers → validate → render into staging → build the plan → back up → execute → record state. The plan is the list of file operations (create, replace, merge keys, remove owned entries), each with the target, the keys it touches and the expected hash of the current content.

- `--dry-run` prints the plan; `--json` serializes it.
- Backup copies exactly the plan's targets.
- If anything fails before execution, nothing was written. Execution checks each target's hash first and stops if the file changed after planning.
- `doctor` builds the plan for the installed state and reports the differences with the disk.

**Packages** (`internal/`):

| Package | Role |
|---|---|
| `pack` | Manifest, layers, profiles, variants and `dotai:` blocks: loading and validation. Reads through `fs.FS`. |
| `merge` | The layer merge rules of [0023](0023-flat-profiles-and-layer-merge-rules.md), as pure functions. Builds one staging tree per scope. |
| `render` | What rulesync does not cover: per-harness settings ([0025](0025-settings-as-per-harness-passthrough.md)), runners, tools, variable expansion such as `${DOTAI_TOOLS}`, and the `plugin.json` of mods ([0027](0027-loose-files-and-plugins-only-for-mods.md)). |
| `plan` | Plan model, diff of staging against the disk and the ownership record, and key-level merge of JSON/JSONC files. |
| `backup` | Backups and restore with manifest and hashes ([0018](0018-install-modes-and-mandatory-backup.md)). |
| `state` | Installed pack, variant, profiles and mode, and the ownership record of files and keys ([0013](0013-renderer-merges-by-key.md)). |
| `harness` | The `Adapter` port and one adapter per harness (`claudecode`, `antigravity`, `opencode`). |
| `rulesync` | Downloads the pinned binary, verifies `SHA256SUMS`, runs it into staging ([0021](0021-go-engine-orchestrates-rulesync.md)). |
| `app` | Use cases: `install`, `apply`, `validate`, `doctor`, `restore`. The CLI and a future UI call only this layer. |
| `platform` | Filesystem, processes, environment and OS paths, behind interfaces. |

`cmd/dotai` only parses flags and calls `app`.

**Dependency rule:** `pack`, `merge` and `plan` do no I/O. They receive data or an `fs.FS` and return values. Disk, processes and network go through `platform` and `rulesync`.

**The `Adapter` port** declares, per harness: settings files and their format per scope; owned and shared paths; rulesync target names; how to detect the harness and its version; the isolation mechanism, its level and whether a new login is needed; and how to launch it isolated. Detecting and launching are real logic (the agy adapter checks that `--gemini_dir` exists); the rest is data.

**Testing:** strict TDD. Table tests for `merge` and `plan`, golden files for staging and plans, a fake rulesync in unit tests and the pinned binary in integration tests over a fixture pack. Integration tests use temp directories as home; they never touch the real harness configuration.

## Considered options

- **A (chosen):** plan-centric, pure domain behind ports.
- **B:** commands that write as they go. Rejected: backup, `--dry-run`, `--json` and `doctor` would each need their own logic for what will change, and a failure midway leaves a half-applied install.

## Consequences

- Good: a new harness is a new adapter plus its rulesync target; the domain does not change.
- Good: the future UI ([0026](0026-json-cli-and-ui-after-v1.md)) reuses `app` and the plan as-is.
- Resolves the pending item of [0021](0021-go-engine-orchestrates-rulesync.md): dotai merges the layers of a scope into one staging tree; rulesync's `--input-roots` is not used.
- Accepted cost: the plan model must express every operation up front, including key-level merges.
- Key-level edits of JSON and JSONC files (for example `opencode.jsonc`) use `github.com/tailscale/hujson` for parsing and packing, plus a small own edit layer in `plan` for set, delete and array union. hujson's own patch is not used: it loses indentation, CRLF line endings and comments next to edited keys. A prototype of the layer kept every untouched byte, CRLF included, and was idempotent (internal research, not published, 2026-10-27).
- Accepted cost: dotai owns and tests that edit layer. Comments on their own line above a deleted key are deleted with it, and inserted arrays and objects are written one item per line. hujson has no tagged releases, so it is pinned by commit.
