# Pack format

Status: draft for `dotai.pack/v1`. Decided in [0022](decisions/0022-pack-format-and-manifest.md) and [0023](decisions/0023-flat-profiles-and-layer-merge-rules.md), [0024](decisions/0024-models-declared-per-harness-no-tiers.md) and [0025](decisions/0025-settings-as-per-harness-passthrough.md).

A pack is the content that the dotai engine installs. The engine contains nothing personal; everything opinionated lives in a pack ([0010](decisions/0010-engine-and-pack-separated.md)).

## Layout

```
my-pack/
  dotai.yaml               # manifest
  base/                    # always applied
    .rulesync/             # rendered by rulesync
    dotai/                 # handled by dotai
  profiles/
    web/                   # same shape as base/
    godot/
  variants/
    standalone/            # same shape as base/
    orca/
```

Every layer has the same shape:

| Path | Owner | Content |
|---|---|---|
| `.rulesync/` | rulesync | `rules/*.md`, `commands/*.md`, `subagents/**/*.md`, `skills/<name>/SKILL.md`, `mcp.jsonc`, `hooks.jsonc`, `permissions.jsonc` |
| `dotai/settings.yaml` | dotai | Settings rulesync does not cover: `statusLine`, `env`, model defaults |
| `dotai/tools/` | dotai | Scripts, binaries and config files the layer installs ([0030](decisions/0030-tools-paths-and-project-config.md)) |

A layer may omit any of these paths. A pack contains no symbolic links, only regular files and directories, and every file the engine reads is at most 1 MiB. The old rulesync names `.rulesync/mcp.json`, `.rulesync/hooks.json` and `.rulesync/permissions.json` are an error, `legacy-file-name`: rulesync ignores such a file when the `.jsonc` one exists, which another layer may provide, and the merge rules read only the `.jsonc` names. Rename the file to its `.jsonc` name.

## Manifest

```yaml
schema: dotai.pack/v1
name: example-pack
version: 0.1.0
engine: ">=0.1.0"
rulesync: 41.0.0
harnesses: [claudecode, antigravity-cli, opencode]

profiles:
  web:
    path: profiles/web
    description: Web projects (Astro, Next.js), with SEO skills.
    hints: [astro.config.*, next.config.*]
  godot:
    path: profiles/godot
    description: Godot 4 games in GDScript.
    hints: [project.godot]

variants:
  standalone:
    path: variants/standalone
    default: true
  orca:
    path: variants/orca
    detect: { command: orca }
  shared:
    path: variants/shared
    shared-skills: true
```

| Field | Required | Meaning |
|---|---|---|
| `schema` | yes | Format version. |
| `name`, `version` | yes | Pack identity. |
| `engine` | yes | Range of engine versions the pack supports. |
| `rulesync` | yes | Exact rulesync version the pack was tested with. |
| `harnesses` | yes | Required targets, using rulesync target names: `claudecode`, `antigravity-cli`, `opencode`. An install where one of them is not installed, or is left out with `--harness`, stops before writing anything, naming it and why ([0038](decisions/0038-replace-mode-resets-harness-to-fresh-install.md)). |
| `optional-harnesses` | no | Targets the pack also supports, with the same names as `harnesses`; each one at most once and none of them also in `harnesses` (`harness-duplicate`), and an unknown name is `harness-unknown`. An install skips one that is not installed, with a notice ([0038](decisions/0038-replace-mode-resets-harness-to-fresh-install.md)). `dotai/settings.yaml` may hold a block for it, and `stage` renders it with the required ones. |
| `profiles.<name>.path`, `variants.<name>.path` | yes | Layer directory, relative to the pack root. |
| `profiles.<name>.description` | yes | What the profile is for. Read by the onboarding agent. |
| `profiles.<name>.hints` | no | Evidence for the onboarding agent. The engine never acts on them. |

