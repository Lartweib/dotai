# A dotai pack

[![dotai pack](https://raw.githubusercontent.com/lartweib/dotai/main/docs/images/badge-dotai-pack.svg)](https://github.com/lartweib/dotai)

This pack started from dotai's template. It shows the files and folders dotai
expects, in the shape `dotai validate` accepts; what the pack holds from here is
yours. The format is in dotai's `docs/PACK-FORMAT.md`. The badge above marks
the repository as a dotai pack; keep it or remove it.

```
dotai.yaml                 the manifest: name, harnesses, profiles, variants
base/                      always installed, at user level
  .rulesync/               rendered by rulesync for each harness
    rules/                 a root rule and named rules
    commands/              slash commands
    subagents/             subagents
    skills/<name>/SKILL.md skills
    mcp.jsonc              MCP servers
    hooks.jsonc            hooks
    permissions.jsonc      permissions
  dotai/
    settings.yaml          settings rulesync does not cover, per harness
    tools/                 scripts the layer installs, as .sh and .ps1
variants/default/          installed with the base, unless another variant is chosen
profiles/<name>/           project scope: dotai apply --profile <name>
mods/<name>/               Claude Code plugins with a hooks module, named by profiles
```

Every file under `base/`, `variants/`, `profiles/` and `mods/` is an example.
While a file is the template's own, unchanged, `dotai validate` and
`dotai pack add` warn `template-unchanged`, and `dotai install` and
`dotai apply` ask before installing it. Edit each one for your pack, or delete
it. This README is not installed: dotai ignores the files at the top of a pack
besides `dotai.yaml`.
