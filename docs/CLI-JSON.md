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
| `data` | The command result. Present only when `ok` is `true`. Its shape is defined per command. |
| `error` | `code` and `message`. Present only when `ok` is `false`. |

Fields that are unknown, such as the commit of a local build, are empty strings, never omitted.

## Error codes and exit codes

| `error.code` | Exit code | When |
|---|---|---|
| — | 0 | Success. |
| `internal` | 1 | The command failed while running. |
| `usage` | 2 | Unknown command, unexpected argument or invalid flag. |

Without `--json`, results are plain text on stdout and errors go to stderr, with the same exit codes.

## Commands

| Command | `data` |
|---|---|
| `version` | `version`, `commit`, `buildDate`, `goVersion`, `os`, `arch` |