`dotai validate --json` lists each profile with its `path`, `description` and `hints` in `pack.profiles`, in declaration order, so the onboarding agent can read them without parsing the manifest ([CLI-JSON](CLI-JSON.md)).
| `variants.<name>.default` | no | Variant used when nothing is chosen or detected. Exactly one variant sets it when the pack has variants. |
| `variants.<name>.detect` | no | Mechanical rule that preselects the variant. `command`: preselect when that command is on `PATH`. |
| `variants.<name>.shared-skills` | no | `true` makes the user's shared skills folder, `~/.agents/skills`, where other tools install skills, visible in the isolated install of this variant ([0036](decisions/0036-shared-skills-per-variant.md)). Default `false`: only the pack's content is visible. Only variants take it. |

`base/` is implicit and always applied.

## Scopes

| Layers | Scope | When |
|---|---|---|
| `base` and the chosen variant | User level, in each harness | Once per machine (`dotai install`) |
| Profiles | Project level (`.claude/`, `.agents/`, `.opencode/`) | Once per project (`dotai apply --profile ...`) |

Profiles are flat and combinable: a project has zero or more of them (for example `--profile web,astro-cloudflare`). There are no sub-profiles. A stack-specific profile exists only when the stack repeats across projects; what is specific to one project goes in that project's own rules.

Profiles are chosen by the onboarding agent after it analyzes the project and asks the user ([0008](decisions/0008-onboarding-by-detection-and-base-profile.md)), or by hand. The engine never picks one. The project records its profiles so later sessions do not ask again.

## Applying a pack

For each scope:

