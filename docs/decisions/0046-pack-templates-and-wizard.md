---
status: accepted
date: 2026-11-17
tags: [pack, authoring, template, ui, cli, consent]
---

# 0046 · Packs start from an embedded template and a wizard

## Context

Packs start as copies: the Packs board copies one, without a command; Home points to `examples/minimal-pack`, whose onboarding subagent [0045](0045-dotai-owned-project-onboarding.md) replaced.

## Decision

Names are proposed.

- **One template**, embedded, versioned with the engine and offline, shows dotai's expected files and shape; then the author creates. Minimal, commented, harmless examples: a root rule in `base/`; a rule, command, subagent and skill; `mcp.jsonc` with no real server, `hooks.jsonc` running `echo`, `permissions.jsonc`, `dotai/settings.yaml`; a tool as `.sh` and `.ps1`; a profile with a real `description` and `hints`, a default variant and a mod. No `dotai-profiles` item, no `onboarding` subagent.
- **A wizard** asks the name, harnesses, and profiles with description and hints, previews 0045's skill and writes a valid pack via one `internal/app` use case.
- **CLI parity.** All the UI does or offers works without it. `dotai pack new <dir>` takes the same inputs as flags or an answers file, has `--json`, writes only to a new or empty directory and adds it as `pack add --dir`. `dotai pack copy` mirrors the board's copy.
- **Unchanged template files.** The binary keeps hashes, line endings normalized, of every released template file. `validate` and `pack add` warn `template-unchanged`, listing matching files; in `install` and `apply` plans this asks for the existing consent (once on a terminal; refused without one or with `--json` unless `--yes`; by digest in the UI). No new flag: `--yes` accepts what the plan asks to confirm. Uncleaned published packs are flagged too.

## Considered options

- **Wizard and template (chosen)** vs hand-edited copies.
- **One teaching template (chosen)** vs frontend, backend or other starters.
- **Example files with hash detection (chosen)** vs a minimal pack with a README of extras.
- **Hashes (chosen)** vs an in-file marker, forgotten in edits; real edits clear a hash.
- **Embedded (chosen)** vs a templates repository.

## Consequences

- Cost: kept example files ask consent at each install (inferred).
- Amends [0043](0043-local-web-ui.md) (copy command; Home points to the wizard) and [0045](0045-dotai-owned-project-onboarding.md) (pack creation).
- Pending:
  - `InstallPlan` has no coded warnings.
  - Today consent covers only foreign content and conflicts; the UI also sends `yes`.
  - The CLI's skill preview.
  - Root files such as `README.md` are ignored, even by `layer-undeclared`.

## Evidence

- `internal/cli/install.go:58,96-109`, `apply.go:45,84-97`, `pack.go:31`, `restore.go:335-340`.
- `internal/app/execute.go:43,239-253`, `install.go:50-76`, `apply.go:150-161`, `project.go:41`, `validate.go:32`, `packs.go:128-148,451`.
- `internal/server/actions.go:17-22`; `internal/pack/pack.go:140-186,223-226`; [0044](0044-git-pack-distribution.md):41.

Update 2026-11-18: implemented as [PACK-FORMAT](../PACK-FORMAT.md#creating-a-pack) and [CLI-JSON](../CLI-JSON.md) describe, with the names above. Where this record left a choice open:

- The template is `internal/packtemplate`, embedded with `go:embed`. Its MCP example server is a comment, so no server runs, and its hook runs `echo` on `stop`, whose output no harness puts in the context. `hashes.txt` keeps, per template version, the hash of every file the wizard can write below the pack's top, `dotai/settings.yaml` once for each choice of its harness blocks; a test fails when the template changes without a new version's hashes appended. The manifest and the README at the top are not hashed: they are not installed.
- `template-unchanged` is one `validate` warning per file, and one `pack add` warning listing them. An install plan warns about the files of the base and the variant, an apply plan about those of the applied profiles and their mods, each with `item` the file's path in the pack; only those need consent.
- The wizard's names of profiles and mods follow the rule for mod names, as the pack name does, so that each is safe as a folder. The plan of `pack new` leaves out its own `template-unchanged` warnings, since every example file is one.
- The UI's wizard is carried out by the plan's digest, which is the consent, as an update of a git pack is: `pack new` has nothing for `--yes` to accept. Install and apply plans take the digest and `yes` as before.
- `pack copy` without a target copies to the first free `<name>-copy` next to the pack, as the Packs board offers; the API's copy now answers as `pack copy`.
- The profiling skill of [0045](0045-dotai-owned-project-onboarding.md) now says to add `--yes` only for what the plan asks to confirm and the user approved, template files included, not only conflicts.
- Pending items: `InstallPlan` already had coded warnings, which `template-unchanged` reuses, as `ProjectPlan` does (closed); consent now covers template files besides foreign content and conflicts, with the UI's `yes` (closed); `pack new --dry-run` previews the skill on the command line (closed); root files such as `README.md` are still ignored by the loader and by `layer-undeclared`, and not hashed (open).

Update 2026-11-18: `pack new` and the wizard ask the compatible and the required harnesses, as [0047](0047-harness-selection-and-preview-harnesses.md) decides.
