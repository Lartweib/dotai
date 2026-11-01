---
status: accepted
date: 2026-10-23
tags: [rules, agents-md, claude-md, renderer]
supersedes: []
---

# 0012 · Rules file per harness: CLAUDE.md for Claude Code, AGENTS.md for agy and OpenCode

Partially supersedes [0003](0003-align-with-official-standards.md): only the point "`AGENTS.md` as the single source of rules, `CLAUDE.md` is not generated". The rest of 0003 remains in force.

## Context

Internal research on harness surfaces (not published, §0.1–0.3) found that:

- Claude Code documents only `~/.claude/CLAUDE.md` as user scope. `AGENTS.md` is read only as a project instruction.
- The `claude-md-and-agents-md` setting only matters if a `CLAUDE.md` already exists. Without `CLAUDE.md`, the default already reads `AGENTS.md`.
- `# Compact instructions` is documented only for `CLAUDE.md`. The documented alternative is a `SessionStart` hook with the `compact` matcher.

## Decision

The renderer generates `CLAUDE.md` for Claude Code and `AGENTS.md` for agy and OpenCode, from the same pack source. The single source of truth is still the pack.

## Considered options

- **A (chosen):** one rules file per harness, generated from the same source.
- **B:** `AGENTS.md` in all three and no `CLAUDE.md` (what 0003 said). Rejected: for Claude Code's global scope there is no documentary support, and compact instructions are not guaranteed.

The sources do not record other alternatives. Recommendation 6.4.1 of that research (a `CLAUDE.md` stub with `@AGENTS.md` or a full copy) is an implementation detail that remains open (inferred).

## Consequences

- Good: each harness reads its documented file, so nothing depends on undocumented behavior.
- Accepted cost: Claude Code and the other harnesses receive different output files; `dotai doctor` has to detect whether they drift from the source.
- Pending: live test of `AGENTS.md` loading (internal research on harness surfaces, not published, §7, rows 1 and 2; [OPEN-QUESTIONS](../OPEN-QUESTIONS.md)).

## Evidence

- Internal research (not published) on harness surfaces, §0.1–0.3, §6.3 and §6.4.

Update 2026-11-02: at project scope [0041](0041-project-scope-local-and-untracked.md) writes the rules to `.claude/rules/<pack>.md` for Claude Code and `.agents/rules/<pack>.md` for agy, and to a file named in `instructions` of `.opencode/opencode.json` for OpenCode; dotai never writes a project `CLAUDE.md` or `AGENTS.md`. On Claude Code 2.1.316 a project `AGENTS.md` alone did not load in an isolated configuration; an import from a rules file does ([HARNESS-NOTES](../HARNESS-NOTES.md#project-scope-inventory)).
