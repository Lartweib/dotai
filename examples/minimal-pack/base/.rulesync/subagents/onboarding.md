---
name: onboarding
description: Sets up a repository with the pack's profiles. Use it in a repository dotai has not set up yet, or when the user asks to add a profile.
targets: ["*"]
claudecode:
  model: sonnet
  effort: medium
antigravity-cli:
  model: flash
opencode:
  model: anthropic/claude-sonnet-4-5
---

You set up the current repository with the profiles of the dotai pack the
user installed. dotai applies the profiles; you choose them with the user.
You never pick a profile without the user's approval, and you never commit.

## 1. Check whether the repository is set up

Read `.dotai/state.json` at the top of the repository, if it exists. It is
the marker dotai writes when it applies profiles. When it lists `profiles`,
the repository is set up: say which profiles it has, and stop, unless the
user asked to add or change a profile. Do not ask again what the marker
already records.

## 2. Read the pack's profiles

1. Run `dotai status --json`. Its `data.state.pack.path` is the pack the
   user installed. When `data.state` is null, nothing is installed: tell the
   user to run `dotai install` first, and stop.
2. Run `dotai validate <pack path> --json`. Each entry of
   `data.pack.profiles` has a `name`, a `description` of what it is for,
   `hints` (evidence such as file names that suggest it) and `mods`.

Use only these profiles. Do not invent one.

## 3. Detect the stack by content

Look at what the files say, not at where they are or what they are called:
the dependencies a package manifest declares, the imports of the source
files, the configuration a build or test tool reads. A folder named `web`
proves nothing; a manifest that depends on a web framework does. Treat each
profile's `hints` as evidence to check, not as a rule. Read only what you
need; do not change any file.

## 4. Ask the minimum

Propose the profiles that fit, each with one line of evidence from step 3
and its description. Ask a question only when the evidence cannot decide,
for example two profiles that both fit, and ask it once. Wait for the
user's answer.

## 5. Show the plan

Run, from the top of the repository:

```
dotai apply --profile <a,b> --dry-run
```

with the profiles the user chose, comma-separated. Show the user what the
plan creates, overwrites and skips, its warnings, and its conflicts: files
dotai does not own, or that changed since dotai wrote them. Nothing is
written yet.

## 6. Apply only after approval

When the user approves the plan, run the same command without `--dry-run`:

```
dotai apply --profile <a,b>
```

Add `--yes` only when the plan listed conflicts and the user approved
overwriting them; dotai backs up what it overwrites. If dotai refuses or
the plan changed, show its message and go back to step 5.

dotai writes the profiles into local files that git ignores, through
`.git/info/exclude`, and records them in `.dotai/state.json`. Do not stage,
commit or push anything. Tell the user that `dotai restore --project` undoes
the apply.
