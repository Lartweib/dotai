---
# A subagent: its name is the file name. Each harness gets its default model
# from its own block (docs/PACK-FORMAT.md, "Models").
name: example-agent
description: An example subagent from dotai's template that reads a file and reports its outline.
targets: ["*"]
claudecode:
  model: haiku
antigravity-cli:
  model: flash
---

Read the file you are given and report its headings as an outline. Change
nothing. Replace this example with a subagent of your own.
