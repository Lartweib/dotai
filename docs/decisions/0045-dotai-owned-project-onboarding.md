---
status: proposed
date: 2026-11-17
tags: [onboarding, profiles, skills, hooks, budget, manifest]
---

# 0045 · Project onboarding is dotai's: a generated profiling skill and a session-start hook

## Context

Profiles are chosen by detection by content, with the minimum of questions and the user's approval ([0008](0008-onboarding-by-detection-and-base-profile.md)); the engine never picks one ([PACK-FORMAT](../PACK-FORMAT.md#scopes)). Today the procedure is pack content: the example pack ships a generic onboarding subagent, `examples/minimal-pack/base/.rulesync/subagents/onboarding.md`, which each pack copies and adapts.

The copies drift from the engine's commands, the user has to know the subagent exists and call it, and nothing notices when a set-up project's profiles no longer fit.

The marker `.dotai/state.json` is engine-written and lists the `pack`, `variant` and `profiles` applied (`internal/state/project.go`); `.dotai/` is local and excluded from git ([0041](0041-project-scope-local-and-untracked.md)). Every always-on piece has to justify its cost ([0007](0007-always-on-context-budget.md), proposed).

## Decision

The names of commands, fields, files and codes below are proposed.

- **dotai owns the onboarding.** `dotai install` generates two items and installs them at user scope in every harness it installs, from an engine layer staged after the base and the variant:
  - **The profiling skill, `dotai-profiles`:** the fixed procedure below, then the installed pack's profiles in declaration order, each with its name, `description`, `hints` and `mods`, regenerated on every install.
  - **A `SessionStart` hook** running `dotai onboarding hook --harness <name>` with the absolute path of the installing binary, quoted by each harness's rules ([PACK-FORMAT](../PACK-FORMAT.md#tools-and-project-config)). When that path is unquotable for agy, the engine omits the agy hook, still installs the skill, and warns `onboarding-hook-skipped`; the install does not fail.
- **Always-on cost.** Only the skill's name and description are always-on, the description at most 60 tokens; the body loads on demand. The hook prints one line of at most 40 tokens, and only while the repository is neither onboarded nor dismissed, or on a mismatch; otherwise it adds no context. Once [0007](0007-always-on-context-budget.md) is accepted, both count against the base's budget in each harness.
- **The fingerprint.** A digest of:
  - (a) the installed pack's identity and resolved revision: the commit for a git pack ([0044](0044-git-pack-distribution.md), proposed), the manifest `version` for a local one;
  - (b) the names of the manifest's profiles;
  - (c) the sorted names of the profiles with at least one `hints` glob matching a tracked path (`git ls-files`, which reads only the index), matched against the repository-relative path.

  `dotai apply` and `dotai onboarding dismiss` store it in `.dotai/onboarding.json`; a dismissal without a marker also stores the pack identity and the set (c) it was taken from.
- **The hook**, in order:
  1. Outside a git repository, in the home directory or at a drive root: silent.
  2. No marker, no dismissal: one line saying the repository is not set up, and to load `dotai-profiles`.
  3. No marker, a dismissal: the stored pack identity and set (c) against the current ones. Equal: silent. Different: one line.
  4. Marker present: the hook recomputes the digest and compares (a) and (b) with the marker's `pack` and `profiles`. All equal: silent. Any difference, or a missing digest (after a restore, or a marker from an older dotai), is a mismatch until the next apply or dismissal: one line saying why.
  5. Any error, or past a time bound (200 ms, proposed): silent. The hook never blocks a session.
- **`.dotai/onboarding.json` is engine-written.** `dismiss` in a never-applied repository creates `.dotai/` and its `.git/info/exclude` entry, and records the write in the project record, so `dotai restore --project` removes it; `dotai doctor` checks it as it checks the marker. The marker and this file are trusted only to decide whether to print one line, never for ownership.
- **The skill's procedure.**
  - Use only the installed pack's profiles.
  - Empty repository (no files besides a README, license or ignore files in the working tree, tracked or untracked, `.git/` and `.dotai/` excluded): interview the user about what the project will be.
  - Otherwise: detect the stack by content ([0008](0008-onboarding-by-detection-and-base-profile.md)), `hints` being evidence only, and ask only what the analysis leaves open.
  - Show `dotai apply --profile <a,b> --dry-run`; run `dotai apply --profile <a,b>` only after the user approves, with `--yes` only for conflicts the user approved. Never commit.
  - Marker present: analyze again and report the mismatch (a profile gone from the pack, one the content now suggests, one it no longer supports). It only reports and proposes; applying needs the same approval. When the user keeps the profiles, it runs `dotai onboarding dismiss`.
