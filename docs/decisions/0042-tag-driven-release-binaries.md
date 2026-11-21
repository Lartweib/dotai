---
status: accepted
date: 2026-11-03
tags: [engine, release, ci, supply-chain, versioning]
---

# 0042 · Tag-driven release binaries built by a script and published with gh

## Context

Roadmap PR 12 ships `dotai` as a binary per platform, so that a user can install it without a Go toolchain. [0004](0004-installer-language-and-shape.md) makes the engine a single Go binary. The engine runs rulesync, and [0029](0029-rulesync-checksums-embedded-in-the-engine.md) embeds rulesync checksums for five platforms only: Windows x64, Linux x64 and arm64, macOS x64 and arm64. A pack declares the engine versions it supports as a SemVer range, and the example pack declares `engine: ">=0.1.0"` ([PACK-FORMAT](../PACK-FORMAT.md)).

## Decision

- **Trigger.** Pushing a tag `vX.Y.Z`, optionally with a SemVer 2.0.0 pre-release suffix such as `v0.2.0-rc.1` and without build metadata, runs `.github/workflows/release.yml`. It first runs the whole CI workflow as a reusable workflow, then checks that the tagged commit is an ancestor of `origin/main` and fails otherwise, then builds and publishes. A tag with a suffix is published as a pre-release.
- **Tooling.** `scripts/release.sh <tag> <out-dir>` builds every target with plain `go build` (`CGO_ENABLED=0`, `-trimpath`, `-s -w`) and writes the archives and `SHA256SUMS`. It needs GNU tools (GNU tar and coreutils, Info-ZIP zip) and is meant for Linux, as in CI: a preflight checks them before anything is built. It refuses a non-empty output directory. CI runs its `--self-test` of the tag pattern.
- **Publishing.** The release is created as a draft, every file is uploaded, and only then is it published, so a failed run never publishes a partial release. A rerun of the tag completes its draft; it fails, changing nothing, when a published release exists.
- **Platforms.** The five platforms of the rulesync checksum table, `windows/amd64`, `linux/amd64`, `linux/arm64`, `darwin/amd64` and `darwin/arm64`. A platform is added to both together.
- **Versioning.** SemVer tags. The tag is stamped as the version (`-X .../internal/app.version=<tag>`), so `dotai version` prints `v0.1.0`, as `go install ...@v0.1.0` would. Commit and build date come from the build info: the commit and its time. The first release is proposed as `v0.1.0`, the lowest version the example pack accepts.
- **Toolchain.** The release workflow pins the exact Go version (`GO_VERSION`), which the release notes and `dotai version` report.
- **Archives and checksums.** `dotai_<version>_<os>_<arch>.tar.gz`, or `.zip` for Windows, each with the binary, `LICENSE` and `README.md`, with entries dated at the commit time and zip run in UTC. `SHA256SUMS` lists every archive.
- **Supply chain.** Every action is pinned to a full commit SHA, the release job alone has `contents: write`, and setup-go runs without a module cache. A build provenance attestation over `SHA256SUMS` is created when the repository is public; attestations for a private repository need a paid plan, so a private release ships without one.

## Considered options

- **A (chosen):** a build script with `go build` and `gh release`. Five targets need a loop, not a tool, and the script adds no dependency to pin or audit.
- **B:** GoReleaser, pinned. Rejected for now: a second tool and its configuration format for what one short script does. It is worth revisiting for package managers (Homebrew, Scoop) or signing.
- **C:** a build matrix with one runner per target. Rejected: Go cross-compiles, and a matrix needs artifact upload and download between jobs.

## Consequences

- Good: the binaries of a tag can be rebuilt bit for bit from the same commit with the same Go version, which the release notes record. The archives, and so `SHA256SUMS`, are only identical when gzip, tar and zip are also the same versions; that is not guaranteed across runner images.
- Good: a user verifies a download against `SHA256SUMS`, and against the attestation once the repository is public.
- Bad or accepted cost: `SHA256SUMS` lives in the same release as the archives, so on its own it detects a corrupt download, not a compromised release (the reason for [0029](0029-rulesync-checksums-embedded-in-the-engine.md)). The attestation covers that case.
- Bad or accepted cost: the binaries are not code-signed; macOS Gatekeeper and Windows SmartScreen may warn on first run.
- Accepted: the ancestry check only stops a tag outside `main`. Anyone who can push a tag can still release a commit of `main`. Maintainers should also add a tag ruleset that limits who creates `v*` tags, or a `release` environment with required reviewers on the release job; both are repository settings, decided outside this record.
- Pending: the first release is not cut yet; its version number, and whether to publish while the repository is private, are still open.

## Evidence

- `scripts/release.sh` and `.github/workflows/release.yml`.
- `cmd/dotai/version_test.go` builds `dotai` with the linker flag of the script and checks the printed version.
- Local check on Windows with Git Bash, 2026-11-03: all five targets built and the Windows binary printed `dotai v0.1.0`. zip is not installed there, so the preflight refused until a stand-in for zip was put on `PATH`.

Update 2026-11-18: accepted. `.github/workflows/release.yml` and `scripts/release.sh` implement this decision as written, and CI runs the script's `--self-test` on every push and pull request. The first release is cut from a `vX.Y.Z` tag on `main`; its version follows the Versioning point above.

Update 2026-11-20: each archive also holds `THIRD_PARTY_NOTICES`, which `scripts/release.sh` generates for that target from the module graph: the license texts of the Go standard library, of every module linked into the binary and of the fonts embedded in the web UI. CI runs the script's `--check-notices`, which fails when a linked module has no license file.

Update 2026-11-22: [0052](0052-release-please-owns-versions-and-changelog.md) decides that release-please creates the tag and a draft release, and `release.yml` completes and publishes that draft; a `v*` tag pushed by a person still works as this record describes. The build, the checksums, the attestation and the Pending note on the first release are unchanged.
