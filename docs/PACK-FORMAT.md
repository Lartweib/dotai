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
  mods/
    panel/                 # a Claude Code plugin with a hooks module, named by profiles
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
    mods: [panel]
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

onboarding:                # optional: replaces dotai's onboarding
  custom: base/.rulesync/skills/setup
```

| Field | Required | Meaning |
|---|---|---|
| `schema` | yes | Format version. |
| `name`, `version` | yes | The pack's display name and version. A local pack's identity is `local/<name>`; a git pack's comes from its source ([Adding a pack](#adding-a-pack), [0044](decisions/0044-git-pack-distribution.md)). |
| `description`, `author` | no | What the pack is and who makes it, as strings. The `.claude-plugin/plugin.json` dotai writes for a mod carries them ([Mods](#mods)). |
| `engine` | yes | Range of engine versions the pack supports. |
| `rulesync` | yes | Exact rulesync version the pack was tested with. |
| `harnesses` | yes | Required targets, using rulesync target names: `claudecode`, `antigravity-cli`, `opencode`. An install where one of them is not installed, or is left out with `--harness`, stops before writing anything, naming it and why ([0038](decisions/0038-replace-mode-resets-harness-to-fresh-install.md)). |
| `optional-harnesses` | no | Targets the pack also supports, with the same names as `harnesses`; each one at most once and none of them also in `harnesses` (`harness-duplicate`), and an unknown name is `harness-unknown`. An install skips one that is not installed, with a notice ([0038](decisions/0038-replace-mode-resets-harness-to-fresh-install.md)). `dotai/settings.yaml` may hold a block for it, and `stage` renders it with the required ones. |
| `profiles.<name>.path`, `variants.<name>.path` | yes | Layer directory, relative to the pack root. |
| `profiles.<name>.description` | yes | What the profile is for. The profiling skill lists it ([Onboarding](#onboarding)). |
| `profiles.<name>.hints` | no | Globs of repository-relative paths: evidence for the profiling skill, never proof. The engine never picks a profile by them; the session-start hook uses them only to tell when to speak ([Onboarding](#onboarding)). Each is matched as Go's `path.Match` reads it, against the whole path from the top of the repository, so `package.json` is the top one only and `*` stays within one directory; `dotai validate` warns `profile-hint-invalid` about a pattern `path.Match` refuses, which matches nothing, and about one with `**`, which `path.Match` reads as `*` within one segment. |
| `profiles.<name>.mods` | no | The mods of the pack's `mods/` the profile installs in a project ([Mods](#mods)). Two profiles may name the same mod. Only profiles take it: a variant that names mods is `mod-scope` ([0011](decisions/0011-mods-always-project-scoped.md)). |
| `variants.<name>.default` | no | Variant used when nothing is chosen or detected. Exactly one variant sets it when the pack has variants. |
| `variants.<name>.detect` | no | Mechanical rule that preselects the variant. `command`: preselect when that command is on `PATH`. |
| `variants.<name>.shared-skills` | no | `true` makes the user's shared skills folder, `~/.agents/skills`, where other tools install skills, visible in the isolated install of this variant ([0036](decisions/0036-shared-skills-per-variant.md)). Default `false`: only the pack's content is visible. Only variants take it. |
| `onboarding.custom` | no | A subagent file under `<layer>/.rulesync/subagents/` or a skill directory under `<layer>/.rulesync/skills/`, relative to the pack root, where `<layer>` is `base/` or a variant's directory, that replaces dotai's onboarding: dotai then generates neither the profiling skill `dotai-profiles` nor its session-start hook ([Scopes](#scopes), [0045](decisions/0045-dotai-owned-project-onboarding.md)). A missing path, one elsewhere, or an item named `dotai-profiles`, is `onboarding-invalid`. In a variant, the onboarding comes only with that variant. `validate` warns `onboarding-replaced`: dotai's onboarding proposes profiles with the user's approval, and a replacement is at the pack author's own risk. Without it, a skill or subagent named `dotai-profiles` is `onboarding-name-reserved`, and a subagent named `onboarding`, as packs shipped before, is the warning `onboarding-duplicate`: remove it, or declare it as `custom`. |

`dotai validate --json` lists each profile with its `path`, `description`, `hints` and `mods` in `pack.profiles`, in declaration order, so a tool can read them without parsing the manifest ([CLI-JSON](CLI-JSON.md)).

`validate --json` gives the onboarding in `pack.onboarding` ([CLI-JSON](CLI-JSON.md)).

`base/` is implicit and always applied.

## Scopes

| Layers | Scope | When |
|---|---|---|
| `base` and the chosen variant | User level, in each harness | Once per machine (`dotai install`) |
| Profiles | Project level, in local files excluded from git (`.claude/`, `.agents/`, `.opencode/`, [0041](decisions/0041-project-scope-local-and-untracked.md)) | Once per project (`dotai apply --profile ...`; so far `--dry-run` prints its plan) |

Profiles are flat and combinable: a project has zero or more of them (for example `--profile web,astro-cloudflare`). There are no sub-profiles. A stack-specific profile exists only when the stack repeats across projects; what is specific to one project goes in that project's own rules.

Profiles are proposed by dotai's profiling skill after it analyzes the project and asks the user ([0008](decisions/0008-onboarding-by-detection-and-base-profile.md), [0045](decisions/0045-dotai-owned-project-onboarding.md)), or chosen by hand. The engine never picks one. The project records its profiles, and the onboarding record its fingerprint, so later sessions do not ask again. The procedure is the engine's, not pack content: a pack gives each profile a `description` and `hints`, which the skill lists, and may replace the onboarding with `onboarding.custom` ([Onboarding](#onboarding)). Before 0045, the example pack shipped an onboarding subagent for packs to copy; it is gone, and the web UI shows the skill dotai generates for the selected pack, or its custom item, instead.

## Onboarding

Decided in [0045](decisions/0045-dotai-owned-project-onboarding.md). `dotai install` adds an engine layer to the user scope, staged after the base and the variant, with two items, unless the manifest declares `onboarding.custom`:

- **The profiling skill `dotai-profiles`**, at `.rulesync/skills/dotai-profiles/SKILL.md` of the user staging, which rulesync renders for every harness. Its description, the only part always in the context, is fixed and under 60 tokens; its body is the fixed procedure (use only the pack's profiles; in an empty repository, ask what the project will be; otherwise detect the stack by content, `hints` being evidence only, and ask only what is left open; show `dotai apply --profile <a,b> --dry-run`, run `dotai apply --profile <a,b>` only after the user approves, `--yes` only for what the plan asks to confirm and the user approved, its conflicts or the template files it installs unchanged; never commit), then each profile of the manifest in declaration order with its name, `description`, `hints` and `mods`. It is generated again on every install, so `description` and `hints` are all the skill knows about a profile.
- **A `SessionStart` hook** in the tool-scoped block of each harness the install renders, `claudecode.hooks.sessionStart`, `antigravity-cli.hooks.sessionStart` and `opencode.hooks.sessionStart` of the merged `.rulesync/hooks.jsonc`. Its command is the absolute path of the installing dotai binary, or of the `dotai` on PATH when that is the very same file, as a package manager's link is, whose path outlives an upgrade, then `onboarding hook --harness <id>`: in double quotes with forward slashes for Claude Code and OpenCode, which run it through a shell, and bare in the OS's own separators for agy, which runs it through `cmd /c` ([HARNESS-NOTES](HARNESS-NOTES.md#hook-commands)). Claude Code's entry has the matcher `startup`, so it runs at a new session only. A block that declares `sessionStart` already gets the entry appended; one that does not gets the merged shared `sessionStart` hooks first, since the block replaces them for its harness, so the pack's own hooks still reach it, and no `hooks-shadowed` is reported for it. A merged hooks file without shared hooks gets an empty `hooks` object, which rulesync 41 requires. A harness whose command cannot hold the binary's path, such as agy with a path that has whitespace or a character of `& | < > ^ ( ) ; * ? [ ] { } !` or a quote, gets the skill without the hook, and the install plan warns `onboarding-hook-skipped`; the install does not fail.

**The session-start hook**, `dotai onboarding hook --harness <id>`, prints one line, of at most 40 tokens, only while the repository is neither set up nor dismissed, or when its profiles may no longer fit; otherwise it adds nothing to the context ([CLI-JSON](CLI-JSON.md#commands)). It compares a **fingerprint** of the repository, a digest of (a) the installed pack's identity and resolved revision, the commit of a git pack or the manifest `version` of a local one, (b) the names of the manifest's profiles, and (c) the sorted names of the profiles with at least one `hints` glob matching a path git's index holds (`git ls-files`), matched against the whole repository-relative path, with the one `dotai apply` stores in `.dotai/onboarding.json`, in a git repository only. Outside a git worktree, which it finds without running git, by the first `.git` directory or file from the current directory up, in the home directory, at a drive root or in a bare repository, it is silent; it runs one `git ls-files` at most, and only when the answer depends on the index. Any error, or 1 s without an answer, after which it stops that git and waits for it to exit, is silence too: the hook never blocks a session. `.dotai/onboarding.json` is engine-written; like the marker it is never trusted for ownership, only to decide whether to print the line. For agy the line is an `injectSteps` object; Claude Code and OpenCode take it as plain text, though OpenCode's plugin, as rulesync 41 renders it, does not pass a hook's output to the model ([HARNESS-NOTES](HARNESS-NOTES.md#rulesync-4100)), so OpenCode support is provisional. agy and OpenCode have no `startup`-only filter, so a resumed or compacted session may repeat the line.

**Dismissal.** When the user wants no profiles in a repository, or keeps the profiles applied after the skill reported a mismatch, the skill runs `dotai onboarding dismiss`, which stores the fingerprint in `.dotai/onboarding.json` with the `source` `dismiss`. Without a marker, the hook then stays silent while the pack's identity and the hinted profiles are the ones the dismissal stored; with one, while the fingerprint is the same. In a repository where no apply ran, the dismissal creates `.dotai/` and its `.git/info/exclude` entry and records them in the project record, so that `dotai restore --project` removes them, and `dotai doctor` checks the onboarding record as it checks the marker ([CLI-JSON](CLI-JSON.md#doctor-findings)).

`stage` adds the same layer, with the path of the binary that runs it. With `onboarding.custom`, dotai stages neither item, and the install plan warns `onboarding-replaced`, naming the pack and the item, in the command line and the web UI before the user confirms; `--yes` still prints it, and `install --json` reports it in `warnings` ([CLI-JSON](CLI-JSON.md)). A custom item in a variant comes only with that variant.

## Creating a pack

Decided in [0046](decisions/0046-pack-templates-and-wizard.md). dotai embeds one pack template, versioned with the engine: a minimal, commented and harmless example of each file and folder a pack holds, so that a new pack is valid from the start. Then the pack is the author's to write.

| Path | Example |
|---|---|
| `dotai.yaml` | The manifest, with one profile, its `description`, `hints` and `mods`, and a default variant |
| `base/.rulesync/rules/overview.md` | The root rule |
| `base/.rulesync/rules/example-rule.md`, `commands/example-command.md`, `subagents/example-agent.md`, `skills/example-skill/SKILL.md` | A rule, a command, a subagent and a skill |
| `base/.rulesync/mcp.jsonc` | MCP servers: the example server is a comment, so none runs |
| `base/.rulesync/hooks.jsonc` | A `stop` hook that runs `echo` |
| `base/.rulesync/permissions.jsonc`, `base/dotai/settings.yaml` | A permission and a Claude Code `env` setting |
| `base/dotai/tools/example-tool.sh`, `.ps1` | A tool, once per OS |
| `variants/default/`, `profiles/<profile>/`, `mods/<mod>/` | A rule of the default variant, a rule of each profile, and a Claude Code plugin with a hooks module for each mod a profile names |
| `README.md` | The layout, at the top, where dotai reads only `dotai.yaml` |

It has no `dotai-profiles` item and no `onboarding` subagent: dotai generates the onboarding ([Onboarding](#onboarding)).

**Unchanged example files.** The engine keeps the SHA-256 of every file below the top of the pack that each released version of the template writes, with line endings normalized, so that a file checked out with CRLF still matches; a new version of the template appends its hashes. A file of a pack, below its top and outside `.git`, whose content is one of them is `template-unchanged`: `dotai validate` warns about each one, and `dotai pack add` warns, listing them. Edit each example for the pack, or delete it; any change clears the warning.

The plans of `dotai install` and `dotai apply` warn `template-unchanged` about each such file of the layers they install, the base and the variant for an install, the profiles and the mods they name for an apply, and carrying such a plan out needs the consent those commands already ask for: once on a terminal, after the plan; refused without a terminal or with `--json` unless `--yes`, which accepts what the plan asks to confirm; and, in the web UI, the plan's digest with `yes`. An isolated install, which otherwise never asks, asks while the pack keeps an example file of its user scope ([CLI-JSON](CLI-JSON.md#commands)). A published pack that kept the examples is flagged as well.

## Adding a pack

`dotai pack add` adds a pack to the registry, `~/.dotai/packs.json`, which the web UI shows too ([0044](decisions/0044-git-pack-distribution.md)). A pack comes from a GitHub repository, fetched with the git on `PATH` (2.41 or later) into `~/.dotai/packs/github.com/<owner>/<repo>`, or from a local directory, added with `--dir`, which stays where it is. A git pack's identity is `github.com/<owner>/<repo>` in lower case, and its manifest `name` is only a label; a local pack's is `local/<name>`, and a new local name follows the rule for mod names. A fetched pack never runs its hooks, its `.gitattributes` has no effect, so the files are the committed bytes, and its tree is checked before it is validated: no symbolic link, no submodule, no `.git` path, at most 10000 files and 64 MiB. `dotai pack update` shows what changed in executable content, every layer's `.rulesync/hooks.jsonc`, `.rulesync/mcp.jsonc` and `dotai/settings.yaml`, `dotai/tools/` and `mods/`, and also `.rulesync/permissions.jsonc`, the scripts of skills, commands and subagents, `dotai.yaml` and every executable file, before it replaces the clone. See the `pack` commands in [CLI JSON output](CLI-JSON.md#commands).

## Applying a pack

For each scope:

1. Resolve the layers. User scope: `base` and the variant passed with `--variant`, detected, or default. Project scope: the profiles passed with `--profile`.
2. Read the `dotai:` blocks from the sources.
3. Run `dotai validate` over all layers, both scopes included. Structural checks (manifest, layer paths, file types, `dotai:` blocks) run first. The [merge rules](#merge-rules) run only when they found no errors, and their diagnostics come after the structural ones.
4. Merge the scope's layers into its own staging tree.
5. Run the pinned rulesync over the staging `.rulesync/`, writing into staging.
6. Render `dotai/` content for each harness.
7. Apply the staging output to the harnesses with the active install mode and backup ([0018](decisions/0018-install-modes-and-mandatory-backup.md)). For the project scope, `dotai apply --profile` writes each harness's local targets and keeps its record under the home directory, in `~/.dotai/projects/<project id>/record.json`; `.dotai/state.json` in the project is only a marker of the pack, variant and profiles applied, which the engine never trusts for ownership ([0041](decisions/0041-project-scope-local-and-untracked.md)); [0045](decisions/0045-dotai-owned-project-onboarding.md) trusts it, and `.dotai/onboarding.json`, only to decide whether its session-start hook prints one line. `.dotai/packs/<pack>/` is the pack's own, for files such as the marker of its onboarding: apply never writes or reads it, and `dotai restore --project` removes it with the rest of `.dotai/`, into its backup.

## Tools and project config

Decided in [0030](decisions/0030-tools-paths-and-project-config.md).

- `dotai/tools/` installs to `~/.dotai/tools/` at user scope and to `.dotai/tools/` at project scope, keeping the executable bit where the file system has one. The same path in two layers of a scope is an error (`tool-conflict`); a layer that adjusts another layer's tool config ships a file with a different name.
- While staging, `${DOTAI_TOOLS}` and `${DOTAI_PROJECT_TOOLS}` become the absolute paths of those directories in every text file of the layers, skills and hooks included, before rulesync runs, and `${DOTAI_HOME}` becomes the absolute path of the user's real home directory, in both install modes: in the isolated mode it is the home directory, not the harness's isolated directory under `~/.dotai/isolated/`. A file that is not UTF-8, or that holds a NUL byte, is copied as-is, and no other `${...}` is touched.
- dotai names the home directory `${DOTAI_HOME}` rather than `${HOME}` so that `$HOME` and `${HOME}` reach a hook command or a script literally, for the shell that runs it to expand; dotai never rewrites them.
- `${DOTAI_HOME}` is an absolute path, and it may hold spaces, as in `C:/Users/Ana Lopez`: put it in double quotes in a hook command or a shell setting (`cat "${DOTAI_HOME}/notes.md"`). A home directory with a character some harness's shell does not take literally inside double quotes (`"`, `$` or `` ` ``, and `%` on Windows or `\` elsewhere), or a network path on Windows, fails the stage, naming the variable and the character, as a tools directory does for a tool command. In a profile, the project scope, it writes the user's absolute home directory into the files installed in the project, which are local and excluded from git ([0041](decisions/0041-project-scope-local-and-untracked.md)) but name the user's account.
- The paths are written with forward slashes on every platform, Windows included: Windows accepts them, and a backslash inside a JSON string, such as a hook command in `.rulesync/hooks.jsonc`, would be an invalid escape.
- A settings value runs a tool with `${DOTAI_TOOL:<name>}` ([0037](decisions/0037-tool-commands-resolved-per-os.md)): the name is the tool's file name without an extension, and the variable becomes the command for the OS of the install, with the script's absolute path in the tools directory of the settings' scope: `powershell.exe -NoProfile -ExecutionPolicy Bypass -File <path>\<name>.ps1` on Windows, which opens no window, and `sh <path>/<name>.sh` on Linux and macOS. Arguments may follow it. For Claude Code, which runs the command through a shell, the path is in double quotes, so spaces work. agy runs the command without a shell and passes quotes on, so for agy the path is bare, and a tools directory with whitespace, a quote or a character that a bare path cannot hold (next item) fails the dry run and the install, naming agy and the directory: such a home directory cannot run a tool from agy's settings. The layers of that scope must ship both `dotai/tools/<name>.sh` and `dotai/tools/<name>.ps1`: a missing one is `tool-missing`, and a name that is not letters, digits, `.`, `_` and `-`, or a variable without its closing brace, is `tool-invalid`. A tools directory whose path holds `"`, `$` or `` ` ``, or `%` on Windows or `\` elsewhere, fails the stage for every harness, since some shell would not read it literally inside the quotes, and so does one on Windows that starts with `\\` or `//`, such as a network path, which bash reads as a single `\` inside the quotes. The variable is expanded in settings values and in hook commands (next item); any other file keeps it as written. Do not name a script by its path in a command a harness runs: on Windows, a bare `.sh` runs through the file association, in a new window.
- A hook runs a tool with the same variable in its `command`, in the shared `hooks` of `.rulesync/hooks.jsonc` or in a tool-scoped block such as `claudecode.hooks`, and the scope's tools need both scripts, as for a setting (`tool-missing`, `tool-invalid`). Each harness runs hook commands its own way ([HARNESS-NOTES](HARNESS-NOTES.md#hook-commands)): Claude Code through bash (Git Bash on Windows) and OpenCode through Bun Shell, where a path in double quotes may hold spaces, and agy through `cmd /c`, which gets every `"` as `\"`, so a path for agy cannot be quoted. The command is written for the harnesses that read the hook:
  - a hook of `claudecode.hooks` or `opencode.hooks` has the path in double quotes, as a setting of that harness does;
  - a hook of `antigravity-cli.hooks` has the path bare, with forward slashes;
  - a shared hook reaches every harness the install renders whose own block does not declare the hook's event (rulesync replaces it for those), so its path is bare, with forward slashes, which bash, Bun Shell and `cmd /c` all run, when agy is one of them, and in double quotes otherwise; a shared hook that no rendered harness gets keeps the variable;
  - a hook of a block whose harness the install does not render keeps the variable as written, since rulesync does not render that block.

  A bare path, in a hook or in agy's settings, cannot hold whitespace, a quote, or any of `& | < > ^ ( ) ; * ? [ ] { } !`, which `cmd.exe` or a shell would read as an operator or a pattern: a hook that agy reads from such a tools directory fails the stage, the dry run and the install with `hook-path-unquotable`, at the `hooks.jsonc` of the hook's layer, naming the hook and the harness, before anything is written. `validate` does not know the home directory, so it cannot report it. The characters refused inside double quotes, and network paths, fail as for a setting.
