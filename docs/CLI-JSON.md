# CLI JSON output

Every dotai command accepts the global `--json` flag. With it, the command prints exactly one JSON document, the **envelope**, to stdout. This is the contract scripts and a future UI build on ([0026](decisions/0026-json-cli-and-ui-after-v1.md)); changing it needs the same care as changing a flag.

## Envelope

Success:

```json
{
  "schema": "dotai.cli/v1",
  "command": "version",
  "ok": true,
  "data": { "version": "1.2.3", "commit": "…", "buildDate": "…", "goVersion": "go1.26.5", "os": "linux", "arch": "amd64" }
}
```

Failure:

```json
{
  "schema": "dotai.cli/v1",
  "command": "dotai",
  "ok": false,
  "error": { "code": "usage", "message": "unknown command \"nope\" for \"dotai\"" }
}
```

| Field | Meaning |
|---|---|
| `schema` | Envelope version. A breaking change to the envelope gets a new value. |
| `command` | Command path without the program name, for example `version`. `dotai` when no command was resolved. |
| `ok` | `true` on success, `false` on failure. |
| `data` | The command result. Always present when `ok` is `true`, as an object: `{}` when the command has no result, never `null`. When `ok` is `false`, present only with the `invalid` error code, where it holds the result that explains the failure; absent otherwise. Its shape is defined per command. |
| `error` | `code` and `message`. Present only when `ok` is `false`. |

Fields that are unknown, such as the commit of a local build, are empty strings, never omitted.

Any `--json` or `--json=<value>` argument requests JSON output, so an invalid value such as `--json=garbage` is reported as a `usage` envelope.

## Error codes and exit codes

| `error.code` | Exit code | When |
|---|---|---|
| — | 0 | Success. |
| `internal` | 1 | The command failed while running. |
| `usage` | 2 | Unknown command, unexpected argument or invalid flag. |
| `invalid` | 3 | The command read its input and rejected it, for example a pack with errors. `data` holds the result with the details. |

Without `--json`, results are plain text on stdout and errors go to stderr, with the same exit codes. One exception: in text mode an invalid pack (`validate`, `stage`, `rulesync fetch` or `install`) prints its report to stdout and nothing to stderr, because the report is the command's result; exit code 3 still signals the failure. `--format` chooses how they look and is ignored with `--json`:

- `pretty` (default): a summary header (`✗ <pack> <version> · 13 problems in 11 files`), then the diagnostics grouped by file in the order above, each with its `line:column` when it has one, its code and message, and the `first:` (related) and `fix:` (hint) lines.
- `compact`: one line per diagnostic, `file[:line[:column]]: code: message`, followed by ` (first: file[:line])` and ` (fix: hint)` when present, so editors can jump to the location.

A whole-file diagnostic (`line` `0`) prints no position in either format. Text output is colored only when stdout is a terminal that supports ANSI colors and `NO_COLOR` is unset or empty; `--json` output is never colored.

## Diagnostics

`validate`, `stage`, `rulesync fetch` and `install` report problems as `diagnostics`. Their stable contract is `code`, `file`, `line`, `column` and `related`: scripts may match on them, and changing them needs a new schema value. `message` and `hint` are text for people and may change in any release without a schema bump.

A diagnostic about a whole file, such as an item defined twice, has `line: 0` (and `column: 0`). Before any release these used `line: 1`; `dotai.cli/v1` is not released yet, so this change keeps the schema value.

## Commands

