# Changelog

## [0.1.2](https://github.com/Lartweib/dotai/compare/v0.1.1...v0.1.2) (2026-11-22)


### Bug fixes

* **ui:** drop the cached pack after an action rewrites it ([e1a33e9](https://github.com/Lartweib/dotai/commit/e1a33e91e571da5eb7a5dd486c07624d6da8b41c))

## [0.1.1](https://github.com/Lartweib/dotai/compare/v0.1.0...v0.1.1) (2026-11-22)


### Bug fixes

* **install:** skip entries that vanish while scanning ([b9541b0](https://github.com/Lartweib/dotai/commit/b9541b029d84815dcacd1c659007188072e7e12d))

## 0.1.0 (2026-11-22)


### Features

* **cli:** add a --version flag that prints what dotai version prints ([96a1768](https://github.com/Lartweib/dotai/commit/96a176849592d0d0f434b799013b1b8b83139fda))
* **cli:** every command takes `--json` and prints a versioned envelope (docs/CLI-JSON.md) ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **doctor:** `dotai doctor` reports what changed outside dotai since it wrote it, with stable finding codes, and `--fix` writes missing exclude entries ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **hooks:** hooks that other programs register in Claude Code's `settings.json` or in agy's `hooks.json` after an install are kept by later installs and updates and by the restore of an isolated install, and `dotai doctor` does not report them: dotai owns its own hook entries (decision 0050) ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **install:** `dotai install` installs a pack's user scope for Claude Code, agy and OpenCode (preview), isolated under `~/.dotai/isolated/` or in replace mode, with harness detection, a backup before every write, a journal and a rollback; `dotai run <harness>` launches a harness against its isolated install ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **install:** a replace install keeps each harness's login, history, caches and runtime state, including Claude Code's running and background sessions and agy's prompt history and the quota snapshots a statusline writes, and wipes vendor trees, `node_modules` and Claude Code's plugin marketplaces and cache (decision 0038) ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **install:** a replace install of a harness that already has one only updates the pack there, wiping nothing and keeping what was added since; the first one still resets the harness, and `--reset`, or the choice asked on a terminal and in the web UI's install step, resets again (decision 0051) ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **install:** a running harness stops a replace install only for the files it rewrites itself ([b48b676](https://github.com/Lartweib/dotai/commit/b48b676942766d12dfa2aeccf487d00a9b158016))
* **install:** Claude Code's user-scope MCP servers go into its `.claude.json`, isolated or `~/.claude.json`, owned server by server, so that the rest of that file stays (decision 0049) ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **install:** without `--json`, an install and a restore report their progress on stderr, a line per phase with a running count, rewritten in place on a terminal; on Windows, one that stops because another program holds a path says so, and how to find the program with Resource Monitor ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **onboarding:** `dotai install` generates a profiling skill and a session-start hook, so that a session in a new repository proposes profiles for the user to approve ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **pack:** --ref latest follows a pack's highest release tag ([fbb38f4](https://github.com/Lartweib/dotai/commit/fbb38f459c4cec28a2d37428a6a5cc549a4380af))
* **pack:** refuse a silent downgrade of latest and settle legacy refs ([b918b4c](https://github.com/Lartweib/dotai/commit/b918b4cee461b996268740ca2aa590c32b4794d4))
* **packs:** `dotai pack add`, `update`, `remove`, `list` and `copy`, with packs from github.com fetched by the user's git or from a local directory; an update shows what changed in the pack's executable content first ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **packs:** `dotai pack new` creates a pack from a template embedded in the engine, and a "dotai pack" badge for a pack's README; files left as the template wrote them need consent to install ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **packs:** `dotai validate` and `dotai stage` load a pack (`dotai.yaml`, a base, variants and profiles in the rulesync layout), apply the layer merge rules and report diagnostics with stable codes ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **project:** `dotai apply --profile <p>` applies profiles to a project in local, untracked files only, listed in `.git/info/exclude`, with mods as Claude Code plugins ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **release:** `install.sh` and `install.ps1` install a release in one command, after verifying its checksum against `SHA256SUMS`, without sudo, and put its directory on `PATH` unless `DOTAI_NO_MODIFY_PATH=1` or `--no-modify-path` is given (decision 0048) ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **release:** tag-driven binaries for Windows, Linux and macOS with `SHA256SUMS`, and `THIRD_PARTY_NOTICES` in every archive with the license texts of the Go standard library, of each linked module and of the web UI's fonts (decision 0042) ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **rendering:** a pinned rulesync binary, 43.10.0, verified against checksums embedded in the engine, renders rules, commands, subagents, skills, MCP, hooks and permissions; dotai adds per-harness settings, tools and their path variables ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **restore:** `dotai restore` undoes installs from the backups, newest first, and `dotai restore --project` undoes a project apply ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **ui:** `dotai ui` serves the same use cases on `127.0.0.1`, with every action shown as its plan first ([3f971fb](https://github.com/Lartweib/dotai/commit/3f971fb8b4c1926d57439c1c8086a7e585d8ce7c))
* **ui:** wrap identities and paths only after a slash ([bc4c598](https://github.com/Lartweib/dotai/commit/bc4c598ab5f908e4f4587cb68b6df59d8fa41b69))


### Bug fixes

* **install:** fail closed on unattributed live-file writes, fold case per OS and drop agy's config.json ([53a13e5](https://github.com/Lartweib/dotai/commit/53a13e5c41fa6ee02ad9738aa2f96a5bcd3b9c69))
* **install:** keep the installed variant when --variant is not given ([9397735](https://github.com/Lartweib/dotai/commit/939773567c0c794339a458eac5a09d71397cac85))
* **install:** make updating only the default of the reinstall question ([0069cd6](https://github.com/Lartweib/dotai/commit/0069cd6dbf72972db06a5f959488608063fa8429))
* **install:** send an empty array, not null, for the wouldReset lists ([1fa0b44](https://github.com/Lartweib/dotai/commit/1fa0b44845ac294040b2ce3ac6d0323ff9b7fa7a))
* **ui:** pin the intro only in details laid out in two columns ([cc55c2a](https://github.com/Lartweib/dotai/commit/cc55c2a7ef98eb53f5e81fd849b2bbe429897855))
* **ui:** use the width of wide screens in the shell and boards ([8d9e897](https://github.com/Lartweib/dotai/commit/8d9e89761ea55a2872def942f9afa8f36bce2181))