- agy reads a `SessionStart` hook's standard output as JSON and drops plain text (checked on agy 1.4.12, [HARNESS-NOTES](HARNESS-NOTES.md#hook-commands)): a hook that gives agy context must print an `injectSteps` object, such as `{"injectSteps":[{"systemMessage":{"systemMessage":"<text>"}}]}`. How Claude Code and OpenCode read that JSON was not checked, so a hook that gives agy context belongs in `antigravity-cli.hooks.sessionStart`; that block then replaces the shared `sessionStart` hooks for agy (`hooks-shadowed`, under [Merge rules](#merge-rules)), so repeat there any shared one agy still needs.
- `.dotai/config.yaml` in a project belongs to the user and the pack; the engine never writes it. Tools find it by walking up from the current directory to the nearest `.dotai/`. The engine writes a marker of the applied profiles in the same directory, `.dotai/state.json`, and keeps its own record under the home directory; `dotai restore --project` removes `.dotai/`, `config.yaml` included, into its backup. `.dotai/` stays local: apply lists it in `.git/info/exclude` and never commits it ([0041](decisions/0041-project-scope-local-and-untracked.md)).

## Mods

A mod is a Claude Code plugin with a hooks module, a JavaScript or TypeScript module that hooks into the session ([0027](decisions/0027-loose-files-and-plugins-only-for-mods.md)). Mods live at the pack root, one directory each, `mods/<mod>/`, and a profile names the ones it uses in `profiles.<name>.mods`. They are always project-scoped ([0011](decisions/0011-mods-always-project-scoped.md)): the base and the variants carry none.

