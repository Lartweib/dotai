---
status: proposed
date: 2026-11-03
tags: [engine, release, ci, supply-chain, versioning]
---

# 0042 · Tag-driven release binaries built by a script and published with gh

## Context

Roadmap PR 12 ships `dotai` as a binary per platform, so that a user can install it without a Go toolchain. [0004](0004-installer-language-and-shape.md) makes the engine a single Go binary. The engine runs rulesync, and [0029](0029-rulesync-checksums-embedded-in-the-engine.md) embeds rulesync checksums for five platforms only: Windows x64, Linux x64 and arm64, macOS x64 and arm64. A pack declares the engine versions it supports as a SemVer range, and the example pack declares `engine: ">=0.1.0"` ([PACK-FORMAT](../PACK-FORMAT.md)). `dotai version` already reads a version set with `-ldflags -X`, and falls back to the Go build info, then to `dev`.

## Decision

- **Trigger.** Pushing a tag `vX.Y.Z` (optionally with a SemVer pre-release suffix, such as `v0.2.0-rc.1`) runs `.github/workflows/release.yml`. It first runs the whole CI workflow as a reusable workflow, then builds and publishes. A tag with a suffix is published as a pre-release.
- **Tooling.** `scripts/release.sh <tag> <out-dir>` builds every target with plain `go build` (`CGO_ENABLED=0`, `-trimpath`, `-s -w`), writes the archives and `SHA256SUMS`. The workflow runs that script and publishes with `gh release create`. The same script runs locally.
- **Platforms.** The five platforms of the rulesync checksum table, `windows/amd64`, `linux/amd64`, `linux/arm64`, `darwin/amd64` and `darwin/arm64`. A platform is added to both tables together.
- **Versioning.** SemVer tags. The tag is stamped as the version (`-X .../internal/app.version=<tag>`), so `dotai version` prints `v0.1.0`, as `go install ...@v0.1.0` would. Commit and build date come from the build info: the commit and its time, so a rebuild of a tag gives the same output. The first release is proposed as `v0.1.0`, the lowest version the example pack accepts.
- **Archives and checksums.** `dotai_<version>_<os>_<arch>.tar.gz`, or `.zip` for Windows, each with the binary, `LICENSE` and `README.md`, with entries dated at the commit time. `SHA256SUMS` lists every archive.
- **Supply chain.** Every action is pinned to a full commit SHA, the release job alone has `contents: write`, and setup-go runs without a module cache. A build provenance attestation over `SHA256SUMS` is created when the repository is public; attestations for a private repository need a paid plan, so a private release ships without one.

## Considered options

- **A (chosen):** a build script with `go build` and `gh release`. Five targets need a loop, not a tool; the script runs the same on a laptop, and adds no dependency to pin or audit.
- **B:** GoReleaser, pinned. Rejected for now: a second tool and its configuration format for what one short script does. It is worth revisiting for package managers (Homebrew, Scoop) or signing.
- **C:** a build matrix with one runner per target. Rejected: Go cross-compiles, and a matrix needs artifact upload and download between jobs.

## Consequences

- Good: a release is reproducible from its tag with the same Go version, which `dotai version` prints.
- Good: a user verifies a download against `SHA256SUMS`, and against the attestation once the repository is public.
- Bad or accepted cost: `SHA256SUMS` lives in the same release as the archives, so on its own it detects a corrupt download, not a compromised release (the reason for [0029](0029-rulesync-checksums-embedded-in-the-engine.md)). The attestation covers that case.
- Bad or accepted cost: the binaries are not code-signed; macOS Gatekeeper and Windows SmartScreen may warn on first run.
- Pending: the first version number, and whether to publish while the repository is private.

## Evidence

- `scripts/release.sh` and `.github/workflows/release.yml`.
- `cmd/dotai/version_test.go` builds `dotai` with the linker flag of the script and checks the printed version.
- Local check on Windows, 2026-11-03: all five targets built, the Windows binary printed `dotai v0.1.0`, and two runs produced byte-identical `.tar.gz` archives (zip is not installed on that machine, so the Windows archive was made with a stand-in).
