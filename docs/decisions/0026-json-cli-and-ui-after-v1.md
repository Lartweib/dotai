---
status: accepted
date: 2026-10-26
tags: [cli, ui, web, tui, v1]
---

# 0026 · Every command has `--json`; a UI comes after v1

## Context

A visual front end would make it easier to see the installed configuration and the result of `dotai doctor`, add or remove profiles, and install a pack. Two shapes fit: a local web page opened by a command (for example `dotai ui`), or a terminal UI. A UI with its own logic would create a second way to do everything, and a local server that can write configuration is an attack surface for any page open in the browser.

## Decision

- **v1:** every engine command offers `--json` output, stable enough for another program to consume. This is the contract any UI builds on.
- **After v1:** a UI is added as a thin layer over the same engine functions the CLI uses. It holds no logic of its own. The choice between a local web page and a terminal UI is made once v1 works.
- **If it is a web page:** the server listens only on `127.0.0.1`, requires a random token included in the URL that `dotai ui` opens, and stops when the session ends. Its assets are embedded in the dotai binary.

## Considered options

- **A (chosen):** `--json` in v1, UI after v1, web or terminal decided later.
- **B:** web UI in v1. Rejected: it would be built on top of commands that are not stable yet, and it delays v1.
- **C:** no UI. Rejected: a visual view of the configuration and of `doctor` adds real value.

## Consequences

- The `--json` schema of each command becomes a public contract; changing it needs the same care as changing a CLI flag.
- A terminal UI (for example with Bubble Tea) avoids the local server and its security rules entirely.
