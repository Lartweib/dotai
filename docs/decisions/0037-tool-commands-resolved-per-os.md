---
status: accepted
date: 2026-10-31
tags: [engine, pack-format, tools, settings, windows]
---

# 0037 · Tool commands in settings are resolved per OS

## Context

A setting that runs a pack tool, such as a statusline, named the script by its path: `"${DOTAI_TOOLS}/hello.sh"` ([0030](0030-tools-paths-and-project-config.md)). On Windows, agy runs a bare `.sh` path through the file association: every statusline refresh opens a new console window, and the command fails (`statusline: command failed: exit status 1` in agy's `cli.log`). Naming an interpreter in the pack does not travel either. `bash "<path>"` works only when Git's `usr/bin` comes first on `PATH`, as in a Git Bash shell; with the usual Windows `PATH`, `bash` is the WSL launcher in `System32`, which fails, and could not read a `C:/...` path anyway. A statusline that runs on Windows names `powershell.exe -NoProfile -File <script>`, which opens no window.

## Decision

- **Variable.** `${DOTAI_TOOL:<name>}` in a string value of `dotai/settings.yaml`, for any harness, stands for the command that runs the tool `<name>`. The name is the tool's file name without an extension: letters, digits, `.`, `_` and `-`, starting with a letter or a digit.
- **Resolution.** While staging, where the path variables of settings values are expanded, the engine replaces it with a full command for the OS it runs on, with the script's absolute path in the tools directory of the settings' scope:
  - Windows: `powershell.exe -NoProfile -ExecutionPolicy Bypass -File "<dir>\<name>.ps1"`;
  - Linux and macOS: `sh "<dir>/<name>.sh"`.
  The rest of the value is kept, so arguments may follow. The path is in double quotes, which cmd.exe, PowerShell and POSIX shells, bash for Claude Code included, all read the same way, so spaces work. A tools directory with a character the quotes do not protect (`"`, `$`, `` ` ``, and `%` on Windows or `\` elsewhere) fails the stage, naming it.
- **Both scripts.** A pack that uses the variable ships `dotai/tools/<name>.sh` and `dotai/tools/<name>.ps1` in the setting's scope. A missing one is a diagnostic (`tool-missing`, naming the expected file), and a name that is not a tool name is `tool-invalid`; `validate` reports them for every variant, and `stage` and `install` stop on them before anything is written.
- **Shown.** The install plan lists each resolved command with its harness and settings key, so the dry run shows what the settings file will run.
- **Elsewhere.** The variable is expanded only in settings values, which are encoded as JSON after the expansion; any other file keeps it as written. `${DOTAI_TOOLS}` and `${DOTAI_PROJECT_TOOLS}` work as before.

## Considered options

- **A (chosen):** one variable resolved per OS at stage time, two scripts per tool.
- **B:** name the interpreter in the pack (`bash "${DOTAI_TOOLS}/hello.sh"`). Rejected: on Windows `bash` is often the WSL launcher.
- **C:** a per-OS settings block. Rejected (inferred): every pack would repeat each command per OS.
- **D:** expand the variable in every text file, as the path variables are. Rejected: the command holds double quotes, which raw text replacement would put unescaped into JSON or YAML.

## Consequences

- Good: a statusline or any other command a pack's settings run works on Windows without a new window, and on Linux and macOS with `sh`.
- Accepted cost: the staged settings depend on the OS, as the absolute paths of [0030](0030-tools-paths-and-project-config.md) already depend on the machine; a pack keeps two scripts per tool.
- Amends [0030](0030-tools-paths-and-project-config.md) (another variable, in settings values only).

## Evidence

- A manual test of agy on Windows: a bare `.sh` statusline opens a console window and fails, while `powershell.exe -NoProfile -File <script>` works.
- [PACK-FORMAT](../PACK-FORMAT.md) and [CLI-JSON](../CLI-JSON.md): the variable, its diagnostics and the plan's `tools`.
