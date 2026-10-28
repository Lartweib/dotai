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
| `dotai/tools/`, `dotai/runners/` | dotai | Scripts and binaries the layer needs |

A layer may omit any of these paths. A pack contains no symbolic links, only regular files and directories, and every file the engine reads is at most 1 MiB.

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
```

| Field | Required | Meaning |
|---|---|---|
| `schema` | yes | Format version. |
| `name`, `version` | yes | Pack identity. |
| `engine` | yes | Range of engine versions the pack supports. |
| `rulesync` | yes | Exact rulesync version the pack was tested with. |
| `harnesses` | yes | Targets, using rulesync target names: `claudecode`, `antigravity-cli`, `opencode`. |
| `profiles.<name>.path`, `variants.<name>.path` | yes | Layer directory, relative to the pack root. |
| `profiles.<name>.description` | yes | What the profile is for. Read by the onboarding agent. |
| `profiles.<name>.hints` | no | Evidence for the onboarding agent. The engine never acts on them. |
| `variants.<name>.default` | no | Variant used when nothing is chosen or detected. Exactly one variant sets it when the pack has variants. |
| `variants.<name>.detect` | no | Mechanical rule that preselects the variant. `command`: preselect when that command is on `PATH`. |

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

## Merge rules

Layer order is base, variant, then profiles. Profiles are peers and have no order among themselves. Nothing is overwritten silently: every merge is a safe union, a declared override or an error ([0023](decisions/0023-flat-profiles-and-layer-merge-rules.md)).

| Element | Identity | Rule |
|---|---|---|
| Skills, subagents, commands, rules, MCP servers | Name | One name, one owner. A same name in two layers is an error, unless the later layer declares `dotai: { overrides: true }`. Two profiles can never override each other. |
| Lists: permissions, hooks per event | Value | Union without duplicates. If an entry is both allowed and denied, deny wins. |
| Single values: `statusLine`, an `env` variable, the default model | Key | A later layer may replace a value only if it declares the override. The same key in two profiles is an error. |
| Root rule (root `CLAUDE.md` or `AGENTS.md`) | Unique | Only the base provides it. Profiles and variants add named, non-root rules. |

The rules apply across scopes too, without overrides: an item in a profile with the same name as one in the base or the variant is always an error, because harnesses resolve that conflict differently ([0023](decisions/0023-flat-profiles-and-layer-merge-rules.md)).

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

`dotai/settings.yaml` holds one block per harness, keyed by rulesync target name, merged as-is into that harness's settings file ([0025](decisions/0025-settings-as-per-harness-passthrough.md)). The engine does not interpret any setting; it only expands its own variables, such as `${DOTAI_TOOLS}`.

```yaml
claudecode:
  model: sonnet
  statusLine: { type: command, command: "${DOTAI_TOOLS}/statusline" }
antigravity-cli:
  statusLine: { type: command, command: "${DOTAI_TOOLS}/statusline" }
opencode:
  model: anthropic/claude-sonnet-4-5
```

| Harness | User file | Project file |
|---|---|---|
| `claudecode` | `~/.claude/settings.json` | `.claude/settings.json` |
| `antigravity-cli` | `~/.gemini/antigravity-cli/settings.json` | none: profiles cannot carry agy settings |
| `opencode` | `~/.config/opencode/opencode.json(c)` | `opencode.json(c)` |