```
mods/
  panel/
    hooks/
      hooks.json           # {"modules": ["./register.js"]}, and optional settings-style "hooks"
      register.js          # export function register(on, options) { ... }
    agents/ skills/ commands/   # optional, as in any Claude Code plugin
    .claude-plugin/plugin.json  # optional; dotai writes one when it is missing
```

The directory is the pack author's own plugin tree, and a project apply installs it as it is, for Claude Code only, at `<project>/.claude/skills/<mod>/` ([0041](decisions/0041-project-scope-local-and-untracked.md)); rulesync does not render it. When the mod ships no `.claude-plugin/plugin.json`, dotai writes one with `name`, the mod's name, `version` and `description`, the pack's (or `The mod <mod> of the pack <pack>.` when the manifest has no description), and `author`, `{"name": "<author>"}`, when the manifest has one; `claude plugin validate` warns about a manifest without a description or an author. The mod's files are copied byte for byte: unlike a layer's, their path variables (`${DOTAI_TOOLS}`, `${DOTAI_PROJECT_TOOLS}`) are not expanded. A mod that two applied profiles name is installed once.

`dotai validate` checks every directory of `mods/`:

- The name is lower-case letters, digits and single hyphens, at most 64 characters, and no Windows device name such as `con` or `lpt1`, so that it is safe as a folder and as a plugin name (`mod-name-invalid`); a profile that names a missing mod is `mod-unknown`, and one that names a mod twice `mod-duplicate`.
- Every file path in the mod is one that each OS can store, with the rules of a staged path: no backslash, control character, `:<>"|?*` or Windows device name in a segment (`mod-path-invalid`), and no two paths that differ only by case, which a file system that ignores case holds as one file (`mod-path-case-conflict`, with `related` at the first).
- `hooks/hooks.json` exists and is a JSON object with `modules`, a non-empty list of paths, and `hooks`, when present, an object of hook events (`mod-hooks-invalid`).
- Each module is a relative slash path, resolved from `hooks/`, that stays inside the mod, ends in `.js`, `.mjs`, `.cjs`, `.jsx`, `.ts`, `.mts`, `.cts` or `.tsx` (Claude Code loads it with no build step) and is a file of the mod (`mod-module-invalid`).
- A `.claude-plugin/plugin.json` it ships is a JSON object whose `name`, when set, is the mod's name (`mod-manifest-invalid`).
- A mod with the name of a skill of the project scope is `mod-conflict`: both would install to `.claude/skills/<name>/`.
- Links, files that are not regular and files over 1 MiB are refused as in a layer.

