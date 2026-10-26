---
status: accepted
date: 2026-10-24
tags: [review, tracker, dependencies, packs]
---

# 0016 · Review lives in external tools that the pack declares

## Context

An earlier plan placed a review web module inside the base, with its own local server. An external work tracker with agents as collaborators covers the same ground. A review card is a tracker item: checks with verdicts, media evidence, rounds and owner approval map onto a tracker's model. Building review inside dotai would duplicate the server, schema, evidence store and agent protocol.

[0010](0010-engine-and-pack-separated.md) also separates the engine from the content: a review tool is a choice of the pack, not of the engine.

## Decision

dotai ships no review server and no review module. A review tool is an external tool with its own CLI or MCP server and no knowledge of dotai. The pack declares it, and the engine installs and configures it per profile (MCP or skill, its configuration, a project-scoped mod per [0011](0011-mods-always-project-scoped.md)), like any other external tool.

dotai depends only on the tool's public contract (CLI, MCP, configuration format), never on its internals.

## Considered options

- **A (chosen):** external tool, consumed through the pack. One implementation, reusable outside dotai, replaceable by another pack.
- **B:** a review module inside dotai. Rejected: it duplicates the tool and ties the engine to one review flow.

## Consequences

- Review is not an engine feature; it is an optional dependency that a pack activates per profile.
- The reference pack's review flow (tracker preset and approval rules) is recorded in the pack repository.
