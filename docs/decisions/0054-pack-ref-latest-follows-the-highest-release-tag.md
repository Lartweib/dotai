---
status: proposed
date: 2026-11-22
tags: [pack, distribution, git, registry, cli, web-ui]
---

# 0054 · `--ref latest` follows a pack's highest release tag

## Context

[0044](0044-git-pack-distribution.md) records the ref a pack was added with and fetches it again, by its full name, on `dotai pack update`. A tag never moves, so a pack added at `v1.2.0` stays there, and a user who wants each release has to read the repository and re-add the pack at the new tag by hand. Without `--ref` the default branch is followed, which is not a release. The wish: `dotai pack add <url> --ref latest` follows the pack's releases, and `dotai pack update` moves it forward.

## Decision

- **`latest` is a ref.** `--ref latest` (the web UI's "Latest release") resolves to the highest SemVer tag of the form `vX.Y.Z`: a leading `v` and three numbers without leading zeros. A pre-release (`v1.2.0-rc.1`), a build suffix (`v1.2.0+5`), a short or long version (`v1`, `v1.2`, `v1.2.3.4`), a tag without the `v` and any other name are ignored, never an error.
- **Read with the user's git.** `git ls-remote --tags --refs -- <url>`, with the same options, credentials and time limit as every other call ([0044](0044-git-pack-distribution.md)): no GitHub API and no token. The numbers are compared as numbers, so `v1.10.0` is above `v1.9.0`.
- **Annotated and lightweight tags both count.** The highest tag is fetched by its full name, `refs/tags/vX.Y.Z`, and the commit installed is `rev-parse FETCH_HEAD^{commit}`, which peels an annotated tag to its commit, as it does for any tag ref. `ls-remote --refs` leaves the peeled `^{}` lines out, so a tag is listed once whatever its kind.
- **What the registry records.** The source keeps `ref: "latest"`, and `fullRef` and `commit` are the release resolved: `refs/tags/v0.1.0` and its commit. No field is added and the schema stays `dotai.packs/v2`. A pack tracks `latest` when `ref` is `latest` and `fullRef` is a `refs/tags/v...` tag; a `latest` recorded before this decision, resolved to `refs/heads/latest` or `refs/tags/latest`, keeps being fetched by that full name. A branch or a tag really named `latest` is given from now on as `--ref refs/heads/latest` or `--ref refs/tags/latest`. Giving `--ref latest` on a pack already added is always the release lookup, so such a legacy pack can move to release tracking; `pack update` alone never changes what a recorded ref means.
- **Update.** `dotai pack update` and the web UI's update look the highest release up again. A higher one is shown as any update is, with the diff of executable content, and installed only with consent; the same one reports `unchanged` ("already on the latest release"); a pre-release or a commit past the tag changes nothing. A tag moved to another commit at the same version shows as an update, with the same consent.
- **No silent downgrade.** If the highest tag is now below the one installed (the release followed was deleted or recreated lower), the update is refused with `latest-downgrade`, naming both tags, with `--yes` and in the web UI too; going down on purpose is `--ref <tag>`, which stops following `latest`. The prompt of an update names the tags it moves between, `v1.3.0 (…)` to `v1.4.0 (…)`.
- **A bad highest tag blocks.** A highest `vX.Y.Z` tag that does not point to a commit is a `fetch-failed` naming the tag; dotai does not fall back to the next one, since a wrong release is worse than none, so the repository fixes the tag, or the user gives `--ref <tag>`.
- **No release tag** is `no-release-tag`, with the message pointing to `--ref <branch|tag|sha>` or to leaving `--ref` out for the default branch. Nothing is fetched or registered.
- **Unchanged.** An explicit tag, branch or commit keeps today's behavior, and the default without `--ref` stays the default branch. Adding a pack again with `--ref latest` moves it to the highest release, as a new `--ref` always did, and adding it again without `--ref` keeps following.
- **Shown as** `latest → v0.1.0`, in `pack list`, `pack add`, `pack update` and the web UI's boards.

## Considered options

- **A (chosen):** resolve `latest` with `git ls-remote --tags`, record it as a ref.
- **B:** the GitHub releases API. Rejected: it needs the network client, a token for private repositories and rate limits, and a release can exist without a SemVer tag. Reading the tags needs only what dotai already runs.
- **C:** a new `tracks: latest` field in the registry. Rejected: `ref` already holds what the user asked for, and the resolved tag is `fullRef`; a second field could disagree with them.
- **D:** accept pre-releases or tags without the `v`. Rejected: `latest` should be a release a user can rely on, and the rule must fit one sentence; `--ref <tag>` covers the rest.

## Consequences

- Good: a pack follows its releases with one `pack update` and the same consent, diff and rollback as before; no new credentials or network paths.
- Good: the data is the same shape as for a tag, so a client that reads `ref` and `fullRef` keeps working.
- Accepted cost: a repository that does not tag releases as `vX.Y.Z` cannot use `latest`; it gets `no-release-tag`.
- Accepted cost: the name `latest` is taken as a ref; the full name is the way to a branch or tag called that.
- Accepted cost: a deleted release tag stops `pack update` of the packs that follow it, until the user gives `--ref <tag>` or the tag is back; refusing is chosen over installing an older version without being asked.
- Accepted cost: the web UI's "Default branch" on a pack already added keeps the recorded ref, as `--ref` left out does; the add board says so.

## Evidence

- `internal/app/packgit.go` (`resolveRef`, `latestReleaseTag`), `internal/app/packlatest_test.go`, `packlatest_update_test.go` and `packlatest_internal_test.go` (local repositories with tags: the highest of annotated and lightweight, pre-releases and other names skipped, update, no newer release, no tag, the downgrade refusal and the explicit `--ref` down, a legacy `latest` branch, a tag of a blob, numbers beyond any integer).
- `git ls-remote --tags --refs` lists `refs/tags/<name>` once per tag, without the `^{}` lines (git-ls-remote documentation).
