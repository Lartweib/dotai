---
status: accepted
date: 2026-10-25
tags: [pack, profiles, merge, scopes, validation]
---

# 0023 · Flat, combinable profiles and explicit layer merge rules

## Context

[0008](0008-onboarding-by-detection-and-base-profile.md) applies "a profile with its sub-profiles", and [0006](0006-five-layer-product-and-v1-order.md) planned profiles with sub-profiles. A stack does not always hang from the same parent: Astro on Cloudflare can serve a commercial site or a tool's documentation site. A hierarchy forces duplicating a sub-profile under each parent.

[0022](0022-pack-format-and-manifest.md) merges layers into one staging tree, but the layers live in different scopes: the base and the variant are installed once per user, profiles once per project. With several layers, same-name items and repeated values must not overwrite each other silently or load twice.

## Decision

**Profiles are flat and combinable.** There are no sub-profiles. A project has zero or more profiles (for example `--profile web,astro-cloudflare`). A profile that is specific to a stack exists only when the stack repeats across projects; what is specific to one project goes in that project's own rules, which the onboarding agent may write.

**Scopes.** The base and the variant are rendered at user level. Profiles are rendered at project level. Each scope has its own staging tree and its own rulesync run.

**Merge rules.** Layer order is base, variant, then profiles. Profiles are peers and have no order among themselves.

| Element | Identity | Rule |
|---|---|---|
| Skills, subagents, commands, rules, MCP servers | Name | One name, one owner. A same name in two layers is an error, unless the later layer declares `dotai: { overrides: true }`. Two profiles can never override each other. |
| Lists: permissions, hooks per event | Value | Union without duplicates. If an entry is both allowed and denied, deny wins. |
| Single values: `statusLine`, an `env` variable, the default model | Key | A later layer may replace a value only if it declares the override. The same key in two profiles is an error. |
| Root rule (root `CLAUDE.md` or `AGENTS.md`) | Unique | Only the base provides it. Profiles and variants add named, non-root rules. |

Nothing is overwritten silently: every merge is a safe union, a declared override or an error. `dotai validate` checks all of this before anything is installed, including an item in a profile that repeats one already in the base, which would otherwise load twice.

## Considered options

- **A (chosen):** flat, combinable profiles; one staging tree per scope; explicit merge rules.
- **B:** profiles with sub-profiles. Rejected: a stack does not belong to a single parent, and the hierarchy adds a concept that combination already covers.
- **C:** last layer wins on every conflict. Rejected: silent overwrites are what install modes and backups try to prevent ([0018](0018-install-modes-and-mandatory-backup.md)).

## Consequences

- Partially supersedes [0008](0008-onboarding-by-detection-and-base-profile.md): sub-profiles are gone; detection by content and installation per profile stay.
- Refines [0013](0013-renderer-merges-by-key.md) (merge by key) for merges between layers, and corrects the single staging tree in [0022](0022-pack-format-and-manifest.md).
- Former sub-profiles (for example Astro + Cloudflare, or Next + Vercel + Supabase) become ordinary profiles if a pack keeps them. That is pack content, not an engine decision.
- Pending: verify what each harness does when a project item has the same name as a user item, since a cross-scope override depends on that precedence.

## Update (2026-10-25)

The precedence was verified and is not consistent: for skills, Claude Code keeps the user item while agy and OpenCode keep the project item; for subagents, all three keep the project item. A cross-scope override would behave differently per harness, so `overrides: true` only applies within a scope. A same-name item in the user scope (base, variant) and the project scope (profiles) is always a validation error.

Update 2026-11-02: an MCP server that two layers define the same way, by canonical JSON, is no conflict: within a scope, profiles included, it is kept once, as the first layer defines it; when a profile defines it as the user scope does, it is kept at user level only and dropped from the project scope, so there is one source, and Claude Code, whose project and local servers take precedence over user ones, never runs a project copy that goes stale ([PACK-FORMAT](../PACK-FORMAT.md#merge-rules)). Different definitions remain an error.