1. Resolve the layers. User scope: `base` and the variant passed with `--variant`, detected, or default. Project scope: the profiles passed with `--profile`.
2. Read the `dotai:` blocks from the sources.
3. Run `dotai validate` over all layers, both scopes included. Structural checks (manifest, layer paths, file types, `dotai:` blocks) run first. The [merge rules](#merge-rules) run only when they found no errors, and their diagnostics come after the structural ones.
4. Merge the scope's layers into its own staging tree.
5. Run the pinned rulesync over the staging `.rulesync/`, writing into staging.
6. Render `dotai/` content for each harness.
7. Apply the staging output to the harnesses with the active install mode and backup ([0018](decisions/0018-install-modes-and-mandatory-backup.md)).

## Tools and project config

Decided in [0030](decisions/0030-tools-paths-and-project-config.md).

- `dotai/tools/` installs to `~/.dotai/tools/` at user scope and to `.dotai/tools/` at project scope, keeping the executable bit where the file system has one. The same path in two layers of a scope is an error (`tool-conflict`); a layer that adjusts another layer's tool config ships a file with a different name.
- While staging, `${DOTAI_TOOLS}` and `${DOTAI_PROJECT_TOOLS}` become the absolute paths of those directories in every text file of the layers, skills and hooks included, before rulesync runs. A file that is not UTF-8, or that holds a NUL byte, is copied as-is, and no other `${...}` is touched.
- The paths are written with forward slashes on every platform, Windows included: Windows accepts them, and a backslash inside a JSON string, such as a hook command in `.rulesync/hooks.jsonc`, would be an invalid escape.
- A settings value runs a tool with `${DOTAI_TOOL:<name>}` ([0037](decisions/0037-tool-commands-resolved-per-os.md)): the name is the tool's file name without an extension, and the variable becomes the command for the OS of the install, with the script's absolute path in the tools directory of the settings' scope: `powershell.exe -NoProfile -ExecutionPolicy Bypass -File <path>\<name>.ps1` on Windows, which opens no window, and `sh <path>/<name>.sh` on Linux and macOS. Arguments may follow it. For Claude Code, which runs the command through a shell, the path is in double quotes, so spaces work. agy runs the command without a shell and passes quotes on, so for agy the path is bare, and a tools directory with whitespace or a quote fails the dry run and the install, naming agy and the directory: such a home directory cannot run a tool from agy's settings. The layers of that scope must ship both `dotai/tools/<name>.sh` and `dotai/tools/<name>.ps1`: a missing one is `tool-missing`, and a name that is not letters, digits, `.`, `_` and `-`, or a variable without its closing brace, is `tool-invalid`. A tools directory whose path holds `"`, `$` or `` ` ``, or `%` on Windows or `\` elsewhere, fails the stage for every harness, since some shell would not read it literally inside the quotes, and so does one on Windows that starts with `\\` or `//`, such as a network path, which bash reads as a single `\` inside the quotes. The variable is expanded in settings values only. Do not name a script by its path in a command a harness runs: on Windows, a bare `.sh` runs through the file association, in a new window.
- `.dotai/config.yaml` in a project belongs to the user and the pack; the engine never writes it. Tools find it by walking up from the current directory to the nearest `.dotai/`. The engine records the applied profiles in its own file in the same directory.

## Merge rules

Layer order is base, variant, then profiles. Profiles are peers and have no order among themselves. Nothing is overwritten silently: every merge is a safe union, a declared override or an error ([0023](decisions/0023-flat-profiles-and-layer-merge-rules.md)).

| Element | Identity | Rule |
|---|---|---|
| Skills, subagents, commands, rules, MCP servers | Name | One name, one owner. A same name in two layers is an error, unless the later layer declares `dotai: { overrides: true }`. Two profiles can never override each other. |
| Lists: permissions, hooks per event | Value | Union in layer order without duplicates; a later layer cannot remove or change an entry ([0040](decisions/0040-list-files-merged-as-a-union.md)). |
| Single values: `statusLine`, an `env` variable, the default model | Key | A later layer may replace a value only if it declares the override. The same key in two profiles is an error. |
| Root rule (root `CLAUDE.md` or `AGENTS.md`) | Unique | Only the base provides it, exactly once, and no layer overrides it. Profiles and variants add named, non-root rules. |

The name of an item comes from its path, not from its frontmatter: the file stem for rules, commands and subagents (`rules/web.md` is `web`), the directory for skills (`skills/seo/SKILL.md` is `seo`), and the key under `mcpServers` in `.rulesync/mcp.jsonc` for MCP servers. Names that differ only by case are a conflict, and so are staged paths that differ only by case, because some file systems cannot hold both. `overrides: true` must replace an item of an earlier layer in the same scope; with nothing to replace it is an error. MCP servers have no override syntax, so a server name in two layers of a scope is an error, `mcp-server-conflict` (or `profile-conflict` between two profiles), unless both layers define the server the same way: an identical definition by canonical JSON (keys sorted, insignificant whitespace and comments dropped, numbers compared by value) is accepted and kept once, as the first layer defines it. The servers of every layer are merged into one `mcp.jsonc` per scope.

`.rulesync/hooks.jsonc` and `.rulesync/permissions.jsonc` merge into one file per scope, in layer order, with the profiles by name ([0040](decisions/0040-list-files-merged-as-a-union.md)). Objects merge by key: hook events, permission categories, the patterns of a category, and tool-scoped blocks such as `claudecode.hooks`. The items of an array, such as the entries of a hook event, are appended, and an item identical to an earlier one by canonical JSON is kept once, at its first position. Any other value, such as a pattern's action (`allow`, `ask` or `deny`) or `version`, must be the same in every layer that sets it: a different value is `hooks-conflict` or `permissions-conflict`, since these files have no override syntax. A file that is not a JSONC object, or that repeats a key in one object, is `hooks-invalid` or `permissions-invalid`. A `deny` and an `allow` on different patterns are both kept, and rulesync and each harness apply `deny` over `ask` over `allow`.

The rules apply across scopes too, without overrides: an item in a profile with the same name as one in the base or the variant is always an error, because harnesses resolve that conflict differently ([0023](decisions/0023-flat-profiles-and-layer-merge-rules.md)). The one exception is an MCP server that a profile defines exactly as the user scope does, by canonical JSON: whichever a harness picks, it runs the same server, so both scopes keep it.

## The `dotai:` block

Frontmatter block for data rulesync cannot express. rulesync strips it from its output, so the engine reads it from the sources.

```yaml
---
name: reviewer
description: Reviews a diff for correctness bugs.
claudecode:
  model: sonnet
  effort: medium
antigravity-cli:
  model: flash
opencode:
  model: anthropic/claude-sonnet-4-5
dotai:
  capabilities: [git]
---
```

| Field | Meaning |
|---|---|
| `capabilities` | Abstract needs (for example `browser`, `git`) checked by `dotai doctor`. |
| `overrides` | `true` when this item deliberately replaces a same-name item from an earlier layer. |

Per-harness tuning goes in rulesync's per-target blocks, not in `dotai:`. The block name depends on the file type: subagents use `antigravity-cli:` for agy (rulesync 41.0.0 drops a subagent `antigravity:` block silently), while rules use `antigravity:`.

## Models

There are no model tiers ([0024](decisions/0024-models-declared-per-harness-no-tiers.md)). Each subagent declares its default model, and its effort where supported, in its per-target blocks, as in the example above. Use aliases (`haiku`, `sonnet`, `opus` in Claude Code; `inherit`, `flash`, `pro` in agy) rather than exact model ids. At runtime the orchestrator chooses the model for each delegation, and the pack's hook rejects a delegation without one.

## Settings

`dotai/settings.yaml` holds one block per harness, keyed by rulesync target name, merged as-is into that harness's settings file ([0025](decisions/0025-settings-as-per-harness-passthrough.md)). The engine does not interpret any setting. A layer that replaces a value from an earlier layer declares it in a top-level `dotai:` block, for example `dotai: { overrides: [claudecode.statusLine] }` ([0030](decisions/0030-tools-paths-and-project-config.md)).

`dotai validate` checks each file on its own. The top-level keys are harnesses listed in the manifest, in `harnesses` or `optional-harnesses`, plus the optional `dotai:`, and each harness block is a mapping whose keys contain no dot; a profile cannot carry an `antigravity-cli` block. Values must have a JSON form: no anchors or aliases, no keys that are not strings, no infinite or NaN numbers, no integers that do not fit 64 bits, and no null, because a layer cannot unset a key. `dotai:` holds only `overrides`, a list of `<harness>.<key>` entries, without duplicates, each naming a key the same file sets. Claude Code's `env` must be a mapping, because it merges per variable, and its override names one variable, `claudecode.env.<VAR>`, and no other nested path is allowed. Whether an override replaces a value of an earlier layer is checked when the layers merge. The same key in the user scope and the project scope is not a conflict: each scope writes its own settings file, and the harness decides which one wins.

```yaml
claudecode:
  model: sonnet
  statusLine: { type: command, command: "${DOTAI_TOOL:statusline}" }
antigravity-cli:
  statusLine: { type: command, command: "${DOTAI_TOOL:statusline}" }
opencode:
  model: anthropic/claude-sonnet-4-5
```

| Harness | User file | Project file |
|---|---|---|
| `claudecode` | `~/.claude/settings.json` | `.claude/settings.json` |
| `antigravity-cli` | `~/.gemini/antigravity-cli/settings.json` | none: profiles cannot carry agy settings |
| `opencode` | `~/.config/opencode/opencode.jsonc` | `opencode.jsonc` |

The merged settings are written after rulesync, which writes some of these files itself when the pack has hooks, permissions or MCP: `$schema`, `hooks` and `permissions` for Claude Code, `permissions` for agy, and `permissions`, `instructions` and `mcp` for OpenCode. dotai keeps rulesync's keys and appends its own; a top-level key written by both is a `settings-conflict` diagnostic, and nothing is staged.
