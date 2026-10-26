---
status: accepted
date: 2026-10-25
tags: [engine, go, rulesync, source-schema, renderers]
supersedes: 0009
---

# 0021 · The Go engine orchestrates; a pinned rulesync renders

## Context

[0009](0009-own-go-engine-rulesync-schema.md) chose an own Go engine with three own renderers, using the rulesync schema only as a reference. It rejected rulesync as a dependency because it was TypeScript/Node, clashing with the single Go binary from [0004](0004-installer-language-and-shape.md), and because of its release pace.

A verification of rulesync 41.0.0 (2026-10-24) changed the facts:

- It renders rules, commands, subagents, skills, MCP, hooks and permissions for Claude Code (`claudecode`), agy (`antigravity-cli`) and OpenCode, plus about 70 other targets, including Copilot, Codex CLI and Cursor ([0019](0019-v1-harness-targets.md)).
- It tolerates a top-level `dotai:` frontmatter block and strips it from the output, in all three v1 harnesses.
- Its OpenCode output works on both OpenCode v1 and v2.
- It publishes standalone binaries with `SHA256SUMS` for Windows x64, Linux (x64, arm64) and macOS (x64, arm64). Node is not required.
- It has no settings feature: `statusLine`, `env` and model defaults are not covered.
- It still ships breaking majors often.

Writing three renderers means reimplementing what rulesync already maintains, and six renderers after v1.

## Decision

dotai stays an own Go binary. It orchestrates; rulesync renders what it covers.

- **Pack content** uses the `.rulesync/` layout as-is. dotai's own fields go in a `dotai:` block, which dotai reads from the sources because rulesync strips it.
- **dotai owns:** loading packs, profiles and variants as layers ([0017](0017-pack-variants-as-layers.md)); install modes, backup and restore ([0018](0018-install-modes-and-mandatory-backup.md)); `doctor`; and everything rulesync does not cover (settings such as `statusLine`, `env` and model defaults, plugins, runners and tools).
- **rulesync** is an external tool pinned to an exact version. dotai downloads the official binary for that version and verifies its checksum. It never uses an unpinned or system-wide rulesync.
- **dotai never lets rulesync write to harness locations.** rulesync renders into a staging directory; dotai then applies the result with its merge by key ([0013](0013-renderer-merges-by-key.md)) and the active install mode.

## Considered options

- **A (chosen):** Go engine that orchestrates a pinned rulesync.
- **B:** own renderers per harness ([0009](0009-own-go-engine-rulesync-schema.md)). Rejected: it duplicates maintained work for fast-changing harness formats, and each new harness costs a full renderer.
- **C:** rulesync as the engine and dotai as a distribution on top. Rejected: packs, variants, install modes, backup and `doctor` lie outside rulesync.

## Consequences

- Supersedes [0009](0009-own-go-engine-rulesync-schema.md).
- Good: the adapter for a new harness ([0019](0019-v1-harness-targets.md)) is mostly rulesync configuration plus dotai's owned paths and isolation.
- Bad or accepted cost: a second binary and a dependency on its release behavior. Upgrading the pinned version is a deliberate change, gated by CI tests over the reference pack.
- Windows on ARM has no native rulesync binary. The x64 binary likely runs under Windows x64 emulation (unverified); it is not a v1 priority.
- Pending: whether rulesync's `--input-roots` overlays can replace dotai's layering, or dotai merges layers into one staging tree first.

## Evidence

- Internal research (not published): pack format verifications, 2026-10-24.
- rulesync v41.0.0 release assets (binaries and `SHA256SUMS`), checked 2026-10-25.