Inside a plugin, Claude Code names the mod's items after it: skills `/<mod>:<skill>`, agents `<mod>:<agent>`, MCP servers `plugin:<mod>:<server>`. A `SubagentStart` or `SubagentStop` matcher of the mod's `hooks` that names one of its own agents bare never matches, so `validate` warns about it with `mod-matcher-bare`; match `^<mod>:<agent>$` instead.

A mod loads only in Claude Code 2.1.287 or later, run in a terminal, in a session started at the project's top, after the user accepts the workspace trust dialog ([HARNESS-NOTES](HARNESS-NOTES.md#mods)). agy and OpenCode have no mods: their plans warn `no-local-slot`. A pack author can check a mod with Claude Code itself, `claude plugin validate <dir>`, run on the installed `.claude/skills/<mod>/` or on a copy of `mods/<mod>/` with its plugin manifest; it reports the module's hooks and the calls it makes, and what Claude Code would refuse.

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

In the merged `hooks.jsonc`, a tool-scoped block that declares an event, such as `claudecode.hooks.sessionStart`, replaces the shared `hooks.sessionStart` for that harness: rulesync 41 renders only the block's hooks of that event for it, and keeps the shared hooks of the other events ([HARNESS-NOTES](HARNESS-NOTES.md#rulesync-4100)). This is no error, since a pack may mean it, but `validate` warns about each such event whose shared hooks are not empty with `hooks-shadowed`, at the event in the block and pointing at the event in the shared hooks, which may come from another layer of the scope. To keep both, repeat the shared entries in the block.

The rules apply across scopes too, without overrides: an item in a profile with the same name as one in the base or the variant is always an error, because harnesses resolve that conflict differently ([0023](decisions/0023-flat-profiles-and-layer-merge-rules.md)). The one exception is an MCP server that a profile defines exactly as the user scope does, by canonical JSON: it is staged at user level only and dropped from the project scope, so there is one source: a harness that lets a project server take precedence over a user one, as Claude Code does, never runs a copy that goes stale when the user scope changes.

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

The project files are where staging writes them, as rulesync does. A project apply never writes those, which are the team's: it installs `.claude/settings.json` into `.claude/settings.local.json` and `opencode.jsonc` into `.opencode/opencode.json`, merged by key, and leaves a file git tracks untouched, naming each key it skips ([0041](decisions/0041-project-scope-local-and-untracked.md), [CLI-JSON](CLI-JSON.md)).

The merged settings are written after rulesync, which writes some of these files itself when the pack has hooks, permissions or MCP: `$schema`, `hooks` and `permissions` for Claude Code, `permissions` for agy, and `permissions`, `instructions` and `mcp` for OpenCode. dotai keeps rulesync's keys and appends its own; a top-level key written by both is a `settings-conflict` diagnostic, and nothing is staged.
