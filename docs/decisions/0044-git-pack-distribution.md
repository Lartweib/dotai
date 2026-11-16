---
status: proposed
date: 2026-11-16
tags: [pack, distribution, git, registry, security, cli]
---

# 0044 · Packs are distributed as git repositories, fetched with the user's git

## Context

A pack lives in its own repository ([0015](0015-pack-in-its-own-repository.md)); today a user clones it by hand and imports the directory. The registry, `~/.dotai/packs.json` (`dotai.packs/v1`), lists absolute local paths, and no CLI command edits it yet ([0043](0043-local-web-ui.md)). Nothing records a pack's source or commit, yet its hooks, MCP servers, settings commands, tools and mods run on the user's machine.

`dotai validate` reads only the manifest, `base/`, the declared layers and `mods/`: an undeclared `profiles/rust/` is ignored, and a link outside those trees is not reported.

## Decision

The commands and the error and warning codes below are proposed names.

- **Two sources.** A git repository, or a local directory for authoring. No archive format: an extracted archive is added as a directory.
- **The user's git.** dotai runs `git` from `PATH`, version 2.41 or later, the first release with `--attr-source`. A missing git (`git-missing`) or an older one fails, naming it.
- **Commands**, each with `--json`:
  - `dotai pack add <git-url> [--ref <ref>]`. The ref is a tag, a branch or a full commit SHA (an abbreviated one cannot be fetched and is refused); a flag, since `url@ref` would clash with `git@host:` URLs. Without `--ref`, dotai fetches `HEAD`, the default branch, and records the resolved branch ref.
  - `dotai pack add --dir <path>` adds a local directory; `--ref` with `--dir` is refused. The positional argument is always a git URL, so a Windows path is never read as `host:path`.
  - `dotai pack update <id>` (git packs only) fetches the recorded full ref; `dotai pack remove <id>`. Both take the identity.
  - Only `https://`, `ssh://` and `user@host:path` URLs are accepted, and no URL or ref starting with `-`; the URL is passed after `--`.
- **Fetch without running the repository.** Into a temporary sibling of `~/.dotai/packs/<identity>`, placed later by a rename: `git init --template=<empty dir> -b main`, `git fetch --depth 1 --no-tags --no-recurse-submodules -- <url> <ref>`, `git checkout --no-recurse-submodules --detach FETCH_HEAD`. A clone never receives the remote's hooks or `.git/config`; the options guard against the user's configuration and the pack's attributes, on every git call, since `-c` settings do not persist:
  - Hooks: `--template=<empty dir>` (overrides `init.templateDir`) and `-c core.hooksPath=<empty dir>`, since reference-transaction hooks fire during init, fetch and checkout.
  - Attributes: `--attr-source=4b825dc642cb6eb9a060e54bf8d69288fbee4904` (the empty tree), `-c core.attributesFile=` (which `--attr-source` alone leaves in force) and `GIT_ATTR_NOSYSTEM=1`. The checkout then equals the blobs. `-c core.autocrlf=false -c core.eol=lf` and `GIT_LFS_SKIP_SMUDGE=1` remain as defense in depth.
  - Transport and objects: `-c protocol.allow=never -c protocol.https.allow=always -c protocol.ssh.allow=always`; `-c transfer.fsckObjects=true`, which refuses a `.git` tree entry at the fetch; `-c core.fsmonitor=false`; on Windows, `-c core.longpaths=true`.
  - Prompts: every git call has a timeout, 10 minutes for a fetch (a proposed value). The CLI leaves git's prompts on. Calls from the UI close stdin and set `GIT_TERMINAL_PROMPT=0`, `GCM_INTERACTIVE=never`, `-c credential.interactive=false` and `GIT_SSH_COMMAND="ssh -o BatchMode=yes"`, since SSH ignores `GIT_TERMINAL_PROMPT`; this overrides the user's `core.sshCommand`.
