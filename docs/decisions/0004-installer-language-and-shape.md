---
status: accepted
date: 2026-10-19
tags: [installer, go, architecture]
---

# 0004 · Installer language and shape

> **Partially superseded by [0006](0006-five-layer-product-and-v1-order.md): build order and profiles.** Still in force: the Go language, the `apply`/`doctor` shape, the core with a bounded budget and the existence of opt-in modules.

## Context

An installer is needed that renders a single source to three harnesses.

## Decision

A Go binary. A single source (YAML + Markdown) and one renderer per harness:

```
dotai apply --harness claude|opencode|agy --profile <profile> [--project .]
dotai doctor   # diffs the generated output against the source
```

Base architecture: a core with a bounded token budget, profiles per project type and opt-in modules. The concrete lists of profiles and modules, and the order to build them, are reference pack content.

## Considered options

The sources do not record rejected alternatives for language or shape.

## Consequences

- Superseded by 0006: the build order and the profiles.
- Kept: Go, `apply`/`doctor`, single source with one renderer per harness.

## Evidence

- No associated research. The later revision is in [0006](0006-five-layer-product-and-v1-order.md).
