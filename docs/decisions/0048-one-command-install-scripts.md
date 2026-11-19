---
status: proposed
date: 2026-11-19
tags: [release, install, supply-chain, ci]
---

# 0048 · One-command install scripts that verify the release checksum

## Context

[0042](0042-tag-driven-release-binaries.md) publishes an archive per platform and `SHA256SUMS` with each release. Installing from them takes five manual steps: pick the archive, download it with `SHA256SUMS`, verify the checksum, extract it, and put the binary on `PATH`. The repository is public, so the scripts at its root can be fetched from `raw.githubusercontent.com`. No release exists yet.

## Decision

- **Two scripts at the repository root.** `install.sh`, in POSIX sh, for Linux and macOS, and `install.ps1`, compatible with Windows PowerShell 5.1, for Windows. They are run as `curl -fsSL .../install.sh | sh` and `irm .../install.ps1 | iex`.
- **Release.** The latest release is the tag that `releases/latest` redirects to, so no token and no API rate limit are involved; pre-releases are installed only when named. `DOTAI_VERSION`, or `--version` for `install.sh`, names a release.
- **Platforms.** The five of 0042. Anything else is refused with a message naming the platforms that exist.
- **Verification first.** The archive and `SHA256SUMS` are downloaded over HTTPS into a fresh temporary directory, removed on exit: curl runs with `--proto =https --proto-redir =https`, GNU wget with `--https-only` (BusyBox wget has no such option), and both scripts refuse a `DOTAI_RELEASE_BASE_URL` that is not `https`, except `http` on `127.0.0.1` or `localhost` for the tests. The archive is checked against its line in `SHA256SUMS` before anything is extracted; a missing line or a mismatch aborts and installs nothing. When `gh` is installed, the script prints how to verify the attestation; it never requires `gh`.
- **Install.** `~/.local/bin` on Linux and macOS, `%LOCALAPPDATA%\Programs\dotai` on Windows, or `DOTAI_INSTALL_DIR`, which must be absolute. The new binary is copied next to the old one and renamed over it, and the copy is removed when that fails. On Windows the old `dotai.exe` is renamed aside under a unique name first, since a running one can be renamed but not overwritten; if the new one cannot take its place, the old one is renamed back, the copy is removed, and the script asks to close any running `dotai`. No sudo or administrator rights. It ends with `dotai version`.
- **PATH.** When the directory is on `PATH` already, trailing separators aside, nothing changes. Otherwise `install.sh` appends a marker comment and one line with the directory single-quoted, so that no character of it runs as code, to the profile of `$SHELL`: `~/.zshrc`; `~/.bashrc`, and on macOS also the login profile bash reads, the first of `~/.bash_profile`, `~/.bash_login` and `~/.profile` that exists, creating `~/.bash_profile` only when none does; or `~/.config/fish/conf.d/dotai.fish` with `fish_add_path`. A profile that has the line for that directory is not written again; another directory gets its own line. Any other shell, a missing `HOME`, or a profile that cannot be written gets the line printed, and the install still succeeds. `install.ps1` appends the directory to the raw user `Path` in `HKCU\Environment`, read without expanding it and written back with its value kind, so `%USERPROFILE%` entries survive; an entry that expands to the directory counts as present. It also adds the directory to the session's `Path`. Both say what they changed. `--no-modify-path` (sh) or `DOTAI_NO_MODIFY_PATH=1` (both) only prints the line.
- **Tests.** `DOTAI_RELEASE_BASE_URL` replaces the releases URL, for tests and mirrors. Two variables exist for the tests only: `DOTAI_TEST_USER_ENV_KEY`, accepted only under `HKCU\Software\dotai-install-test\`, replaces `HKCU\Environment` so that no test writes the real user `Path`, and `DOTAI_TEST_FAIL_SWAP=1` fails the Windows update between its renames. A Go test serves a fake release on `127.0.0.1` and runs `install.sh` under `sh`, and `bash` when present, on Linux and macOS, and `install.ps1` under Windows PowerShell, and pwsh when present, on Windows; in CI a missing `sh` or Windows PowerShell fails it. CI runs shellcheck on the shell scripts.

## Considered options

- **A (chosen):** two short scripts, read before running, with checksum verification.
- **B:** only the manual steps of the README. Rejected: five steps, and the checksum step is the one most often skipped.
- **C:** package managers (Homebrew, Scoop, winget). Not rejected, deferred: each needs its own repository or manifest and release automation; 0042 names GoReleaser as the moment to revisit them.
- **D:** print the PATH line and never write it. Rejected as the default, since `dotai` would not run in the next terminal; kept as the opt-out.

## Consequences

- Good: one command installs a verified release, without sudo, and `dotai` works in the next terminal.
- Bad or accepted cost: the script writes a shell profile or the user `Path`. The change is marked, made once, reported, and can be refused with the opt-out; the script does not remove it on uninstall.
- Bad or accepted cost: `curl | sh` runs what the server sends. The README shows how to download and read the script first. As in 0042, `SHA256SUMS` comes from the same release, so the script detects a corrupt download, not a compromised release; the attestation covers that.
- Bad or accepted cost: Windows on Arm and other architectures are refused, as no release is built for them.
- Pending: the scripts are verified against the fake release only until the first release is cut.

## Evidence

- `install.sh`, `install.ps1` and `cmd/dotai/install_script_test.go`.
- Local checks, 2026-11-19: the test passed on Windows 11 with Windows PowerShell 5.1, its registry cases on a test-only key, in Docker `golang:1.26` (dash and bash, curl; as root and as an unprivileged user) and `golang:1.26-alpine` (busybox sh and wget), the macOS bash cases with a `uname` that reports Darwin; shellcheck 0.11.0 reported nothing. Against github.com, with no release yet, both scripts stop with "no release found".
