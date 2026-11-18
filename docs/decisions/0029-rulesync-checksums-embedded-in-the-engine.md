---
status: accepted
date: 2026-10-28
tags: [engine, rulesync, security, supply-chain]
---

# 0029 · rulesync checksums are embedded in the engine

## Context

[0021](0021-go-engine-orchestrates-rulesync.md) pins rulesync to an exact version: dotai downloads the official binary and verifies its checksum. It does not say where the expected checksum comes from, and [0028](0028-plan-centric-hexagonal-engine.md) says the `rulesync` package verifies `SHA256SUMS`. Each rulesync release publishes that file next to its binaries, so it lives in the same release as the binaries it describes.

## Decision

The engine embeds the SHA-256 of every rulesync release asset it supports, per version and platform. It verifies each downloaded or cached binary against that table and never trusts a `SHA256SUMS` file fetched at runtime.

A pack can only pin a rulesync version the engine knows; `dotai validate` reports any other version. Supporting a new rulesync version is an engine change, gated by the tests over the example pack.

### Adding a rulesync version

1. Download every asset of the supported platforms and hash each one independently.
2. Compare each hash with the release's `SHA256SUMS` and with the asset digest that the GitHub API reports. All three must agree; if any differs, do not add the version.
3. Add the hashes to the table in `internal/rulesync` and run the tests over the example pack.

## Considered options

- **A (chosen):** checksums embedded in the engine.
- **B:** verify against the release's own `SHA256SUMS`. Rejected: it only detects a corrupt download. A compromised release can replace the binaries and the checksums together.

## Consequences

- Refines [0021](0021-go-engine-orchestrates-rulesync.md) and partially supersedes [0028](0028-plan-centric-hexagonal-engine.md): the `rulesync` package does not verify a fetched `SHA256SUMS`.
- Good: a release that is compromised or re-uploaded after a version was embedded cannot run on a user's machine; the trust anchor is the engine binary the user installed.
- Bad or accepted cost: each new rulesync version needs an engine release, and a pack cannot move ahead of the engine.
- Accepted: the first embedding trusts the release as it is at that moment. The procedure above detects a corrupt or inconsistent release, not one that was already compromised when it was hashed.
- The cached binary is verified again right before it is executed, since the cache is writable by the user; `dotai stage` does this each time it runs rulesync.

## Evidence

- rulesync v41.0.0 release assets, checked 2026-10-28: all five binaries (Windows x64, Linux x64 and arm64, macOS x64 and arm64) were downloaded and hashed independently, and each hash matched both the release's `SHA256SUMS` and the GitHub API digest.

Update 2026-11-18: the engine embeds rulesync 43.9.2 in place of 41.0.0, so a pack pins 43.9.2. All five v43.9.2 binaries were downloaded from the release's download URLs and hashed independently, and each hash matched both the release's `SHA256SUMS` and the asset digest the GitHub API reports. The asset names are unchanged.
