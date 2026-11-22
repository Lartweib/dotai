# Reviewing a pull request to dotai

dotai is a Go engine that writes AI coding harness configuration (Claude Code, Antigravity CLI, OpenCode) into a user's home directory and projects. A mistake can overwrite or delete a user's own configuration, so review for that first. Report problems that change behavior, lose data or weaken security; skip style, naming and formatting (gofmt and the `commits` check cover them) and anything a passing test already proves.

## Look for

- **Writes without the safety net.** Every write to a user's home or project goes through a plan (`--dry-run` shows it), a backup taken before the write, a journal with rollback on failure, and `dotai restore`. Flag code that writes, renames or deletes outside that path, or that changes what a plan shows without changing what is written, or the other way round.
- **Other programs' content.** dotai owns only what it records in its state. Flag changes that could delete or rewrite files, keys or hook entries another program or the user added ([decisions 0013, 0035, 0049, 0050](../docs/decisions/README.md)).
- **Tests that touch a real home.** Tests run in `t.TempDir()` with `HOME` and `USERPROFILE` pointed there; no test may read or write the developer's real home, start a real harness (`claude`, `agy`, `opencode`), or use the network. A test must fail when the behavior it names breaks; flag tests that cannot fail.
- **Command and path injection.** Values from a pack, a remote (git URLs, tag names), a manifest or a request must not reach a shell or a command's options unvalidated (`--` before arguments, no string-built commands). Paths must stay inside the directory they belong to (no `..`, absolute or symlinked escapes).
- **The JSON contract.** A change to any command's `--json` output, an error code or a web API response must update [docs/CLI-JSON.md](../docs/CLI-JSON.md); lists are never `null`.
- **Decisions.** A change to a behavior a decision record states needs that record updated or a new one in [docs/decisions/](../docs/decisions/README.md).
- **Cross-platform.** Windows, Linux and macOS are supported: path separators, case-insensitive file systems (Windows, macOS), line endings and file locking on Windows.

## Leave alone

- `CHANGELOG.md`: release-please writes it; do not ask for entries.
- Generated test goldens, unless the change in them is unexplained.
