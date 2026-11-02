# dotai

> Working name. Cross-harness configuration engine for AI coding agents.

One source of rules, agents, skills, hooks, MCP servers and settings, rendered into Claude Code, Google Antigravity CLI and OpenCode, with per-project-type profiles and per-environment variants from a pack.

dotai is split into a Go **engine** and **packs** of content. A reference pack is kept in its own repository; you can point the same binary at your own pack instead. See [0010](docs/decisions/0010-engine-and-pack-separated.md) and [0015](docs/decisions/0015-pack-in-its-own-repository.md).

**Status:** early development. dotai validates and stages a pack, installs its user scope isolated or in place of each harness's own configuration with a backup and a restore, launches each harness against the isolated install, and applies profiles to a project, kept out of git, which `dotai restore --project` undoes. [CLI JSON output](docs/CLI-JSON.md) lists the commands and what each does.

## Docs

- [Architecture](docs/ARCHITECTURE.md)
- [Pack format](docs/PACK-FORMAT.md)
- [CLI JSON output](docs/CLI-JSON.md)
- [Decision records](docs/decisions/README.md)
- [Open questions](docs/OPEN-QUESTIONS.md)
- [Harness notes](docs/HARNESS-NOTES.md)
- [Roadmap](docs/ROADMAP.md)

## License

[MIT](LICENSE)
