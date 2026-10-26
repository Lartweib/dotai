---
status: accepted
date: 2026-10-25
tags: [engine, install, backup, isolation, harnesses]
---

# 0018 · Install modes (isolated, replace, merge) with a mandatory backup

## Context

[0013](0013-renderer-merges-by-key.md) only defines merging: dotai merges by key and never rewrites foreign content. That protects existing configuration, but it produces a mix of the user's previous setup and the pack, which is hard to reason about. Replacing everything without a backup loses the user's configuration. The user needs to choose how a pack lands, and to be able to go back.

A verification of the targeted harnesses and of the most used ones beyond them (2026-10-24) found:

- Most harnesses can relocate their user configuration (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `GEMINI_CLI_HOME`, `COPILOT_HOME`, `XDG_*` for OpenCode, `--gemini_dir` for agy). agy's flag is undocumented and has no environment variable equivalent.
- Several harnesses read the same paths: `~/.claude/skills`, `~/.claude/agents`, `~/.agents/skills`; `~/.gemini` is shared by agy, Gemini CLI and the Antigravity IDE.
- Part of the state is not plain files: plugin and marketplace registries, SQLite databases, cloud-synced settings. Some harnesses rewrite their files while running (Claude Code and `~/.claude.json`).

A runtime check of the v1 harnesses (2026-10-25) found that none isolates fully:

| Harness | Mechanism | Level | What still leaks or breaks |
|---|---|---|---|
| Claude Code | `CLAUDE_CONFIG_DIR` | partial | The real `~/.claude/CLAUDE.md` loads when the working directory is under the home folder, through parent-directory discovery. On Windows, credentials live in the config dir, so the user must log in again. |
| agy | `--gemini_dir` | partial | Only authentication is shared, through the OS keyring, which is desired. |
| OpenCode v2 | `XDG_*` plus `OPENCODE_DISABLE_CLAUDE_CODE` and `OPENCODE_DISABLE_EXTERNAL_SKILLS` | partial | `~/.agents/skills` still loads. It needs its own service port while the user's service runs. Provider credentials do not carry over. |
| OpenCode v1 | same | full for configuration | Provider credentials do not carry over. v1 and v2 cannot share a data directory. |

## Decision

The installer offers three modes, chosen at install time:

- **Isolated:** install the pack into a separate configuration directory per harness and create an alias command (for example `claude-dotai`). The user's configuration is not touched.
- **Replace:** back up, remove the files the harness owns, then apply the pack.
- **Merge:** the behavior of 0013.

Rules that apply to every mode:

1. **Backup is always taken** before any write, and `dotai restore` brings it back. It is not an option the user can skip.
2. **Credentials are never backed up or removed.** Login stays where the harness keeps it.
3. **Replace works on an allowlist of paths owned by each harness**, declared by its adapter. It never removes a whole directory, and never removes a shared path (one read by several harnesses).
4. **Each adapter declares its isolation level** (`full`, `partial`, `none`), what still leaks, and whether the user must log in again. dotai shows this before installing; a harness with `none` does not offer the isolated mode.
5. **Replace and restore require the harness to be closed**, because some harnesses rewrite their files while running.
6. Project-level files (rules files, `.claude/`, `.mcp.json`) still load in every mode; isolation covers user-level configuration only.

Isolation per harness:

- **OpenCode:** the isolated setup sets both disable variables, its own service port, and a separate data root per OpenCode major version.
- **agy:** the adapter checks that `--gemini_dir` is still accepted before offering the isolated mode. If the flag disappears, agy's level becomes `none` and only replace and merge are offered.

Backup format:

```
~/.dotai/backups/<timestamp>_<operation>/
  manifest.json   # mode, pack, variant, harness; per file: original path, sha256, permissions
  files/          # exact copy of each file, under its original path
```

- Only the paths dotai is about to write are backed up, never a whole directory and never credentials.
- Plain files and a manifest, so a backup can be inspected and restored by hand.
- `dotai restore` checks hashes first. If a file changed after the install, it warns and asks instead of overwriting it.
- The last N backups are kept (10 by default). The first backup, which holds the user's original configuration, is never deleted automatically.

## Considered options

- **A (chosen):** three modes, mandatory backup, replace by allowlist, isolation level per adapter.
- **B:** merge only (0013 as is). Rejected: it produces the mix the user wants to avoid.
- **C:** replace by wiping the harness directory. Rejected: shared directories would break other harnesses and log the user out.
- **D:** backup as an option. Rejected: the one time it is skipped is the time the configuration is lost.
- **E:** backups as archives (zip, tar). Rejected: they cannot be inspected or restored by hand without dotai.

## Consequences

- Extends [0013](0013-renderer-merges-by-key.md): merge becomes one of three modes.
- Each harness adapter carries more knowledge: owned paths, shared paths, isolation mechanism, isolation level, re-login needs, non-file state to back up.
- The engine records the installed mode and variant ([0017](0017-pack-variants-as-layers.md)) so that updates, `doctor` and `restore` act consistently.
- The isolated mode is a trial mode, not a perfect sandbox: the warnings above are shown every time.

## Evidence

- Internal research (not published): harness install modes verification, 2026-10-24.
- Internal research (not published): isolation runtime verification, 2026-10-25.
