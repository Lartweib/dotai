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
  - Windows: `powershell.exe -NoProfile -ExecutionPolicy Bypass -File <path>\<name>.ps1`;
  - Linux and macOS: `sh <path>/<name>.sh`.
  The rest of the value is kept, so arguments may follow.
- **Quoting per harness.** How the path is written depends on how the harness reads a command in its settings, which its adapter records as data:
  - **Through a shell** (Claude Code, and OpenCode, which has no statusline and is not checked): the path is in double quotes, which cmd.exe, PowerShell and POSIX shells, bash for Claude Code included, all read the same way, so spaces work.
  - **Split by the harness** (agy): agy splits the command itself and passes quotes on to the program as part of an argument, so the path is written bare. A tools directory with whitespace or a quote fails the stage for agy, naming agy and the directory: there is no way to quote it.
  For every harness, a tools directory with a character a shell would not take literally inside double quotes (`"`, `$`, `` ` ``, and `%` on Windows or `\` elsewhere) fails the stage, naming it, and so does one on Windows that starts with `\\` or `//`, such as a network path: bash reads `\\` inside double quotes as one `\`. The same rules apply on Linux and macOS, where agy's splitting was not checked.
- **Both scripts.** A pack that uses the variable ships `dotai/tools/<name>.sh` and `dotai/tools/<name>.ps1` in the setting's scope. A missing one is a diagnostic (`tool-missing`, naming the expected file), and a name that is not a tool name, or a variable without its closing brace, is `tool-invalid`; `validate` reports them for every variant, and `stage` and `install` stop on them before anything is written.
- **Shown.** The install plan lists each resolved command with its harness and settings key, so the dry run shows what the settings file will run.
- **Elsewhere.** The variable is expanded only in settings values, which are encoded as JSON after the expansion; any other file keeps it as written. `${DOTAI_TOOLS}` and `${DOTAI_PROJECT_TOOLS}` work as before.

## Considered options

- **A (chosen):** one variable resolved per OS at stage time, two scripts per tool.
- **B:** name the interpreter in the pack (`bash "${DOTAI_TOOLS}/hello.sh"`). Rejected: on Windows `bash` is often the WSL launcher.
- **C:** a per-OS settings block. Rejected (inferred): every pack would repeat each command per OS.
- **D:** expand the variable in every text file, as the path variables are. Rejected: the command holds double quotes, which raw text replacement would put unescaped into JSON or YAML.
- **E:** one quoting for every harness. Rejected: double quotes break agy, and a bare path breaks a shell on a directory with spaces.

## Consequences

- Good: a statusline or any other command a pack's settings run works on Windows without a new window, and on Linux and macOS with `sh`.
- Accepted cost: the staged settings depend on the OS, as the absolute paths of [0030](0030-tools-paths-and-project-config.md) already depend on the machine; a pack keeps two scripts per tool.
- Accepted cost: a pack whose agy settings run a tool cannot install into a home directory whose path has a space. `validate` does not know the home directory, so the dry run and the install report it.
- Amends [0030](0030-tools-paths-and-project-config.md) (another variable, in settings values only).

## Evidence

- A manual test of agy on Windows: a bare `.sh` statusline opens a console window and fails, while `powershell.exe -NoProfile -File <script>` works.
- A manual test of agy 1.4.x on Windows with `-File "<path>\hello.ps1"`: every refresh fails, and `cli.log` shows PowerShell got the quotes as part of the path: it reported invalid characters in the `-File` value `"C:\...\hello.ps1"`, quotes included, with exit status `0xfffd0000`. So agy runs the command without a shell and does not strip quotes. The statusline runs only in the interactive screen, so whether agy splits at whitespace alone was not checked; dotai assumes it does and refuses whitespace ([HARNESS-NOTES](../HARNESS-NOTES.md)).
- [PACK-FORMAT](../PACK-FORMAT.md) and [CLI-JSON](../CLI-JSON.md): the variable, its diagnostics and the plan's `tools`.

Update 2026-11-02: the variable is also expanded in the `command` of each hook of the merged `.rulesync/hooks.jsonc` ([0040](0040-list-files-merged-as-a-union.md)), with the same checks (`tool-missing`, `tool-invalid`). agy runs hooks through `cmd /c` with every `"` passed as `\"`, Claude Code through bash (Git Bash on Windows) and OpenCode through Bun Shell ([HARNESS-NOTES](../HARNESS-NOTES.md#hook-commands)). So a hook of a tool-scoped block is written for that block's harness, in double quotes for `claudecode` and `opencode` and bare for `antigravity-cli`. A shared hook reaches, through one string, every rendered harness whose own block does not declare its event, since rulesync 41 replaces it for those that do; it is bare when agy is among them and quoted otherwise, and a hook that no rendered harness gets keeps the variable as written. A bare path in a hook uses forward slashes, Windows included, because Git Bash drops the backslashes of a bare path and all three harnesses run the bare form with `/`. A bare path, in a hook or in agy's settings, cannot hold whitespace, a quote, or one of `& | < > ^ ( ) ; * ? [ ] { } !`, which `cmd.exe` or a shell would read as an operator, an expansion or a pattern; for a hook such a tools directory is the diagnostic `hook-path-unquotable`, naming the hook and the harness, from `stage` and `install`, and nothing is written. This goes beyond the Decision above, which expanded the variable in settings values only.
