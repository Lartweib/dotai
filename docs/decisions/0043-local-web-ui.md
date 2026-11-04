---
status: proposed
date: 2026-11-04
tags: [ui, web, security, cli, v1]
---

# 0043 · A local web UI in v1, served by `dotai ui` over the same use cases

## Context

[0026](0026-json-cli-and-ui-after-v1.md) made `--json` the contract a UI builds on and placed the UI after v1, as a web page or a terminal UI. The decision now is that v1 ships a local web UI, so that the installed state, the backups, a plan and the `doctor` result can be seen without reading JSON. A local server that can write configuration is an attack surface for every page open in the browser: another site can send requests to `127.0.0.1` (CSRF), and a site can point its own name at `127.0.0.1` to read the answers (DNS rebinding).

## Decision

- **Thin presentation layer.** `dotai ui` runs in the same binary and its handlers call `internal/app` directly, never the command line. Every response is the CLI-JSON envelope, with the same `data` and error codes as the command it mirrors; the error mapping is shared code (`envelope.Classify` in `internal/envelope`, which `internal/cli` and `internal/server` both use and neither imports the other). The UI holds no logic of its own.
- **Local only.** The server binds `127.0.0.1` only, on a free port unless `--port` names one, prints its address, opens the browser unless `--no-open`, and stops on Ctrl+C, cancelling the requests in flight.
- **One-time code, session token, Host and Origin.** Each run makes two secrets with `crypto/rand`. The printed address carries a one-time code in its fragment (`#code=`), which the browser never sends to the server or in a `Referer`. The page posts it to `/api/session`, which accepts it once, within two minutes, and returns the session token; the page keeps the token in `sessionStorage` and sends it as `Authorization: Bearer` on every API request. No cookie is a credential, since cookies are not bound to a port. A used or expired code is refused with a message to restart `dotai ui`. A request whose Host is not `127.0.0.1:<port>` or `localhost:<port>`, whose Origin is present and not the server's own, or whose `Sec-Fetch-Site` is another site's is refused. The static frontend is public. Endpoints that only read take `GET`; the plans and every endpoint that writes take `POST`.
- **No-build frontend.** The frontend is plain HTML, CSS and native ES modules, embedded with `go:embed`, with no Node toolchain and no generated files, so that `go install` keeps building a complete binary.
- **Plans run tooling.** A plan, as with `--dry-run`, fetches rulesync into the cache, stages the pack into a temporary directory and runs git in the project; nothing else is written. One plan runs at a time, a second one is refused as busy, and a plan stops after five minutes.
- **Plan, then confirm, by digest.** Reading writes nothing in the home directory or a project: the state, the backups, the plans of install and apply, the previews of restore and `doctor`. Every plan and preview carries a digest, the SHA-256 of its JSON. Each action (install, apply, restore, restore of a project, `doctor --fix`) takes that digest back. The use case plans again and calls `Confirm`, an option it takes, with the plan it is about to carry out, before the running-harness check, the consent, the lock and any write. The server compares the digests and refuses another plan with `plan-changed` (409) and the new plan, so that the user carries out only a plan they saw and confirms a changed one again. The consent to overwrite what dotai does not own stays explicit (`yes`, as `--yes`). Actions take `POST` only, run one at a time with the plans (`busy`, 429) and never skip the running-harness check.
- **Boards.** The page shows the packs, the merged tree of an install per harness, the install plan, the projects, the backups and the `doctor` findings. The merged tree is drawn from the install plan, which names each target and its staged source but not the layer that gave it. `doctor` defines no severity, so the page orders and marks findings by their code and whether `--fix` can fix them.
- **Fonts.** The page embeds Schibsted Grotesk and Spline Sans Mono as WOFF2 files under the SIL Open Font License, so that the Content Security Policy keeps allowing only the server's own origin.
- **Pending:** where the UI finds packs to install (a pack registry) is not decided yet. Until then the user types the absolute path of a pack, and the page remembers the recent paths in the browser's `localStorage`; the server keeps no list.

## Considered options

- **A (chosen):** a local web UI in v1, as above.
- **B:** a terminal UI. Not chosen for v1 (inferred): a browser shows the plans, diffs and backups more readily; it stays possible later over the same use cases.
- **C:** a UI after v1, as 0026 planned. Not chosen: the user decided that v1 ships with the UI.
- **D:** a frontend with a build step. Rejected: it would need Node to build and generated files in the repository, and `go install` would no longer build everything.

## Consequences

- Good: one source of truth for behaviour and error codes; the UI cannot drift from the command line.
- Good: the API is useful to scripts too, with the same envelope.
- Accepted cost: the one-time code is briefly exposed, on the command line of the process that opens the browser and in the terminal, until the page uses it or it expires two minutes later. The session token never leaves the server and the page. A program that already runs as the user can read `~/.dotai` anyway.
- Accepted cost: an action plans twice, once for the user and once to carry it out, so an action takes about as long as its plan again.
- Good: a plan that changed between the page and the action, such as a file another program edited, is never carried out unseen.
- Pending: the pack registry; once accepted, 0026 is partially superseded on when the UI comes.

## Evidence

- [CLI-JSON, Web UI API](../CLI-JSON.md#web-ui-api): the endpoints, the checks and the error codes.
- [0026](0026-json-cli-and-ui-after-v1.md): the token and `127.0.0.1` rules this record refines.
