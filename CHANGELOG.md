# Changelog

Release notes for each version are generated per tag and published with its release on [GitHub Releases](https://github.com/lartweib/dotai/releases), built by the release workflow ([0042](docs/decisions/0042-tag-driven-release-binaries.md)). This file summarizes what is on `main` and not yet in a release. Versions follow [Semantic Versioning](https://semver.org/).

## Unreleased

The first release, `v0.1.0`, will contain:

- **Packs.** `dotai validate` and `dotai stage` load a pack (`dotai.yaml`, a base, variants and profiles in the rulesync layout), apply the layer merge rules and report diagnostics with stable codes.
- **Rendering.** A pinned rulesync binary, 43.10.0, verified against checksums embedded in the engine, renders rules, commands, subagents, skills, MCP, hooks and permissions; dotai adds per-harness settings, tools and their path variables.
- **Install.** `dotai install` installs a pack's user scope for Claude Code, agy and OpenCode (preview), isolated under `~/.dotai/isolated/` or in replace mode, with harness detection, a backup before every write, a journal and a rollback; `dotai run <harness>` launches a harness against its isolated install. Claude Code's user-scope MCP servers go into its `.claude.json`, isolated or `~/.claude.json`, owned server by server, so that the rest of that file stays ([0049](docs/decisions/0049-claude-user-mcp-servers-in-claude-json.md)). A replace install keeps each harness's login, history, caches and runtime state, Claude Code's running and background sessions and agy's prompt history included, and wipes vendor trees, `node_modules` and Claude Code's plugin marketplaces and cache, without refusing on the file names of published code inside them ([0038](docs/decisions/0038-replace-mode-resets-harness-to-fresh-install.md)). It also keeps the quota snapshots a statusline writes, `rate_limits.json` of Claude Code and of agy. Without `--json`, an install and a restore report their progress on stderr, a line per phase with a running count (`wiping 12/69 ~/.claude/plugins`), rewritten in place on a terminal; and on Windows an install, apply or restore that stops because another program holds a path says so, and how to find the program with Resource Monitor.
- **Coexisting hooks.** Hooks that other programs register in Claude Code's `settings.json` or in agy's `hooks.json` after an install are kept by later installs, updates and restores, and are not reported by `dotai doctor`: dotai owns its own hook entries, not those files ([0050](docs/decisions/0050-hook-entries-owned-one-by-one.md)).
- **Restore.** `dotai restore` undoes installs from the backups, newest first, and `dotai restore --project` undoes a project apply.
- **Project scope.** `dotai apply --profile <p>` applies profiles to a project in local, untracked files only, listed in `.git/info/exclude`, with mods as Claude Code plugins.
- **Doctor.** `dotai doctor` reports what changed outside dotai since it wrote it, with stable finding codes, and `--fix` writes missing exclude entries.
- **Pack registry.** `dotai pack add`, `update`, `remove`, `list` and `copy`, with packs from github.com fetched by the user's git or from a local directory; an update shows what changed in the pack's executable content first.
- **Template and wizard.** `dotai pack new` creates a pack from a template embedded in the engine; files left as the template wrote them need consent to install.
- **Pack badge.** A "dotai pack" badge for a pack's README, which the template's README starts with ([PACK-FORMAT](docs/PACK-FORMAT.md#badge)).
- **Onboarding.** `dotai install` generates a profiling skill and a session-start hook, so that a session in a new repository proposes profiles for the user to approve.
- **Web UI.** `dotai ui` serves the same use cases on `127.0.0.1`, with every action shown as its plan first. Its favicon is the dotai logo mark.
- **JSON output.** Every command takes `--json` and prints a versioned envelope ([CLI-JSON.md](docs/CLI-JSON.md)).
- **Releases.** Tag-driven binaries for Windows, Linux and macOS, with `SHA256SUMS`.
- **Third-party notices.** Every release archive includes `THIRD_PARTY_NOTICES`, with the license texts of the Go standard library, of each module linked into the binary and of the web UI's fonts.
- **Install scripts.** `install.sh` and `install.ps1` install a release in one command, after verifying its checksum against `SHA256SUMS`, without sudo, and put its directory on `PATH` through the shell profile or the user `Path` unless `DOTAI_NO_MODIFY_PATH=1` or `--no-modify-path` is given.