- **The engine still never picks a profile.** The hook reports facts, the skill proposes, the user approves. `hints` only tell the hook when to speak.
- **A pack may replace dotai's onboarding:**

  ```yaml
  onboarding:
    custom: base/.rulesync/subagents/onboarding.md
  ```

  `custom` is a path relative to the pack root, to a file under `<layer>/.rulesync/subagents/` or a directory under `<layer>/.rulesync/skills/`, where `<layer>` is the base or a variant. A missing path, or one elsewhere, is `onboarding-invalid`; the custom item may not be named `dotai-profiles`. dotai then generates neither the skill nor the hook. When the path is in a variant, the onboarding depends on the chosen variant.
  - A skill or subagent named `dotai-profiles` without `onboarding.custom` is an error, `onboarding-name-reserved`.
  - `dotai validate` warns with `onboarding-replaced`: dotai's onboarding is designed to propose profiles this way, with the user's approval, and replacing it is at the author's own risk.
  - `dotai install` shows the same warning, naming the pack and the item, in the CLI and UI plans before the installer confirms; `--yes` still prints it, and `install --json` reports it.
  - `validate --json` adds `pack.onboarding`, `{"mode": "dotai" | "custom", "path"}`, with `path` null in `dotai` mode ([CLI-JSON](../CLI-JSON.md)).
  - Migration: a pack that ships an `onboarding` subagent without `custom` gets both; `validate` warns with `onboarding-duplicate`, suggesting removing it or declaring it as `custom`.
- **Per harness.**
  - **Claude Code:** `SessionStart` fired in `-p` sessions ([HARNESS-NOTES](../HARNESS-NOTES.md#hook-commands)); by its documentation, not checked here, plain stdout joins the context. The hook matches `startup` only, provisionally, until rulesync's rendering of the matcher is checked.
  - **agy:** stdout must be JSON; plain text never reaches the model (1.4.12). The hook prints `{"injectSteps":[{"systemMessage":{"systemMessage":"<line>"}}]}`, or nothing. It goes in `antigravity-cli.hooks.sessionStart`, which replaces the shared `sessionStart` hooks for agy ([PACK-FORMAT](../PACK-FORMAT.md#merge-rules)). When the pack already has that block, the engine appends its entry; otherwise it creates the block with the merged shared entries copied first, and reports no `hooks-shadowed` for it.
  - **OpenCode:** `sessionStart` fired through the plugin rulesync generates. With the pinned rulesync, a `postToolUse` hook that read stdin hung `write`, so the hook never reads stdin: the harness comes from its argument, the repository from its working directory. Whether the plugin delivers a hook's stdout to the model is unverified, so OpenCode support is provisional until checked.
  - agy and OpenCode have no `startup`-only filter, so a resumed or compacted session may repeat the line.

## Considered options

- **A (chosen):** dotai generates the profiling skill from the manifest and installs a session-start hook; a pack may replace both, with a warning.
- **B:** each pack ships its own onboarding, as the example subagent does. Rejected: duplicated in every pack, drifts from the engine's commands, invoked by hand.
- **C:** the engine detects and applies profiles itself. Rejected: it contradicts [0008](0008-onboarding-by-detection-and-base-profile.md).

## Consequences

- Good: every pack gets the same onboarding, triggered on its own; an unchanged set-up repository pays no context beyond the skill's description.
- Good descriptions and hints become the core of authoring a pack: they are all the skill knows about each profile. The UI's pack-creation onboarding (templates, later) explains profiles and the skill and previews the generated skill; its design is out of scope.
- Accepted cost: the fingerprint sees only tracked files and what `hints` match; untracked source and other changes are invisible to the hook until the user asks.
- Accepted cost: a directory outside git, where [0041](0041-project-scope-local-and-untracked.md) still allows apply, is never prompted and never drift-checked.
- Accepted cost: a hook process at every session start, even when silent; a pack that replaces the onboarding loses the trigger and the drift check unless it builds its own.
- On acceptance, this record supersedes the example pack's onboarding subagent, the [PACK-FORMAT](../PACK-FORMAT.md#scopes) paragraph on it, and the web UI's display of the pack's onboarding agent (`internal/app/packs.go`). It partially supersedes [0008](0008-onboarding-by-detection-and-base-profile.md) (the procedure is no longer pack content; the profiles and their evidence still are) and option D of [0041](0041-project-scope-local-and-untracked.md) (an engine-owned record decides when the agent is told to run the analysis; the engine still runs no agent). It amends [PACK-FORMAT](../PACK-FORMAT.md#manifest) (`hints` gain the fingerprint use; `onboarding` joins the manifest), PACK-FORMAT's "the engine never trusts" the marker ([Applying a pack](../PACK-FORMAT.md#applying-a-pack): trusted only to decide one line), and 0041's exclude and record rule (`dismiss` creates `.dotai/` and records it). Those records and the index are marked on acceptance.
- Pending:
  - Whether the OpenCode plugin delivers a hook's stdout to the model, and in what form.
  - A runtime check that Claude Code adds `SessionStart` stdout to the context, and that rulesync renders the `startup` matcher.
  - The skill's description text, and a size bound on its body for a pack with many profiles.
  - The hook's time bound, and `git ls-files` in very large repositories.
  - Whether a custom onboarding may keep dotai's hook, pointing at its own item.
  - The measured always-on cost of both items per harness, once `dotai doctor` exists.

## Evidence

- [PACK-FORMAT](../PACK-FORMAT.md) (manifest, "Applying a pack", the agy `injectSteps` note) and [HARNESS-NOTES](../HARNESS-NOTES.md#hook-commands).
- `examples/minimal-pack/base/.rulesync/subagents/onboarding.md`, `internal/state/project.go` (the marker), `internal/app/packs.go` (the UI's onboarding agent).
- The OpenCode stdin problem: reproduced on 2026-11-13 with the plugin rulesync 41.0.0 generates, on OpenCode 2.0.30 (a `postToolUse` hook that reads stdin hangs `write`); a fix is proposed upstream and not released. Not yet in HARNESS-NOTES.
