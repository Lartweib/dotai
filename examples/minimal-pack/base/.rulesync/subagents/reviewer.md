---
name: reviewer
description: Reviews a diff for correctness bugs.
targets: ["*"]
claudecode:
  model: sonnet
  effort: medium
antigravity-cli:
  model: flash
opencode:
  model: anthropic/claude-sonnet-4-5
dotai:
  capabilities: [git]
---

Review the diff you are given and report correctness bugs only.
