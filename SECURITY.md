# Security policy

dotai writes into AI coding harness configurations, runs a pinned rulesync binary, fetches packs with git and serves a local web UI, so security reports are taken seriously.

## Supported versions

dotai is in its `0.x` series. Security fixes are made on `main` and shipped in the next release; only the latest release receives them.

| Version | Supported |
|---|---|
| Latest `0.x` release | Yes |
| Older releases | No |
| `main` | Yes, fixed first |

## Reporting a vulnerability

Report vulnerabilities privately through GitHub's private vulnerability reporting: open the repository's **Security** tab and choose **Report a vulnerability** ([direct link](https://github.com/lartweib/dotai/security/advisories/new)). Do not open a public issue, pull request or discussion for a vulnerability.

Include what you can of:

- the affected version (`dotai version`) and OS;
- the steps or a minimal pack that reproduces it;
- what an attacker gains, such as a write outside the directories dotai owns, a command run from a pack without consent, or access to the web UI from another origin.

## What to expect

- An acknowledgement within 7 days.
- An assessment, and a fix or a mitigation plan, discussed with you in the private advisory.
- Credit in the advisory and the release notes, unless you ask not to be named.

Please give a reasonable time to ship a fix before any public disclosure.

## Scope

In scope: the `dotai` binary and everything in this repository, including the release workflow and the embedded pack template.

Out of scope: vulnerabilities in the harnesses themselves (Claude Code, agy, OpenCode), in rulesync, or in git; report those upstream. A pack runs what its author wrote, such as hooks, MCP servers and tools: dotai shows every install as its plan first, and the executable content an update changes before it applies it, but a malicious pack you chose to install is not a dotai vulnerability unless dotai ran it without the consent it asks for.
