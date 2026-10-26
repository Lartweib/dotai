---
status: accepted
date: 2026-10-20
tags: [onboarding, profiles, analyzer]
---

# 0008 · Onboarding by detection and base profile

> **Partially superseded by [0023](0023-flat-profiles-and-layer-merge-rules.md): sub-profiles.** Profiles are now flat and combinable. Still in force: detection by content, minimal questions, approval before applying, and installation per profile.

## Context

Skills and agents installed globally are paid for on every request, even in projects that never use them.

## Decision

Onboarding detects the stack by content (not by path), asks the minimum, then proposes and applies a profile with its sub-profiles. Skills and agents are installed per profile, never globally.

## Considered options

- **A (chosen):** detection by content, installation per profile.
- **B:** install skills and agents globally. Rejected: unused items are paid for on every request.
- **C:** detect the stack by path. Rejected; the sources do not record the reason.

## Consequences

The sources record no additional consequences. The onboarding agent and the mapping from project types to profiles are pack content.

## Evidence

- Internal research (not published): component catalog.
