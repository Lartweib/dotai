# Changelog

Release notes for each version are generated per tag and published with its release on [GitHub Releases](https://github.com/lartweib/dotai/releases), built by the release workflow ([0042](docs/decisions/0042-tag-driven-release-binaries.md)). This file summarizes what is on `main` and not yet in a release. Versions follow [Semantic Versioning](https://semver.org/).

## Unreleased

The first release, `v0.1.0`, will contain:

- **Packs.** `dotai validate` and `dotai stage` load a pack (`dotai.yaml`, a base, variants and profiles in the rulesync layout), apply the layer merge rules and report diagnostics with stable codes.
- **Rendering.** A pinned rulesync binary, 43.9.2, verified against checksums embedded in the engine, renders rules, commands, subagents, skills, MCP, hooks and permissions; dotai adds per-harness settings, tools and their path variables.
- **Install.** `dotai install` installs a pack's user scope for Claude Code, agy and OpenCode (preview), isolated under `~/.dotai/isolated/` or in replace mode, with harness detection, a backup before every write, a journal and a rollback; `dotai run <harness>` launches a harness against its isolated install.
- **Restore.** `dotai restore` undoes installs from the backups, newest first, and `dotai restore --project` undoes a project apply.
- **Project scope.** `dotai apply --profile <p>` applies profiles to a project in local, untracked files only, listed in `.git/info/exclude`, with mods as Claude Code plugins.
- **Doctor.** `dotai doctor` reports what changed outside dotai since it wrote it, with stable finding codes, and `--fix` writes missing exclude entries.
- **Pack registry.** `dotai pack add`, `update`, `remove`, `list` and `copy`, with packs from github.com fetched by the user's git or from a local directory; an update shows what changed in the pack's executable content first.
- **Template and wizard.** `dotai pack new` creates a pack from a template embedded in the engine; files left as the template wrote them need consent to install.
- **Onboarding.** `dotai install` generates a profiling skill and a session-start hook, so that a session in a new repository proposes profiles for the user to approve.
- **Web UI.** `dotai ui` serves the same use cases on `127.0.0.1`, with every action shown as its plan first.
- **JSON output.** Every command takes `--json` and prints a versioned envelope ([CLI-JSON.md](docs/CLI-JSON.md)).
- **Releases.** Tag-driven binaries for Windows, Linux and macOS, with `SHA256SUMS`.