- **Fetch by commit.** A host that refuses a fetch by SHA fails the command with `fetch-by-commit-refused`, naming the host; no fallback to a full clone.
- **Check the tree, then validate.** `git ls-tree -r -l -z` of the commit is the source of truth: a link (`120000`), a submodule (`160000`) or a path with a `.git` component is refused, and so is a tree over the bounds of a pack copy (10,000 files, 64 MiB). Not the disk: on Windows checkout writes a link as a plain file or, with `core.symlinks=true` and no Developer Mode, exits 0 with the file missing. Then `dotai validate` runs. On any failure the temporary directory is deleted, nothing is registered, and the failures are reported.
- **Identity from the source.** A git pack's identity is `<host>/<path>` of its URL, with user, port, trailing `.git` and `/` removed, lowercased: `https://github.com/Acme/My-Pack.git` and `git@github.com:acme/my-pack` are both `github.com/acme/my-pack`. URLs that differ only by user, port or case get the same identity. A host named `local`, and a segment unsafe as a directory name on some OS (a Windows device name or reserved character, a trailing dot), are refused. For a git pack, the manifest `name` is only a display label. A local pack's identity is `local/<name>`: re-adding the listed directory is a no-op, and another directory with a taken name is refused. A new local pack's name follows the rule for mod names; an existing entry or project whose name breaks it is kept as is.
- **Add and register.** The same identity is always the same pack: adding it again, from any URL form or case, is an update, with its diff and confirmation, and the registry keeps the URL last used. Adding it with another `--ref` moves the pack to that ref and replaces the recorded one. A branch is accepted with a warning (`ref-branch`) that it can move and that the installed commit stays fixed until `dotai pack update`. A renamed or transferred repository keeps its stored identity; a new identity that resolves to an existing entry's commit gets the warning `pack-same-commit`. The entry records the URL, the ref asked for, its full name from `FETCH_HEAD` (`refs/tags/...` or `refs/heads/...`, as a tag silently wins over a same-name branch) and the commit (`rev-parse FETCH_HEAD^{commit}`, as an annotated tag puts the tag object there), written atomically under `~/.dotai/lock`.
- **Update.** dotai fetches only on `add` and `update`; an unreachable remote fails the command, naming it, and leaves the pack untouched. After the fetch and checks, dotai shows what changed in executable content: `.rulesync/hooks.jsonc`, `.rulesync/mcp.jsonc` and `dotai/settings.yaml` of every layer, `dotai/tools/` and `mods/`. Once the user confirms (a prompt or `--yes`; in the UI, the digest of what was shown, [0043](0043-local-web-ui.md)), dotai moves the old directory aside to a temporary sibling, renames the checked clone into place, writes the registry, then deletes the old one; on any failure the old one is moved back. Installs and projects change only when `dotai install` or `dotai apply` runs again.
- **Remove.** `pack remove` drops the entry; it deletes the directory only for a git entry at `~/.dotai/packs/<identity>`, never a local one.
- **What keys on the identity.** The registry and `~/.dotai/packs/<identity>/`. The install state, backups and project records add `id` beside `name`, `version` and `path`, and the reuse of a saved harness selection (`internal/app/detect.go`) compares identities, not names. A project needs one path segment: the flat form is the identity with each `/` replaced by `__`, and a local pack's is its bare name, so existing projects keep their paths. A legacy local name holding `__` gets the `validate` warning `flat-name-ambiguous`, since it could collide with a git pack's flat form. The flat form names the reserved `.dotai/packs/<flat>/` of [0041](0041-project-scope-local-and-untracked.md) and the Claude Code rules file `.claude/rules/<flat>.md`; `apply --json` reports the reserved directory to the onboarding agent. The display name stays in output, `validate` and a mod's `plugin.json` description; a plugin keeps its mod name. CLI-JSON adds `id` where a pack is listed. Entries and records without an `id` read as `local/<name>`; the rest of the migration is left to the implementation.
- **Registry schema `dotai.packs/v2`.** An entry is `{"id", "path", "source": {"url", "ref", "fullRef", "commit"}}`; a local pack has no `source`. A new schema, because the v1 reader drops unknown fields on its rewrite, so an older dotai would erase every `source`; it refuses v2 with `registry-invalid` instead. The new engine reads v1 and writes v2.
- **The manifest is the routes file.** `dotai.yaml` declares every layer, so the structure can be checked:
  - A JSON Schema, `schemas/dotai.pack.v1.schema.json`, flags errors in editors; `validate` stays the authority, and a test keeps the two in step.
  - `validate` warns (`layer-undeclared`) on a directory that is not a declared layer, inside one or on the path to one, such as `profiles/rust/` beside a declared `profiles/web/`. The allowed set is `base/`, `mods/` and `.git`; files are not checked. A warning keeps today's valid packs valid.

