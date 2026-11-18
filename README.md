# dotai

> Working name. Cross-harness configuration engine for AI coding agents.

One source of rules, agents, skills, hooks, MCP servers and settings, rendered into Claude Code, Google Antigravity CLI and OpenCode, with per-project-type profiles and per-environment variants from a pack.

dotai is split into a Go **engine** and **packs** of content. A reference pack is kept in its own repository; you can point the same binary at your own pack instead. See [0010](docs/decisions/0010-engine-and-pack-separated.md) and [0015](docs/decisions/0015-pack-in-its-own-repository.md).

**Status:** early development. dotai validates and stages a pack, installs its user scope isolated or in place of each harness's own configuration with a backup and a restore, launches each harness against the isolated install, and applies profiles to a project, kept out of git, which `dotai restore --project` undoes. `dotai pack new` creates a pack from dotai's embedded template, with your profiles, for you to edit ([Creating a pack](docs/PACK-FORMAT.md#creating-a-pack)); `dotai pack` adds packs from GitHub or a local directory to a registry, and updates a GitHub pack after showing what changed in its hooks, MCP servers, settings, tools and mods. [CLI JSON output](docs/CLI-JSON.md) lists the commands and what each does.

## Install

No release is published yet. Each release on [github.com/lartweib/dotai/releases](https://github.com/lartweib/dotai/releases) will have an archive per platform (Windows x64, Linux x64 and arm64, macOS x64 and arm64) and a `SHA256SUMS` file ([0042](docs/decisions/0042-tag-driven-release-binaries.md)). The archives are named `dotai_<version>_<os>_<arch>.tar.gz`, or `.zip` for Windows; below, `<archive>` is the one you downloaded.

1. Download the archive for your platform and `SHA256SUMS` from the same release.
2. Verify the checksum in the download directory:
   - Linux: `grep <archive> SHA256SUMS | sha256sum --check`
   - macOS: `grep <archive> SHA256SUMS | shasum -a 256 --check`
   - Windows (PowerShell): `(Get-FileHash <archive>).Hash`, and compare it with the line for that file in `SHA256SUMS`.
3. Once the repository is public, releases also carry a build provenance attestation. Verify it with the GitHub CLI: `gh attestation verify <archive> --repo lartweib/dotai`.
4. Extract the archive and put `dotai` (`dotai.exe` on Windows) in a directory on your `PATH`, such as `~/.local/bin`.
5. Check it with `dotai version`.

From source, with Go installed: `go install github.com/lartweib/dotai/cmd/dotai@latest`.

## Docs

- [Architecture](docs/ARCHITECTURE.md)
- [Pack format](docs/PACK-FORMAT.md)
- [CLI JSON output](docs/CLI-JSON.md)
- [Decision records](docs/decisions/README.md)
- [Harness notes](docs/HARNESS-NOTES.md)
- [Roadmap](docs/ROADMAP.md)

## License

[MIT](LICENSE)
