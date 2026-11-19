<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/images/logo-dark.svg">
    <img src="docs/images/logo-light.svg" alt="dotai" width="300">
  </picture>
</p>

# dotai

<!-- The release badge shows "no releases" until v0.1.0 is published. -->

[![CI](https://github.com/lartweib/dotai/actions/workflows/ci.yml/badge.svg)](https://github.com/lartweib/dotai/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/github/license/lartweib/dotai)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/lartweib/dotai.svg)](https://pkg.go.dev/github.com/lartweib/dotai)
[![Go version](https://img.shields.io/github/go-mod/go-version/lartweib/dotai)](go.mod)
[![Release](https://img.shields.io/github/v/release/lartweib/dotai)](https://github.com/lartweib/dotai/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/lartweib/dotai)](https://goreportcard.com/report/github.com/lartweib/dotai)

One pack of agent configuration, rendered into Claude Code, Antigravity CLI and OpenCode, with every write planned, backed up and reversible.

Each AI coding harness keeps its rules, agents, skills, hooks, MCP servers and settings in its own files and formats, so a setup you care about drifts apart across tools and machines, and trying a new one means hand-editing configuration you cannot easily undo. dotai keeps that setup in one place, a **pack**, and renders it into Claude Code, Google Antigravity CLI (agy) and OpenCode, with profiles per project type and variants per environment. Every write is planned first, shown before it happens, backed up, and undone with `dotai restore`: an install can run isolated from your own configuration, and a project's files stay out of git.

A pack is a `dotai.yaml` manifest and layers in the rulesync layout: a base, variants per environment and profiles per project type. Trimmed from [examples/minimal-pack](examples/minimal-pack/README.md):

```yaml
schema: dotai.pack/v1
name: minimal-pack
version: 0.1.0
rulesync: 43.9.2
harnesses: [claudecode, antigravity-cli, opencode]

profiles:
  web:
    path: profiles/web
    description: Web projects with a JavaScript toolchain.
    hints: [package.json]

variants:
  standalone:
    path: variants/standalone
    default: true
```

The base layer holds the shared rules, skills and subagents, such as `base/.rulesync/rules/overview.md` and `base/.rulesync/skills/commit-message/SKILL.md`. A dry run shows every file and key an install would write, and writes nothing but the rulesync cache (trimmed):

```text
$ dotai install examples/minimal-pack --mode isolated --dry-run
Install plan · minimal-pack 0.1.0 · variant standalone · mode isolated
root: ~/.dotai/isolated

claudecode · partial isolation · may need a new login
  CLAUDE_CONFIG_DIR=~/.dotai/isolated/claude

claudecode
  create     ~/.dotai/isolated/claude/CLAUDE.md
  create     ~/.dotai/isolated/claude/agents/reviewer.md
  create     ~/.dotai/isolated/claude/rules/standalone.md
  create     ~/.dotai/isolated/claude/settings.json
             keys: create $schema, create claudeMdExcludes, create hooks, create model, create permissions
  create     ~/.dotai/isolated/claude/skills/commit-message/SKILL.md
  ...

15 create, 0 replace, 0 remove, 0 unchanged · dry run, nothing was written
```

![The dotai web UI, Home board](docs/images/ui-home.png)

## Features

- **One source, three harnesses.** A pack in the [rulesync](https://github.com/dyoshikawa/rulesync) layout, rendered by a pinned rulesync binary whose checksums are embedded in the engine.
- **Plan first.** Every command that writes shows its exact file and key operations with `--dry-run`, and `--json` gives the same plan to scripts ([CLI JSON output](docs/CLI-JSON.md)).
- **Safe by default.** Isolated installs under `~/.dotai/isolated/`, or a replace mode that resets a harness to a fresh install; a backup before every write, a journal and a rollback on failure, and `dotai restore` to undo.
- **Project profiles that stay local.** `dotai apply --profile <p>` writes only to local, untracked files, lists them in `.git/info/exclude`, and `dotai restore --project` undoes it.
- **Packs from GitHub or a folder.** `dotai pack add`, `update` (which shows what changed in hooks, MCP servers, settings, tools and mods first), `remove`, `list`, and `dotai pack new` from an embedded template.
- **A local web UI.** `dotai ui` serves the same use cases on `127.0.0.1`, each action shown as its plan first.
- **`dotai doctor`** reports what changed outside dotai since it wrote it.

## Supported harnesses and platforms

| Harness | Pack id | Command | Isolated launch | Status |
|---|---|---|---|---|
| Claude Code | `claudecode` | `claude` | `CLAUDE_CONFIG_DIR` | Supported |
| Google Antigravity CLI (agy) | `antigravity-cli` | `agy` | `--gemini_dir` | Supported |
| OpenCode | `opencode` | `opencode` | `XDG_*` directories | Preview |

OpenCode is a preview, pending an upstream fix in rulesync's generated OpenCode hooks plugin: hooks that read stdin can hang, and hook timeouts are not enforced. An install never selects a preview harness by default: name it with `--harness`, or tick it in the web UI's harness step, and the plan warns `harness-preview`; the saved choice, or an earlier install of the pack that managed it, keeps it managed.

Isolation is partial for every harness: each install plan names what of your own setup still loads, such as a shared login. The verified behavior of each harness, with the version and OS it was checked on, is in [Harness notes](docs/HARNESS-NOTES.md).

| OS | Architectures |
|---|---|
| Windows | x64 (`amd64`) |
| Linux | x64 (`amd64`), arm64 |
| macOS | x64 (`amd64`), arm64 |

These are the platforms of the release archives and of the rulesync checksums the engine embeds ([0042](docs/decisions/0042-tag-driven-release-binaries.md)). CI runs on Windows, Linux and macOS. Adding or updating a pack from GitHub needs git 2.41 or later on `PATH`, and a project apply uses git to find the files a repository tracks.

## Install

> [!NOTE]
> No release is published yet. The one-line installers below work once the first release, `v0.1.0`, is published; until then, install with `go install`.

```sh
# Linux and macOS
curl -fsSL https://raw.githubusercontent.com/Lartweib/dotai/main/install.sh | sh
```

```powershell
# Windows, in PowerShell
irm https://raw.githubusercontent.com/Lartweib/dotai/main/install.ps1 | iex
```

```sh
# From source, with Go 1.26 or later
go install github.com/lartweib/dotai/cmd/dotai@latest
```

[Installing dotai](docs/INSTALL.md) covers what the scripts change on `PATH`, their options, reading a script before running it, and installing a release archive by hand with its checksum and attestation.

## Quickstart

Create a pack, check it, see what an install would do, install it isolated, launch a harness against it, and open the UI. The example uses Claude Code; use the harness you have instead (`claudecode` or `antigravity-cli`, or `opencode` as a preview), which must already be installed. The first install downloads the pinned rulesync once and verifies its checksum.

```sh
dotai pack new my-pack --name my-pack --harness claudecode --profile web:"Web projects"
dotai validate my-pack
dotai install my-pack --mode isolated --dry-run   # then again without --dry-run
dotai run claudecode
dotai ui
```

- `dotai pack new` writes dotai's template, one commented example of each file a pack holds, and adds the pack to the registry. Edit the examples: a file left as the template wrote it is flagged `template-unchanged`, and `install` asks before installing it.
- `dotai install --mode isolated` installs under `~/.dotai/isolated/`, never into your own harness configuration; `dotai restore` undoes it.
- `dotai run claudecode` launches Claude Code against the isolated install; pass the harness's own arguments after `--`.
- `dotai install` manages the pack's `harnesses` that are installed on your machine, or those `--harness` names. A harness listed in `requires` that is missing stops the install. A preview harness, such as OpenCode, is managed only when you choose it, as [Supported harnesses](#supported-harnesses-and-platforms) describes ([Pack format](docs/PACK-FORMAT.md#manifest)).

For a complete, small pack to read, see [examples/minimal-pack](examples/minimal-pack/README.md). [Creating a pack](docs/PACK-FORMAT.md#creating-a-pack) covers `dotai pack new` in full, and [Applying a pack](docs/PACK-FORMAT.md#applying-a-pack) the project profiles.

## Everyday commands

| Command | What it does |
|---|---|
| `dotai install [path] --mode isolated` | Install a pack's user scope under `~/.dotai/isolated/`; `--mode replace` resets each harness and installs into its own configuration instead. |
| `dotai apply --profile <p>` | Apply profiles to the project in the current directory, in local, untracked files only. |
| `dotai run <harness> [-- args]` | Launch a harness against its isolated install. |
| `dotai restore [backup-id]` | Undo the last install, or every install down to the one that took `backup-id`. |
| `dotai restore --project` | Undo the profiles applied to the project in the current directory. |
| `dotai doctor [--fix]` | Report what changed outside dotai since it wrote it; `--fix` writes missing `.git/info/exclude` entries. |
| `dotai pack add <github-url>` | Add a pack from GitHub at `--ref`, or a local one with `--dir <path>`. |
| `dotai pack update <identity>` | Fetch a GitHub pack again and show what changed in its executable content before replacing it. |
| `dotai pack new <dir> --name <n>` | Create a pack from the template embedded in the engine and add it to the registry. |
| `dotai ui` | Serve the local web UI on `127.0.0.1`. |
| `dotai validate [path]` | Check a pack's structure and merge rules. |

`install`, `apply` and `pack new` take `--dry-run`. `dotai <command> --help` describes each command in full.

## dotai and rulesync

dotai renders packs with [rulesync](https://github.com/dyoshikawa/rulesync), and a pack's layers use the rulesync layout. rulesync on its own generates each harness's files from one source. dotai adds:

- **Pinned rendering.** Each pack names its rulesync version; dotai downloads it once and checks it against checksums embedded in the engine. dotai also renders per-harness settings, tools and their path variables.
- **Plans.** Every file and settings key is shown before it is written, with `--dry-run` and `--json`.
- **Backups, a journal and restore.** A backup before every write, a journal and a rollback on failure, and `dotai restore` to undo an install.
- **Isolated installs.** An install under `~/.dotai/isolated/` that leaves your own harness configuration as it is, launched with `dotai run`.
- **Project profiles kept out of git.** Local, untracked files listed in `.git/info/exclude`, undone with `dotai restore --project`.
- **`dotai doctor`.** A report of what changed outside dotai since it wrote it.
- **A web UI.** The same use cases on `127.0.0.1`, each action shown as its plan first.

## Docs

- [Installing dotai](docs/INSTALL.md): the install scripts, their options, and installing a release by hand.
- [Architecture](docs/ARCHITECTURE.md): how the engine works and the code map.
- [Pack format](docs/PACK-FORMAT.md): the pack contract.
- [CLI JSON output](docs/CLI-JSON.md): every command, its `--json` data, and the web UI API.
- [Harness notes](docs/HARNESS-NOTES.md): verified harness behavior.
- [Decision records](docs/decisions/README.md): why the engine is built this way.
- [Roadmap](docs/ROADMAP.md): what is done and what comes next.
- [Changelog](CHANGELOG.md).

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) first, and follow the [Code of Conduct](CODE_OF_CONDUCT.md). Report security issues privately, as [SECURITY.md](SECURITY.md) describes.

## License

[MIT](LICENSE)
