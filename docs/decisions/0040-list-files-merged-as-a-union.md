---
status: accepted
date: 2026-11-01
tags: [pack, merge, hooks, permissions, validation]
---

# 0040 · Hooks and permissions merge as a union in layer order

## Context

[0023](0023-flat-profiles-and-layer-merge-rules.md) gives lists, permissions and hooks per event, the rule "union without duplicates". Until now the engine did not apply it: `.rulesync/hooks.jsonc` or `.rulesync/permissions.jsonc` in two layers of a scope was reported as `merge-pending`, so a variant or a second profile could not add one permission or one hook to what the base already has.

The files follow the rulesync schema that the pack pins (41.0.0):

- `hooks.jsonc` holds `version`, a `hooks` object with an array of entries per event, and tool-scoped blocks such as `claudecode.hooks` with the same shape.
- `permissions.jsonc` holds a `permission` object of categories (`bash`, `read`, `edit`, ...), each an object that maps a pattern to `allow`, `ask` or `deny`, and tool-scoped blocks such as `claudecode.permission`. A pattern is a key, so it holds one action per category; the permissions have no `allow` or `deny` arrays.

The MCP servers of every layer already merge into one `mcp.jsonc` per scope, which records the layer of each server in the stage's `origins`.

## Decision

The layers of a scope merge each of the two files into one, in layer order: base, then variant; the selected profiles in the order the engine merges them, by name.

- **Objects** merge by key. A key that a later layer adds comes after the earlier keys. This covers the events of `hooks`, the categories of `permission`, the patterns of a category and the tool-scoped blocks.
- **Arrays**, such as the entries of a hook event, are appended in layer order. An item identical to an earlier one, by canonical JSON (keys sorted, insignificant whitespace dropped), is kept once, at its first position.
- **Any other value**, such as a pattern's action or `version`, follows the Single values row of 0023: the same value in two layers is kept once, and a different value is an error, `hooks-conflict` or `permissions-conflict`. The files have no way to declare an override, so in this version the error has no exception. A value that is an object in one layer and an array or a scalar in another is the same error.
- **No removal.** A later layer only adds entries. It cannot remove or change an entry of an earlier layer.
- A file that is not a JSONC object, or that repeats a key in one object, is `hooks-invalid` or `permissions-invalid`.
- The merged file has no single layer. The stage records the layer of each value and array item in `origins`, by its JSON pointer: `/permission/bash/git status`, `/hooks/preToolUse/0`.
- A file in one layer only is merged the same way, so it is checked the same way and gets `origins`.
- `merge-pending` is removed rather than kept reserved: `dotai.cli/v1` is not released yet, and no other file waits for a list merge.

0023's "if an entry is both allowed and denied, deny wins" keeps its meaning through rulesync and the harnesses: a `deny` and an `allow` on different patterns are both kept, and rulesync and each harness apply `deny` over `ask` over `allow`. The same pattern with a different action in two layers is a conflict, not a silent deny.

## Considered options

- **A (chosen):** union in layer order with duplicates kept once, a different scalar an error, no removal.
- **B:** a later layer replaces the whole file. Rejected: it is the silent overwrite that 0023 rules out, and a variant would have to repeat every permission of the base.
- **C:** deny wins when two layers give a pattern different actions. Rejected for now: a later layer could then turn a base `allow` into a `deny` without saying so, and a base `deny` could hide a profile's `allow` with no message. An error keeps every change visible.

## Consequences

- Implements the Lists row of [0023](0023-flat-profiles-and-layer-merge-rules.md) and refines it for the rulesync schema: the identity of a permission is its category and pattern, and the identity of a hook entry is its canonical JSON.
- Good: a variant or a profile adds one permission or one hook without copying the base's file.
- Accepted cost: comments and the original formatting of the source files are not kept in the staged file.
- Accepted cost: a pack that needs to drop an entry of an earlier layer for one variant must move that entry out of the earlier layer.
- Pending: removing or changing an earlier entry through a declared override, as `dotai: { overrides: true }` and `dotai.overrides` do for items and settings. It needs a place in these files, or beside them, that rulesync ignores.

## Evidence

- Maintainer decision, 2026-11-01.
- rulesync 41.0.0, `docs/reference/file-formats.md` (sections `.rulesync/hooks.jsonc` and `.rulesync/permissions.jsonc`) and `src/types/permissions.ts`: a permission category maps a pattern to one action.
- A stage with the pinned binary of a base and a variant that both have the two files: rulesync accepts the merged files and renders the entries of both layers into `.claude/settings.json` (`hooks`, `permissions`), agy's `settings.json` (`permissions`) and `.gemini/config/hooks.json`, and OpenCode's `opencode.jsonc` (`permissions`) and `plugins/rulesync-hooks.js` (`TestStageMergesListFilesWithRealRulesync`).
- [0023](0023-flat-profiles-and-layer-merge-rules.md), [PACK-FORMAT](../PACK-FORMAT.md#merge-rules).

Update 2026-11-02: rulesync 41 renders a tool-scoped block's hooks of an event, such as `claudecode.hooks.sessionStart`, instead of the shared hooks of that event for that harness ([HARNESS-NOTES](../HARNESS-NOTES.md#rulesync-4100)). The merge keeps both, as above; `validate` warns about each such event whose shared hooks are not empty with `hooks-shadowed`, which leaves the pack valid ([CLI-JSON](../CLI-JSON.md#diagnostics)).

Update 2026-11-02 (roadmap PR 10-3): in a project, a list the profiles merge into a file that is not dotai's, such as `permissions.allow` in `.claude/settings.local.json`, is a union with the list there, whose entries stay first. The project record keeps the list from before, so that a later apply without the profile, or `dotai restore --project`, gives it back ([0041](0041-project-scope-local-and-untracked.md)).
