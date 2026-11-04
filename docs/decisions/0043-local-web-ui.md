---
status: proposed
date: 2026-11-04
tags: [ui, web, security, cli, v1]
---

# 0043 · A local web UI in v1, served by `dotai ui` over the same use cases

## Context

[0026](0026-json-cli-and-ui-after-v1.md) made `--json` the contract a UI builds on and placed the UI after v1, as a web page or a terminal UI. The decision now is that v1 ships a local web UI, so that the installed state, the backups, a plan and the `doctor` result can be seen without reading JSON. A local server that can write configuration is an attack surface for every page open in the browser: another site can send requests to `127.0.0.1` (CSRF), and a site can point its own name at `127.0.0.1` to read the answers (DNS rebinding).

## Decision

- **Thin presentation layer.** `dotai ui` runs in the same binary and its handlers call `internal/app` directly, never the command line. Every response is the CLI-JSON envelope, with the same `data` and error codes as the command it mirrors; the error mapping is shared code (`output.Classify`). The UI holds no logic of its own.
- **Local only.** The server binds `127.0.0.1` only, on a free port unless `--port` names one, prints its address, opens the browser unless `--no-open`, and stops on Ctrl+C, cancelling the requests in flight.
- **Token, Host and Origin.** Each run makes a random token with `crypto/rand`. The printed address carries it once, as a query parameter that sets an `HttpOnly`, `SameSite=Strict` cookie and is redirected away; API requests carry it in that cookie or an `Authorization: Bearer` header. A request whose Host is not `127.0.0.1:<port>` or `localhost:<port>`, whose Origin is present and not the server's own, or whose `Sec-Fetch-Site` is another site's is refused. Read endpoints take `GET` only; endpoints that write will take `POST` only.
- **No-build frontend.** The frontend is plain HTML, CSS and native ES modules, embedded with `go:embed`, with no Node toolchain and no generated files, so that `go install` keeps building a complete binary.
- **Plan, then confirm.** The first step is read-only: the state, backups, the plans of install and apply, a preview of restore and `doctor`. Actions follow in the next step, each one shown as its plan first and carried out only after the user confirms it, through the consent callbacks the use cases already take.
- **Pending:** where the UI finds packs to install (a pack registry) is not decided yet.

## Considered options

- **A (chosen):** a local web UI in v1, as above.
- **B:** a terminal UI. Not chosen for v1 (inferred): a browser shows the plans, diffs and backups more readily; it stays possible later over the same use cases.
- **C:** a UI after v1, as 0026 planned. Not chosen: the user decided that v1 ships with the UI.
- **D:** a frontend with a build step. Rejected: it would need Node to build and generated files in the repository, and `go install` would no longer build everything.

## Consequences

- Good: one source of truth for behaviour and error codes; the UI cannot drift from the command line.
- Good: the read-only API is useful to scripts too, with the same envelope.
- Accepted cost: a cookie is not bound to a port, so a program that already runs as the user on another port of `127.0.0.1` could receive it; such a program can read `~/.dotai` anyway. The token is also briefly on the command line of the process that opens the browser.
- Pending: the pack registry; the endpoints that write; once accepted, 0026 is partially superseded on when the UI comes.

## Evidence

- [CLI-JSON, Web UI API](../CLI-JSON.md#web-ui-api): the endpoints, the checks and the error codes.
- [0026](0026-json-cli-and-ui-after-v1.md): the token and `127.0.0.1` rules this record keeps.
