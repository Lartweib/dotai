# Installing dotai

dotai is one binary, `dotai` (`dotai.exe` on Windows), for the platforms listed in the [README](../README.md#supported-harnesses-and-platforms). Install it with a script, from a release archive, or from source.

## One command

On Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/Lartweib/dotai/main/install.sh | sh
```

On Windows, in PowerShell:

```powershell
irm https://raw.githubusercontent.com/Lartweib/dotai/main/install.ps1 | iex
```

The script installs the latest release for your platform ([0048](decisions/0048-one-command-install-scripts.md)). It downloads the archive and `SHA256SUMS` over HTTPS, with curl or, when curl is missing, wget, into a temporary directory. It verifies the archive's checksum and stops before extracting anything if it does not match, then puts `dotai` in `~/.local/bin` (`%LOCALAPPDATA%\Programs\dotai` on Windows) and runs `dotai version`. It needs no sudo or administrator rights. On Windows, a failed update puts the previous `dotai.exe` back; close a running `dotai`, such as `dotai ui`, if the script asks you to.

### PATH

When that directory is not on your `PATH` already, the script puts it there:

- `install.sh` appends two lines, `# added by the dotai installer` and `export PATH='<dir>':"$PATH"`, to the profile of the shell in `$SHELL`: `~/.zshrc` for zsh; `~/.bashrc` for bash, and on macOS also the first of `~/.bash_profile`, `~/.bash_login` and `~/.profile` that exists (`~/.bash_profile` when none does); or `~/.config/fish/conf.d/dotai.fish` with `fish_add_path` for fish. A profile that has the line for that directory already is left as it is. For any other shell, or when the profile cannot be written, it prints the line to add by hand.
- `install.ps1` appends the directory to your user `Path` (`HKCU\Environment`), keeping the existing value and entries such as `%USERPROFILE%` unexpanded, and to the `Path` of the current session.

Either way the script says what it changed; new terminals pick it up.

### Options

Options go after `sh -s --`, or as variables on the side of the pipe that runs the script, not on `curl`: `DOTAI_VERSION=0.1.0 curl ... | sh` sets the variable for `curl` only, and the script never sees it.

```sh
curl -fsSL https://raw.githubusercontent.com/Lartweib/dotai/main/install.sh | sh -s -- --version 0.1.0 --no-modify-path
curl -fsSL https://raw.githubusercontent.com/Lartweib/dotai/main/install.sh | DOTAI_INSTALL_DIR="$HOME/bin" DOTAI_NO_MODIFY_PATH=1 sh
```

```powershell
$env:DOTAI_VERSION = '0.1.0'; $env:DOTAI_NO_MODIFY_PATH = '1'; irm https://raw.githubusercontent.com/Lartweib/dotai/main/install.ps1 | iex
```

- `DOTAI_VERSION` (or `--version`) installs a given release instead of the latest.
- `DOTAI_INSTALL_DIR`, an absolute path, installs elsewhere.
- `DOTAI_NO_MODIFY_PATH=1` (or `--no-modify-path`) changes no profile or `Path` and only prints the line to add.
- `DOTAI_RELEASE_BASE_URL` replaces `https://github.com/Lartweib/dotai/releases`, for tests and mirrors. It must be an `https` URL; plain `http` is accepted only on `127.0.0.1` and `localhost`.

The flags `--version` and `--no-modify-path` are for `install.sh`; `install.ps1` takes the variables only.

### Read the script first

To read the script before running it, download it first, then run the file:

```sh
curl -fsSLo install.sh https://raw.githubusercontent.com/Lartweib/dotai/main/install.sh
less install.sh
sh install.sh
```

On Windows, `irm https://raw.githubusercontent.com/Lartweib/dotai/main/install.ps1 -OutFile install.ps1`, read it, then run `powershell -ExecutionPolicy Bypass -File install.ps1`.

## From a release

Each release on [github.com/lartweib/dotai/releases](https://github.com/lartweib/dotai/releases) has an archive per platform and a `SHA256SUMS` file. The archives are named `dotai_<version>_<os>_<arch>.tar.gz`, or `.zip` for Windows; below, `<archive>` is the one you downloaded.

1. Download the archive for your platform and `SHA256SUMS` from the same release.
2. Verify the checksum in the download directory:
   - Linux: `grep <archive> SHA256SUMS | sha256sum --check`
   - macOS: `grep <archive> SHA256SUMS | shasum -a 256 --check`
   - Windows (PowerShell): `(Get-FileHash <archive>).Hash`, and compare it with the line for that file in `SHA256SUMS`.
3. When the release carries a build provenance attestation, verify it with the GitHub CLI: `gh attestation verify <archive> --repo lartweib/dotai`.
4. Extract the archive and put `dotai` (`dotai.exe` on Windows) in a directory on your `PATH`, such as `~/.local/bin`.
5. Check it with `dotai version`.

## From source

With Go 1.26 or later:

```sh
go install github.com/lartweib/dotai/cmd/dotai@latest
```

The binary goes to `$GOBIN`, or `$(go env GOPATH)/bin` when `GOBIN` is not set; that directory must be on your `PATH`.
