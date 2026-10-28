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

Without `--json`, results are plain text on stdout and errors go to stderr, with the same exit codes.

## Commands

| Command | `data` |
|---|---|
| `version` | `version`, `commit`, `buildDate`, `goVersion`, `os`, `arch` |
| `validate [path] [--variant v] [--profile a,b]` | Structural checks, then the merge rules over the chosen layers: without `--variant`, the base with each variant in turn; without `--profile`, every profile together. `dotai/settings.yaml` is not merged yet: the same file in two layers of a scope is reported as `settings-merge-pending`. `valid`; `pack`: `name`, `version`, `engine`, `rulesync`, `harnesses`, `profiles`, `variants`, `defaultVariant`, `layers` (each `kind`, `name`, `path`, `items`), or `null` when the manifest cannot be read; `diagnostics`: each `file` (relative to the pack, `/`-separated), `line`, `column` (1-based, `0` when unknown), `code`, `message`. A pack with errors fails with `invalid` and carries the same `data`. A path that is not a directory, or a pack that cannot be read (for example a permission error, or a `dotai.yaml` that is a directory), fails with `internal`. |
| help (`dotai --json`, `--help`, `dotai help [command]`) | `help`: the help text. `command` is the command whose help is shown. Exit code 0. |
