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

Without `--json`, results are plain text on stdout and errors go to stderr, with the same exit codes. One exception: in text mode an invalid pack (`validate` or `stage`) prints its report to stdout and nothing to stderr, because the report is the command's result; exit code 3 still signals the failure. `--format` chooses how they look and is ignored with `--json`:

- `pretty` (default): a summary header (`✗ <pack> <version> · 13 problems in 11 files`), then the diagnostics grouped by file in the order above, each with its `line:column` when it has one, its code and message, and the `first:` (related) and `fix:` (hint) lines.
- `compact`: one line per diagnostic, `file[:line[:column]]: code: message`, followed by ` (first: file[:line])` and ` (fix: hint)` when present, so editors can jump to the location.

A whole-file diagnostic (`line` `0`) prints no position in either format. Text output is colored only when stdout is a terminal that supports ANSI colors and `NO_COLOR` is unset or empty; `--json` output is never colored.

## Diagnostics

`validate` and `stage` report problems as `diagnostics`. Their stable contract is `code`, `file`, `line`, `column` and `related`: scripts may match on them, and changing them needs a new schema value. `message` and `hint` are text for people and may change in any release without a schema bump.

A diagnostic about a whole file, such as an item defined twice, has `line: 0` (and `column: 0`). Before any release these used `line: 1`; `dotai.cli/v1` is not released yet, so this change keeps the schema value.

## Commands

| Command | `data` |
|---|---|
| `version` | `version`, `commit`, `buildDate`, `goVersion`, `os`, `arch` |
| `validate [path] [--variant v] [--profile a,b]` | Structural checks, then the merge rules over the chosen layers: without `--variant`, the base with each variant in turn; without `--profile`, every profile together. Lists and values are not merged yet: `dotai/settings.yaml`, `.rulesync/hooks.jsonc` or `.rulesync/permissions.jsonc` in two layers of a scope is reported as `merge-pending`, and a `rulesync` version the engine has no checksums for as `rulesync-unsupported` ([0029](decisions/0029-rulesync-checksums-embedded-in-the-engine.md)). `valid`; `pack`: `name`, `version`, `engine`, `rulesync`, `harnesses`, `profiles`, `variants`, `defaultVariant`, `layers` (each `kind`, `name`, `path`, `items`), or `null` when the manifest cannot be read; `diagnostics`: each `file` (relative to the pack, `/`-separated), `line`, `column` (1-based; `line` `0` means the problem concerns the whole file, such as a duplicated item, and `column` `0` the whole line), `code`, `message` (short, without locations or advice), and when they apply `related` (the earlier definition the problem clashes with, a location with `file`, `line` and `column` as above) and `hint` (how to fix it). `related` and `hint` are omitted when empty. See [Diagnostics](#diagnostics) for which fields are stable. A pack with errors fails with `invalid` and carries the same `data`. A path that is not a directory, or a pack that cannot be read (for example a permission error, or a `dotai.yaml` that is a directory), fails with `internal`. |
| `stage [path] --out <dir> [--force] [--variant v] [--profile a,b]` | Validates every layer, then merges the base and the variant (`--variant` or the default) into `<dir>/user` and the profiles into `<dir>/project`. `files`: each `scope` (`user` or `project`), `path` (inside the scope's tree, `/`-separated), `layer` (path of the layer that provides it, `""` for a merged file such as `.rulesync/mcp.jsonc`) and, for a merged file, `origins` (the layer of each entry by name); `diagnostics` as in `validate`. A pack with errors fails with `invalid`, carries the same `data` and writes nothing. A missing `--out`, or an `--out` that is the pack directory or inside it, is a `usage` error. `<dir>` must be missing, empty, or an earlier staging, marked by a `.dotai-stage` file; an earlier staging is replaced as a whole only with `--force`, and any other non-empty directory is refused even with `--force` (`internal`). The new staging is written to a temporary directory next to `<dir>` and moved into place, so a failure leaves `<dir>` as it was. Nothing is written outside it, not even through a link. |
| help (`dotai --json`, `--help`, `dotai help [command]`) | `help`: the help text. `command` is the command whose help is shown. Exit code 0. |
