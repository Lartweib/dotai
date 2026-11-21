---
status: accepted
date: 2026-11-01
tags: [engine, install, backup, restore, harnesses, pack-format]
---

# 0038 · Replace mode resets each selected harness to a fresh install

## Context

[0034](0034-replace-mode-in-v1.md) puts the replace mode in v1 with an allowlist of owned paths per adapter, following rule 3 of [0018](0018-install-modes-and-mandatory-backup.md): replace removes only what dotai owns, never a whole directory and never a path several harnesses read. An allowlist leaves in place whatever the user or another tool put in the harness's directories, so the result is the user's previous setup plus the pack, the mix 0018 set out to avoid. The maintainer wants replace to give what installing the pack right after installing the harness would give.

What each harness reads and creates at user level was inventoried on Windows ([HARNESS-NOTES](../HARNESS-NOTES.md#replace-inventory)); the facts still unverified are marked there.

## Decision

**Purpose.** At user level, `dotai install --mode replace` takes each selected harness back to the state of a fresh install, then installs the pack on that clean base.

**What is wiped.** Everything that a supported harness (Claude Code, agy, OpenCode) reads at user level and that can affect its behavior. Kept: login and credentials; history (sessions, conversations, history files, OpenCode's database); caches; and the harness state it needs to work, such as agy's `config/.migrated`. A path that only other tools read is never touched. The generic user-level paths that a supported harness reads, `~/.agents/skills`, `~/.agents/`, `~/AGENTS.md`, `~/CLAUDE.md` and `~/.opencode`, are wiped for a closed variant as accepted collateral damage: the plan names each one and warns that other tools reading it are affected. A variant that shares skills ([0036](0036-shared-skills-per-variant.md)) keeps `~/.agents/skills` and exposes it as in the isolated mode.

**Keep-lists.** Inside each harness directory, `~/.claude`, the parts of `~/.gemini` that agy owns (`config/`, `antigravity-cli/`) and `~/.config/opencode`, the adapter declares a keep-list as data: every entry not on it is wiped, unknown entries included. A path outside those directories is wiped only as an explicit leak entry of the adapter. A file that mixes configuration and state is split by key. In `~/.claude.json` the configuration keys are wiped: `mcpServers`, and per project `mcpServers`, `allowedTools`, `enabledMcpjsonServers`, `disabledMcpjsonServers` and `mcpContextUris`; account, onboarding, stats and per-project trust and history are kept. dotai never owns `~/.claude.json`.

**Safety net.** An unknown entry whose name looks like login or history (`*cred*`, `*auth*`, `*token*`, `*oauth*`, `*.db`, `*history*`) makes the plan refuse until the adapter classifies it.

**Gemini CLI and the IDE.** The files under `~/.gemini` that only Gemini CLI reads, the root `settings.json`, `oauth_creds.json`, `google_accounts.json`, `commands/` and `extensions/`, and the Antigravity IDE's `antigravity/` are never touched. agy reads the root `~/.gemini/GEMINI.md`, so it is wiped, with a warning that Gemini CLI is affected; by the same rule, so is `~/.gemini/AGENTS.md`, which agy's changelog names as a global rules file too. In replace mode agy's rules file installs to `~/.gemini/config/GEMINI.md` instead of the root, so Gemini CLI gets nothing new; the move applies to replace only.

**Every replace wipes.** Each replace install wipes again. Consent is needed whenever the plan removes or overwrites anything dotai does not own, and always on the first replace: on a terminal y/N, otherwise a refusal with exit code 1 unless `--yes`. A file added later in a wiped area is left alone until the next replace install, which wipes it again, backed up, listed and under consent. A settings file dotai recreated follows [0035](0035-settings-files-owned-by-key.md): keys the harness writes, such as `theme`, stay, and a user change to a dotai key is a conflict.

**Backup by move.** Each wiped top-level entry is renamed into `~/.dotai/backups/<id>/files/...`, keeping links, junctions, permissions, timestamps and empty directories, and the manifest lists the SHA-256 of every file. Across volumes the move falls back to copy, sync and delete, with a warning about the size. Rollback moves the entries back, and so does restore, exactly; `~/.claude.json` is restored key by key. The first replace backup is pinned: it and the restore chain back to it are never pruned.

**Mode switching.** `install` refuses to go from replace to isolated and points to `dotai restore`. A plain `dotai install` uses the mode the state records, or isolated when there is no state. `dotai run` on a replace install says to start the harness directly.

**Refusals.** Install and restore refuse while a selected harness is detected running (best effort per OS), unless `--ignore-running`. Replace refuses when a relocation variable is set: `CLAUDE_CONFIG_DIR`, `XDG_*`, `OPENCODE_CONFIG*`, `OPENCODE_DB`.

**Harness selection, in both modes.** Detection uses two separate signals: the binary on `PATH` and the configuration directory; agy also requires the `agy` binary, since Gemini CLI creates `~/.gemini` too (inferred reason). A stat error other than not found is an error, not absence. `--harness a,b` selects; the default is the pack's harnesses that are detected. The selection is saved in `state.json` and reused by a plain install. The plan shows, per harness, whether it was detected, its binary path, its configuration path and whether dotai manages it. In the manifest, `harnesses:` are required and the new `optional-harnesses:` are optional. A required harness that is missing or deselected aborts before anything is written, naming it ("the pack requires antigravity-cli, which is not installed (agy not found on PATH)"); a missing optional one is skipped with a notice. Replace applies only to the selected harnesses, and v1 has one mode for the whole install.

## Considered options

- **A (chosen):** keep-lists per harness directory, explicit leak entries outside them, wipe by move into the backup.
- **B:** an allowlist of owned paths that keeps the user's settings keys. Rejected: the result is not clean.
- **C:** wipe whole harness directories, login and history included. Rejected: the user must log in again and loses their history.
- **D:** skip a missing required harness silently. Rejected: the install would not be what the pack declares.
- **E:** preselect every harness when none is detected. Rejected: it would install into harnesses that are not there.
- **F:** a mode per harness. Rejected for v1: it needs a new state schema (`dotai.state/v2`).

## Consequences

- Partially supersedes [0018](0018-install-modes-and-mandatory-backup.md): replace wipes whole areas by keep-list instead of an allowlist, and wipes paths other tools read (rule 3 and the rejection of option C), and a replace backup moves entries instead of copying them. Partially supersedes [0034](0034-replace-mode-in-v1.md): the allowlist, and the exclusion of `~/.claude.json` and shared paths from it. Amends [0032](0032-install-state-backup-and-isolated-launch.md) (backup by move, the pinned first replace backup, the saved selection) and [0033](0033-restore-from-the-install-history.md) (restore moves wiped entries back and checks targets outside the isolated directories).
- Good: a replace install has the same result on any machine, and restore brings back exactly what was there.
- Accepted cost: tools that read the generic paths lose what they had there until a restore; the plan names them every time.
- Accepted cost: a wrong keep-list wipes a file the harness needed. The backup by move and restore limit the damage, and the safety net catches likely logins and history.
- Pending: the keep-lists and leak entries of each adapter, how a running harness is detected on each OS, and the facts still unverified in [HARNESS-NOTES](../HARNESS-NOTES.md#replace-inventory).

## Evidence

- Maintainer decision, 2026-11-01.
- [HARNESS-NOTES](../HARNESS-NOTES.md#replace-inventory): what each harness creates and reads at user level, checked in a temporary home on Windows.
- [ROADMAP](../ROADMAP.md): rows 9b-1 to 9b-4.

Update 2026-11-01: the pin is read from the install history, not recorded: the pinned backup is the oldest replace install the install state leads back to, so a replace install that rolled back is never pinned, a killed one that the next install settled is, and a restore that undoes it releases it, the next replace backup being pinned in turn. The move across volumes is not implemented yet: such a move fails and rolls the install back; the copy, sync and delete fallback moves to roadmap PR 9b-4.

Update 2026-11-01 (roadmap PR 9b-4): consent is needed when the plan wipes an entry that holds a file or a link, wipes a key of `~/.claude.json` or replaces a file dotai does not own; a first replace whose wipes are all empty directories, or that wipes nothing, needs none, since it removes nothing of the user's. The running-harness check applies to a replace install and to a restore that writes into a harness's own configuration, not to the isolated mode: a harness running outside the isolation does not read the isolated directories, and a check by executable name cannot tell the two apart. The move across volumes is implemented; each such move is journaled in `~/.dotai/crossing.json`, and the next install or restore settles one a kill cut short before it plans.

Update 2026-11-02 (roadmap PRs 10-2 and 10-3): "dotai never owns `~/.claude.json`" holds for the user level. At project scope [0041](0041-project-scope-local-and-untracked.md) owns the MCP servers it writes under `projects["<path>"].mcpServers`, recorded under the home directory, in the project's record in `~/.dotai/projects/<project id>/` and in the list `~/.dotai/projects.json`. A replace install reads that list and never wipes those servers: a project entry whose `mcpServers` holds one is wiped server by server, each but those, and the restore of the replace puts the others back. An entry whose project or record is gone is ignored, with a notice in the plan, and its servers are wiped like any other.

Update 2026-11-18: under [0047](0047-harness-selection-and-preview-harnesses.md), a required harness is one the manifest lists in `requires`; the others in `harnesses` are compatible, and the user picks among those detected.

Update 2026-11-20: [0049](0049-claude-user-mcp-servers-in-claude-json.md) partially supersedes "dotai never owns `~/.claude.json`": at user level a replace install now owns the MCP servers it writes into `mcpServers`, server by server, and nothing else of the file. The reset still wipes the user's own servers and per-project configuration keys, but keeps the servers the previous install owns; the wipe and the pack's servers are one write of the file, with one copy in the backup, and a restore gives the file back key by key, exactly when nothing changed since.

Update 2026-11-21: a replace dry run over a real Windows install (Claude Code 2.1.330, agy 1.5, OpenCode 2.0.30) refused 38 paths, so the install could not run even with `--yes` ([HARNESS-NOTES](../HARNESS-NOTES.md#a-real-install-claude-code-21330-agy-15-opencode-2030-windows)). Ten were harness state the keep-lists did not know yet, now classified: Claude Code's `sessions/`, `daemon/` (with `jobs/` and `daemon.log`, which the docs list beside it), `transcripts/` and `mcp-needs-auth-cache.json`, and agy's `antigravity-cli/history.jsonl`; the rest of the application data that Claude Code's docs list is kept too. The other 28 were false positives of the safety net inside third-party code: file names such as `Tokenizer.js`, `Cookies.ts`, `tokens.css` and `secret.py` in `~/.config/opencode/node_modules` and in Claude Code's `plugins/marketplaces/` and `plugins/cache/`. The decision is unchanged; its scan is narrowed: a **vendor tree**, any `node_modules` and the directories an adapter declares as vendor (Claude Code's `plugins/marketplaces` and `plugins/cache`), is wiped with its entry without looking inside it for credentials. Weighed against it was narrowing the inside signals to credential-shaped files only (exact names such as `.credentials.json` and `auth.json`, `*.key` and `*.pem` near the top). That would also silence the user's own files that intentionally refuse today, such as `agents/sub/api_token.txt`, and would still refuse on the certificates and keys that packages ship as test fixtures. A vendor tree is put there by a package manager or a marketplace and is put back by it, a harness writes no login there, and the wipe is a move into the backup, so not looking into it loses nothing a restore cannot give back. Everywhere else, `plugins/data/` included, every signal still refuses. With the false positives gone, `~/.claude/plugins` is wiped whole, as before: a fresh install has no plugins.