| Command | `data` |
|---|---|
| `version` | `version`, `commit`, `buildDate`, `goVersion`, `os`, `arch` |
| `validate [path] [--variant v] [--profile a,b]` | Structural checks, then the merge rules over the chosen layers: without `--variant`, the base with each variant in turn; without `--profile`, every profile together. Lists are not merged yet: `.rulesync/hooks.jsonc` or `.rulesync/permissions.jsonc` in two layers of a scope is reported as `merge-pending`, and a `rulesync` version the engine has no checksums for as `rulesync-unsupported` ([0029](decisions/0029-rulesync-checksums-embedded-in-the-engine.md)), which, unlike other structural problems, does not stop the merge rules. `valid`; `pack`: `name`, `version`, `engine`, `rulesync`, `harnesses`, `profiles`, `variants`, `defaultVariant`, `layers` (each `kind`, `name`, `path`, `items`), or `null` when the manifest cannot be read; `diagnostics`: each `file` (relative to the pack, `/`-separated), `line`, `column` (1-based; `line` `0` means the problem concerns the whole file, such as a duplicated item, and `column` `0` the whole line), `code`, `message` (short, without locations or advice), and when they apply `related` (the earlier definition the problem clashes with, a location with `file`, `line` and `column` as above) and `hint` (how to fix it). `related` and `hint` are omitted when empty. See [Diagnostics](#diagnostics) for which fields are stable. A pack with errors fails with `invalid` and carries the same `data`. A path that is not a directory, or a pack that cannot be read (for example a permission error, or a `dotai.yaml` that is a directory), fails with `internal`. |
| `stage [path] --out <dir> [--force] [--variant v] [--profile a,b]` | Validates every layer, then merges the base and the variant (`--variant` or the default) into `<dir>/user` and the profiles into `<dir>/project`. Each scope's tools are staged at `.dotai/tools/` in its tree, and `${DOTAI_TOOLS}` and `${DOTAI_PROJECT_TOOLS}` in every staged text file become `<home>/.dotai/tools` and `<current directory>/.dotai/tools`, with forward slashes ([0030](decisions/0030-tools-paths-and-project-config.md)); when a file uses a token whose directory cannot be found, the stage fails with `internal`, naming the file, the token and the cause, and writes nothing. It then fetches the rulesync the pack pins, as `rulesync fetch` does, checks the cached binary against its SHA-256 again and runs `rulesync generate` for the manifest's `harnesses` over each scope tree that has a `.rulesync/`, with that tree as rulesync's home directory. After rulesync, the merged `dotai/settings.yaml` of each scope is written into each harness's settings file in the scope tree (`.claude/settings.json`, `.gemini/antigravity-cli/settings.json` and `.config/opencode/opencode.jsonc` for the user scope; `.claude/settings.json` and `opencode.jsonc` for the project scope), as JSON with 2-space indentation, with the path variables of string values expanded. When rulesync wrote that file, its keys come first in its order and the settings are appended; a top-level key written by both is a `settings-conflict` diagnostic at the `dotai/settings.yaml` of the layer that sets it (`line` `0`), and the stage fails with `invalid` and writes nothing. rulesync gets only the variables it needs to start (`PATH`, the Windows system variables, `TZ`, `LANG`, `LC_*`); its home, config, data, cache and temporary directories all point inside the scope tree, and its temporary directory is removed after the run. The user scope runs with `--global`, so its output sits at the paths it has under the home directory (`user/.claude/CLAUDE.md`, `user/.gemini/GEMINI.md`, `user/.config/opencode/AGENTS.md`); the project scope's sits at the paths it has in a project (`project/CLAUDE.md`, `project/.claude/rules/web.md`). `files`: each `scope` (`user` or `project`), `path` (inside the scope's tree, `/`-separated), `layer` (path of the layer that provides it, `""` for a merged file such as `.rulesync/mcp.jsonc` and for a rendered file), for a merged file `origins` (the layer of each entry by name) and for a rendered file `generator`: `rulesync` for a file rulesync wrote, `dotai` for a settings file made from `dotai/settings.yaml` alone, `rulesync+dotai` for a file rulesync wrote with settings appended; a settings file also has `origins`, the layer of each key, and of each Claude Code `env` variable as `env.<VAR>`; rendered files come after the staged sources. `diagnostics` as in `validate`. A failed fetch, a checksum mismatch or a failed rulesync run fails with `internal`, with the end of rulesync's stderr (or of its stdout when stderr is empty), and writes nothing; so does a run that is cancelled or takes more than 5 minutes. Without `--json`, a download is announced on stderr as in `rulesync fetch`. A pack with errors fails with `invalid`, carries the same `data` and writes nothing. A missing `--out`, or an `--out` that is the pack directory or inside it, is a `usage` error. `<dir>` must be missing, empty, or an earlier staging, marked by a `.dotai-stage` file; an earlier staging is replaced as a whole only with `--force`, and any other non-empty directory is refused even with `--force` (`internal`). The new staging is written to a temporary directory next to `<dir>` and moved into place, so a failure leaves `<dir>` as it was. Nothing is written outside it, not even through a link. |
| `rulesync fetch [path]` | Validates the pack, then downloads the rulesync binary its manifest pins into `<cache>/rulesync/<version>/<asset>`, where `<cache>` is `DOTAI_CACHE_DIR` or `dotai` in the user cache directory, and verifies it against the SHA-256 built into dotai ([0029](decisions/0029-rulesync-checksums-embedded-in-the-engine.md)). A cached binary is used only when it still matches. `version`, `asset` (the release file name), `path`, `sha256`, `cached` (`true` when no download was needed) and `diagnostics` as in `validate`. A pack with errors fails with `invalid`, carries the same `data` with empty fetch fields and downloads nothing. An unsupported platform, a failed or cancelled download, or a checksum mismatch fails with `internal`. A failed fetch leaves no file of its own, not even a temporary one; a corrupt cached binary that was there before may remain, and the next run checks it again and replaces it. Without `--json`, a real download is announced with one line on stderr, such as `downloading rulesync 41.0.0 (106 MB)...`; a cached binary prints nothing there. |
| `install [path] --dry-run [--variant v]` | Plans an isolated install of the pack's user scope ([0031](decisions/0031-isolated-install-root.md)) and writes nothing: installing itself is not available yet, so without `--dry-run` the command is a `usage` error, and so is `--profile`, since profiles are project scope. The pack is validated and staged as by `stage` with no profile, `--variant` or the default variant, and `${DOTAI_TOOLS}` at `<home>/.dotai/tools`, into a temporary directory that is removed afterwards; rulesync is fetched into the cache as by `rulesync fetch`, which is the only other write. Each staged file is compared with its target by SHA-256 and exec bit (the exec bit is ignored on Windows, which has none). `pack` as in `validate`; `variant` (the one used, `""` for a pack without variants); `mode` (`isolated`, the only mode); `root` (`<home>/.dotai/isolated`); `harnesses`, in manifest order, what [0018](decisions/0018-install-modes-and-mandatory-backup.md) requires to show before installing: each `id`, `level` (`full`, `partial` or `none`), `dir` (its isolated directory), `pointers` (each `mechanism`, `env` for an environment variable or `flag` for a command-line flag, `name`, such as `CLAUDE_CONFIG_DIR` or `--gemini_dir`, and the `dir` it is set to), `relogin` (`true` when the user must log in again) and `leaks` (what the user's own setup still provides, text for people); `ops`, in target order: each `action` (`create` for a missing target, `replace` for one with other content or another exec bit, `unchanged`), `harness` (`""` for a tool, which installs to `<home>/.dotai/tools`), `source` (the path inside the user staging tree, `/`-separated), `target`, `exec` (the staged exec bit), `before` and `beforeExec` (the target's current SHA-256 in hex and exec bit, `""` and `false` when it is missing) and `after` (the staged SHA-256); `diagnostics` as in `validate`. `root`, every `dir` and every `target` are absolute paths with the OS separator, as `path` in `rulesync fetch`. rulesync's sources (`.rulesync/`) are never planned. Every staged path is checked before the disk is read: a path Windows cannot store (a control character such as NUL, one of `:<>"|?*`, or a device name such as `CON`) fails with `internal` on every OS, and a staged file that no harness of the pack and no tools rule installs is a `path-unmapped` diagnostic at `dotai.yaml` (`line` `0`), one per file, naming it as `user/<path>`. Nothing on the way to a target is followed: each directory from the home directory down to the target must be a real directory or missing (a missing one means every target below it is `create`), and the target must be a regular file or missing. A link, a Windows junction or other reparse point, a file where a directory is needed, or a directory or other special file at a target fails with `internal`, naming that path and the fix: move or remove it, then plan again. A pack with errors fails with `invalid` and carries the same `data` with empty `harnesses` and `ops`. A harness whose isolation level is `none` does not offer the isolated mode and fails with `internal`. Without `--json`, the plan is printed as a header with the pack, variant, mode and root; then each harness with its isolation level, whether it needs a new login, each pointer (`NAME=<dir>` for a variable, `--flag <dir>` for a flag) and each leak; then the operations grouped by harness in manifest order with the tools last, one `action  target` line each; and a final count, such as `16 create, 0 replace, 0 unchanged`. A download is announced on stderr as in `rulesync fetch`. |
| help (`dotai --json`, `--help`, `dotai help [command]`) | `help`: the help text. `command` is the command whose help is shown. Exit code 0. |
