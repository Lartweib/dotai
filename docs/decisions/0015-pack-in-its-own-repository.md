---
status: accepted
date: 2026-10-23
tags: [repository, packs, open-source]
---

# 0015 · The reference pack lives in its own repository

## Context

[0010](0010-engine-and-pack-separated.md) splits dotai into a Go engine and packs of content, with a reference pack. The first commit of this repository included a draft skill, which mixed pack content into the engine repository.

## Decision

- **This repository holds only the engine:** Go code, source schema, renderers, docs and decision records.
- **The reference pack** (rules, skills, agents, profiles, policies) lives in a separate repository that dotai consumes. Its visibility is decided when it is created.
- **The only content allowed here** is a minimal, generic example pack under `examples/` or `testdata/`, used by golden tests for `apply` and `doctor` and by the documentation. It never contains a real user configuration.
- Draft pack content stays out of this repository until the pack repository exists.

## Considered options

- **A (chosen):** engine and reference pack in separate repositories, like lazy.nvim and the configurations built on it.
- **B:** one repository with the engine and the reference pack in subfolders. Rejected: it mixes a general-purpose product with one set of opinions, and users who only want the engine inherit content they do not need.

## Consequences

- Good: the engine repository stays generic and clean for open-source users; anyone can create their own pack repository from the same layout.
- Bad or accepted cost: two repositories to keep in step; changes to the source schema have to be checked against the reference pack.
- Pending: name and visibility of the pack repository.

## Evidence

- No associated research.
