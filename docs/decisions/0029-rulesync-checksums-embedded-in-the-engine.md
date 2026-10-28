---
status: accepted
date: 2026-10-28
tags: [engine, rulesync, security, supply-chain]
---

# 0029 · rulesync checksums are embedded in the engine

## Context

[0021](0021-go-engine-orchestrates-rulesync.md) pins rulesync to an exact version: dotai downloads the official binary and verifies its checksum. It does not say where the expected checksum comes from. Each rulesync release publishes a `SHA256SUMS` file next to its binaries, but that file lives in the same release as the binaries it describes.

## Decision

The engine embeds the SHA-256 of every rulesync release asset it supports, per version and platform. It verifies each downloaded or cached binary against that table and never trusts a `SHA256SUMS` file fetched at runtime.

A pack can only pin a rulesync version the engine knows; `dotai validate` reports any other version. Supporting a new rulesync version is an engine change, gated by the tests over the example pack.

## Considered options

- **A (chosen):** checksums embedded in the engine.
- **B:** verify against the release's own `SHA256SUMS`. Rejected: it only detects a corrupt download. A compromised release can replace the binaries and the checksums together.

## Consequences

- Refines [0021](0021-go-engine-orchestrates-rulesync.md).
- Good: a compromised or re-uploaded release cannot run on a user's machine; the trust anchor is the engine binary the user installed.
- Bad or accepted cost: each new rulesync version needs an engine release, and a pack cannot move ahead of the engine.

## Evidence

- rulesync v41.0.0 release assets and `SHA256SUMS`, checked 2026-10-28: the Windows x64 binary was downloaded and hashed independently.
