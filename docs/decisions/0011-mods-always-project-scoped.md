---
status: accepted
date: 2026-10-23
tags: [mods, panel, scope, claude-code]
---

# 0011 · Mods always project-scoped

## Context

Mods (Claude Code panels) are plugins. A plugin can be installed with `user`, `project` or `local` scope (internal research on harness surfaces, not published, §5.1). [ARCHITECTURE](../ARCHITECTURE.md) already listed "mods always at project level" as an invariant, but without its own record.

Incident on 2026-10-23: a panel mod installed with user scope appeared in every project. It was uninstalled from user scope.

## Decision

Mods are always installed with project or local scope, never user scope. A pack's base carries no mods. Variants are also applied at user scope, so they carry no mods either (inferred).

## Considered options

- **A (chosen):** mods only with project or local scope.
- **B:** allow mods with user scope. Rejected: the incident shows that one project's panel ends up appearing in all of them.

## Consequences

- Good: each panel appears only where it makes sense.
- Accepted cost: each project that wants a panel installs it separately (with `project` scope, each collaborator installs it once).
- Pending (inferred): `dotai validate` rejects a mod in a user-scope layer.

## Evidence

- Incident observed on 2026-10-23.
- Internal research (not published) on harness surfaces, §1 (Mods) and §5.1.
