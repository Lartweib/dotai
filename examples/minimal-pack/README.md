# minimal-pack

A small, complete dotai pack: the one the engine's tests and the docs' demos use. Read it next to [PACK-FORMAT.md](../../docs/PACK-FORMAT.md), which defines every file below. To start a pack of your own, run `dotai pack new` instead, which writes a commented template ([Creating a pack](../../docs/PACK-FORMAT.md#creating-a-pack)).

## Layout

```
dotai.yaml                   the manifest: name, version, engine range, pinned rulesync, harnesses,
                             profiles and variants
base/                        the user scope every install gets
  .rulesync/                 rulesync sources: a root rule, a skill, a subagent, permissions
  dotai/settings.yaml        per-harness settings, such as the model, that rulesync does not render
  dotai/tools/               hello.sh and hello.ps1, installed to ~/.dotai/tools
variants/                    one layer on top of the base, chosen at install time
  standalone/                the default variant
  tmux/                      overrides the Claude Code model, sets an env variable, adds a rule, a subagent and permissions
  shared/                    shares the user's ~/.agents/skills folder (shared-skills: true)
profiles/
  web/                       a project profile, applied with dotai apply --profile web
mods/
  hello/                     a mod, a Claude Code plugin with a hooks module, that the web profile names
```

## Try it

In a temporary home directory, so that nothing touches your own harness configuration:

```sh
dotai validate examples/minimal-pack
dotai stage examples/minimal-pack --out /tmp/minimal-stage
dotai install examples/minimal-pack --dry-run
```

The pack is compatible with all three harnesses (`harnesses` in `dotai.yaml`) and requires none of them: an install manages those that are installed. Add `requires: [claudecode]` to make an install stop when Claude Code is not installed.
