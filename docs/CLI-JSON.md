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
| `install [path] --dry-run [--variant v]` | Plans an isolated install of the pack's user scope ([0031](decisions/0031-isolated-install-root.md)) and writes nothing; `install` without `--dry-run` carries the same plan out (next row). `--profile` is a `usage` error, since profiles are project scope. The pack is validated and staged as by `stage` with no profile, `--variant` or the default variant, and `${DOTAI_TOOLS}` at `<home>/.dotai/tools`, into a temporary directory that is removed afterwards; rulesync is fetched into the cache as by `rulesync fetch`, which is the only other write. Each staged file is compared with its target by SHA-256 and exec bit (the exec bit is ignored on Windows, which has none). `pack` as in `validate`; `variant` (the one used, `""` for a pack without variants); `mode` (`isolated`, the only mode); `root` (`<home>/.dotai/isolated`); `harnesses`, in manifest order, what [0018](decisions/0018-install-modes-and-mandatory-backup.md) requires to show before installing: each `id`, `level` (`full`, `partial` or `none`), `dir` (its isolated directory), `pointers` (each `mechanism`, `env` for an environment variable or `flag` for a command-line flag, `name`, such as `CLAUDE_CONFIG_DIR` or `--gemini_dir`, and the `dir` it is set to), `relogin` (`true` when the user must log in again) and `leaks` (what the user's own setup still provides, text for people); `ops`, in target order: each `action` (`create` for a missing target, `replace` for one with other content or another exec bit, `unchanged`, `remove` for a file the previous install owned, by its [install state](#install-state), that the pack no longer stages and that is still there), `harness` (`""` for a tool, which installs to `<home>/.dotai/tools`), `source` (the path inside the user staging tree, `/`-separated, `""` for a `remove`), `target`, `exec` (the staged exec bit, `false` for a `remove`), `before` and `beforeExec` (the target's current SHA-256 in hex and exec bit, `""` and `false` when it is missing) and `after` (the staged SHA-256, `""` for a `remove`); `diagnostics` as in `validate`. A file the previous install owned whose SHA-256 or exec bit is no longer what the install state records was edited by the user: it is neither replaced nor removed, and the plan fails with `internal`, naming every such file (a `--force` that overrides this does not exist yet); a file it owned that is missing is not an edit, and is created again when the pack still stages it. An owned file outside `<home>/.dotai/isolated` and `<home>/.dotai/tools`, or an install state that cannot be read (another `schema`, a target that is not absolute or is owned twice), fails with `internal`. `root`, every `dir` and every `target` are absolute paths with the OS separator, as `path` in `rulesync fetch`. rulesync's sources (`.rulesync/`) are never planned. Every staged path is checked before the disk is read: a path Windows cannot store (a control character such as NUL, one of `:<>"|?*`, or a device name such as `CON`) fails with `internal` on every OS, and a staged file that no harness of the pack and no tools rule installs is a `path-unmapped` diagnostic at `dotai.yaml` (`line` `0`), one per file, naming it as `user/<path>`. Nothing on the way to a target is followed: each directory from the home directory down to the target must be a real directory or missing (a missing one means every target below it is `create`), and the target must be a regular file or missing. A link, a Windows junction or other reparse point, a file where a directory is needed, or a directory or other special file at a target fails with `internal`, naming that path and the fix: move or remove it, then plan again. A pack with errors fails with `invalid` and carries the same `data` with empty `harnesses` and `ops`. A harness whose isolation level is `none` does not offer the isolated mode and fails with `internal`. Without `--json`, the plan is printed as a header with the pack, variant, mode and root; then each harness with its isolation level, whether it needs a new login, each pointer (`NAME=<dir>` for a variable, `--flag <dir>` for a flag) and each leak; then the operations grouped by harness in manifest order (a harness the pack no longer has, whose files are removed, after them) with the tools last, one `action  target` line each; and a final count, such as `16 create, 0 replace, 0 remove, 0 unchanged · dry run, nothing was written`. A download is announced on stderr as in `rulesync fetch`. |
| `install [path] [--variant v]` | Installs the plan of `install --dry-run`, built the same way, as [0018](decisions/0018-install-modes-and-mandatory-backup.md), [0028](decisions/0028-plan-centric-hexagonal-engine.md) and [0032](decisions/0032-install-state-backup-and-isolated-launch.md) decide. The pack is validated and planned first: a pack with errors fails with `invalid`, carries the same `data` with `backup` `null`, `state` `""` and `warnings` `[]`, and nothing is written, not even `<home>/.dotai/`. Only when the plan writes something does the install take `<home>/.dotai/lock`, created only when it is missing; when it is there, the install fails with `internal`, naming it with the process id written in it and how long ago it was taken: another dotai is running, or one that stopped left it, and it must then be removed by hand. Under the lock, the [install state](#install-state) must still be the one the plan read, or the install fails with `internal`, naming it, and writes nothing. Before the first write, a [backup](#backups) records every `create`, `replace` and `remove`, with a copy of each target that is replaced or removed, checked against the plan and synced to the disk; if the backup fails, the incomplete backup is removed and nothing is written. The install then writes its journal (`pending`) into the install state. Each operation, in target order, checks again that no directory above its target is a link, a junction or not a directory, and that the target is still as the plan found it (missing for a `create`, the same SHA-256 and exec bit for a `replace` or `remove`). Every write goes through the target's harness directory or the tools directory, opened so that no link or junction inside it can lead the write outside it; the only window left is between checking the directories from the home directory down to that directory and opening it. A `create` makes the missing directories (mode `0755`); a `create` or `replace` writes a temporary file next to the target, syncs it and moves it over it, with mode `0755` when `exec` is set and `0644` otherwise, but never more permissive than a file already there (a `0600` file stays `0600`; Windows keeps no exec bit); a `remove` deletes the target and the directories it leaves empty, up to but never including the harness directory or the tools directory; `unchanged` does nothing. On Windows, a move or removal refused with access denied or a sharing violation, as when another program holds the file for a moment, is retried up to five times with a growing wait, and a read-only attribute on the target is cleared first. A target that changed after planning, a cancellation such as Ctrl-C, or any other failure stops the install and rolls back, last first, what it wrote, from the backup just taken, with up to a minute of its own even when cancelled: a created target is deleted, with the directories the install made when they are empty, and a replaced or removed one is written back from its copy; a target that is neither what the install wrote nor what was there is left as it is and reported. A rollback that works also puts back the install state the install started from, or removes it when there was none. The install then fails with `internal`, naming the cause, the rollback's outcome and the backup, which is kept. A rollback that fails, or an install that is killed, leaves the journal, and the next install resumes from what is on disk (see [Install state](#install-state)). The final install state is written last; a failure to write it is rolled back too. After that, older backups are pruned as [Backups](#backups) describes. An install whose plan writes no file takes no backup, and takes no lock when the install state already records the same install (apart from `installedAt` and `backup`); otherwise it writes the state, keeping the previous `backup` (or the journal's). `data` is the plan, as in `install --dry-run`, with `backup` (its `id` and absolute `path`, or `null` when no file was written), `state` (the absolute path of the install state) and `warnings`: what failed after the install itself succeeded (removing the temporary staging, pruning old backups, removing the lock), text for people, `[]` when nothing did; the exit code is still 0. Without `--json`, the plan is printed as in `install --dry-run`, with a final count such as `16 create, 0 replace, 0 remove, 0 unchanged · installed`, then `backup: <path>` (or `backup: none, no file was written`) and `state:  <path>`; each warning goes to stderr as `warning: <text>`. |
| help (`dotai --json`, `--help`, `dotai help [command]`) | `help`: the help text. `command` is the command whose help is shown. Exit code 0. |

## Install state

`install` records what it installed in `<home>/.dotai/state.json` ([0032](decisions/0032-install-state-backup-and-isolated-launch.md)), readable by the user only (`0600`) and always written atomically (a temporary file in `<home>/.dotai/` moved over it). It is not an envelope, and `install --dry-run` and the next `install` read it for its ownership record.

```json
{
  "schema": "dotai.state/v1",
  "pack": { "name": "minimal-pack", "version": "0.1.0", "path": "/home/u/packs/minimal-pack" },
  "variant": "standalone",
  "mode": "isolated",
  "installedAt": "2026-10-29T10:15:02.31Z",
  "harnesses": ["claudecode", "antigravity-cli", "opencode"],
  "backup": "20261029T101501.874Z_install",
  "files": [
    { "target": "/home/u/.dotai/isolated/claude/CLAUDE.md", "harness": "claudecode", "sha256": "8e91…", "exec": false },
    { "target": "/home/u/.dotai/tools/hello.sh", "harness": "", "sha256": "1e59…", "exec": true }
  ]
}
```

| Field | Meaning |
|---|---|
| `schema` | State version. A state with another value is not read: `install` fails with `internal`. |
| `pack` | The pack's `name` and `version`, and the absolute `path` it was installed from. |
| `variant`, `mode`, `harnesses` | As in the plan. |
| `installedAt` | When the state was written, in UTC (RFC 3339). |
| `backup` | The id of the backup of the last install that wrote files, `""` when none did. |
| `files` | The ownership record, in target order: every file the install leaves in place, written or already `unchanged`, with its absolute `target`, `harness` (`""` for a tool), `sha256` and `exec` as written. A removed file is no longer listed. |
| `pending` | Present only while an install is writing, and left behind when it was killed or its rollback failed: the journal, with its `backup` id and `ops`, each `action`, `target`, `harness`, and `before` and `after` as in the backup manifest. `files` is then what dotai owned before that install. |

The next install, and `install --dry-run`, read a target of `pending` as dotai's when it holds what `files` or `pending` says dotai wrote; a target dotai did not own before that still holds what `pending` records as `before` is the user's own file again, so it is neither an edit nor removed. Any other content is an edit, as for `files`. The install that follows drops `pending`.

## Backups

Each `install` that writes a file first takes a backup in `<home>/.dotai/backups/<id>/`, as [0018](decisions/0018-install-modes-and-mandatory-backup.md) describes. The id is the UTC time with milliseconds and the operation, such as `20261029T101501.874Z_install`, so ids sort in time order and are valid file names on Windows. Backup directories are created `0700` and their files `0600`. A backup holds `manifest.json` and, under `files/`, a copy of each target that is replaced or removed, at its path relative to the home directory (for example `files/.dotai/tools/hello.sh`), so that it can be restored by hand. Only plan targets are ever copied: nothing else in an isolated directory, such as a harness's credentials, is backed up.

```json
{
  "schema": "dotai.backup/v1",
  "id": "20261029T101501.874Z_install",
  "operation": "install",
  "createdAt": "2026-10-29T10:15:01.874Z",
  "home": "/home/u",
  "mode": "isolated",
  "pack": { "name": "minimal-pack", "version": "0.1.0", "path": "/home/u/packs/minimal-pack" },
  "variant": "standalone",
  "harnesses": ["claudecode", "antigravity-cli", "opencode"],
  "ops": [
    { "action": "create", "target": "/home/u/.dotai/isolated/claude/CLAUDE.md", "after": { "sha256": "8e91…", "exec": false } },
    { "action": "replace", "target": "/home/u/.dotai/tools/hello.sh", "file": "files/.dotai/tools/hello.sh",
      "before": { "sha256": "77ac…", "exec": true }, "after": { "sha256": "1e59…", "exec": true } },
    { "action": "remove", "target": "/home/u/.dotai/isolated/claude/rules/old.md", "file": "files/.dotai/isolated/claude/rules/old.md",
      "before": { "sha256": "c0de…", "exec": false } }
  ]
}
```

`ops` lists every target the install writes, in target order; `unchanged` targets are left out. `before` is what the target had, absent for a `create`; `after` is what it is given, absent for a `remove`; `file` is the copy inside the backup, present for a `replace` and a `remove`. A created target has no copy, so that a restore deletes it. The manifest is written after every copy, so a backup without one is incomplete; the copies, the manifest and their directories are synced to the disk (directories where the OS can) before the install writes anything else. After a successful install, backups are pruned: complete backups are ordered by the `createdAt` of their manifests, then by id, so a clock that went back does not reorder them; the last 10 are kept, and so is the oldest, which is never deleted. The backup just taken and the one the install state names are never deleted. A backup without a manifest does not count and is deleted, since under the lock only an install that stopped can have left it; a backup whose manifest cannot be read is left alone, and directories whose names are not backup ids are never touched. `dotai restore` comes with roadmap PR 8b.
