---
status: accepted
date: 2026-10-19
tags: [engine, scope, harnesses]
---

# 0001 · Build an own configuration engine for AI coding harnesses

## Context

Working daily with several AI coding harnesses shows a set of needs that existing configuration setups do not cover together:

- **One source, several harnesses.** The same rules, skills, agents and MCP servers should reach Claude Code, OpenCode and Antigravity (agy) without being maintained by hand for each one.
- **Per-project configuration.** A global install loads skills and agents in every project, including the ones a project never uses, and their descriptions are paid for on every request.
- **A small always-on context.** Large orchestration or review protocols in the main rules file are paid for on every request, subagents included.
- **Workflows that fit the task.** A single heavyweight, phase-based workflow for every change tends to give way to lighter ones (briefs with phases, `Workflow`, `/goal`) that depend on the task.
- **Not redefining the setup for each new project.**

Cost savings do not motivate this decision: they come from the model policy and the loop caps, which are pack content.

## Decision

Build an own engine that configures Claude Code, OpenCode and agy from a single source.

## Considered options

- **A (chosen):** own engine from a single source.
- **B:** adopt an existing, workflow-centric configuration setup. Rejected because of fit to these needs, not because of cost.
- **C:** configure each harness by hand. Rejected: it duplicates work across harnesses and projects, and drifts.

## Consequences

- Cost savings do not depend on this decision; they come from the pack's cost policy.

## Evidence

- Internal research (not published): workflow alternatives and component catalog.
- Internal research (not published): discovery of harness capabilities.