## Considered options

- **A (chosen):** git repositories through the user's git, and local directories for authoring.
- **B:** zip or tar archives. Rejected: no update path, no provenance, and no inspection by hand, the reason [0018](0018-install-modes-and-mandatory-backup.md) gave for backups.
- **C:** a Go git library. Rejected: a larger binary, and credentials and SSH that would not behave as the user's git does.
- **D:** a central index service. Out of scope for v1; possible later over the same `source` fields.

## Consequences

- Good: private repositories work through the user's credentials, SSH keys and proxy; dotai stores no secret.
- Accepted cost: dotai does not judge a pack's code. The pinned commit and the update diff are the trust boundary; on the first add, the user trusts the source.
- Accepted cost: a pack's `.gitattributes` has no effect, so an author who wants CRLF commits CRLF bytes. In return, what `validate` checks is what the diff shows and what is installed. `transfer.fsckObjects` may rarely refuse an old repository with malformed objects.
- Accepted cost: a shallow clone has one commit of history; an author works in their own clone, added with `--dir`.
- Accepted cost: repositories that differ only by case, on a host with case-sensitive paths, or only by port share an identity and would be merged. This is rare, and the update diff and confirmation show the content before anything is replaced. In theory `x/y__z` and `x__y/z` share a flat form.
- Accepted cost: from the UI, a repository behind an unknown host key, or a passphrase-protected key without an agent, cannot be added; the user runs the CLI once.
- Accepted cost: after the first v2 write, an older dotai cannot read the registry.
- Risk: the nested directory and the flat form lengthen Windows paths, which harnesses may not handle even when git does.
- Pending: once this record is accepted, it partially supersedes [0043](0043-local-web-ui.md) on the pack registry: schema v2, the CLI edits it, and `remove` deletes a git pack's clone. It also amends [0041](0041-project-scope-local-and-untracked.md) (the reserved `.dotai/packs/<pack>/` takes the flat identity) and [PACK-FORMAT](../PACK-FORMAT.md#manifest) (`name` and `version` are no longer the identity); those records and the index are marked on acceptance.
- Pending:
  - Fetch by commit on hosts other than github.com is untested.
  - How the UI presents add, update and the diff of executable content.
  - Extra names the root check allows, such as `.github/`.
  - The URL editors use for the JSON Schema while the repository is private.

## Evidence

- `internal/app/packs.go` (the v1 registry, its rewrite, the copy bounds) and `internal/pack/pack.go` (`LoadWith`).
- `--attr-source` first appears in the git 2.41.0 release notes (read 2026-11-16); 2.41 itself was not tested.
- Test of the fetch flow, 2026-11-16: Windows (git 2.51.0.windows.1, git-lfs 3.7.0) and Debian 13 (git 2.47.3, git-lfs 3.6.1), empty global configuration, `GIT_CONFIG_NOSYSTEM=1`, crafted local repositories and anonymous github.com fetches. The git behavior stated above was observed, except the timeout, `core.longpaths` and the UI prompt settings other than `GIT_TERMINAL_PROMPT`; also:
  - `eol=crlf`, `working-tree-encoding`, `ident` and a user filter change the checkout despite `core.autocrlf=false`; the three attribute options stop all four. `GIT_LFS_SKIP_SMUDGE=1` covers `filter.lfs.process`.
  - Without `transfer.fsckObjects`, a tree entry `.git/config` passes the fetch and only the checkout aborts; with it, the fetch refuses it (`hasDotgit`).
  - A full SHA, tip or not, fetches with `--depth 1`, protocol v0 included; a 12-character SHA does not.
  - `ssh://-oProxyCommand=...` is refused ("strange hostname"); an option after `--` is read as a refspec. `GIT_TERMINAL_PROMPT=0` fails a missing https credential (Linux). Submodules stay out with `submodule.recurse=true`. `core.fsmonitor=false` is harmless.
  - Not tested: other hosts, Git Credential Manager prompts, long paths, `safe.directory`.
