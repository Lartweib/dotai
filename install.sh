#!/bin/sh
# Installs dotai from a GitHub release on Linux or macOS (decision 0048).
#
#   curl -fsSL https://raw.githubusercontent.com/Lartweib/dotai/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/Lartweib/dotai/main/install.sh | sh -s -- --version 0.1.0
#
# It downloads the archive for this platform and SHA256SUMS from the
# release over HTTPS, checks the archive against SHA256SUMS before
# extracting it, and moves dotai into the install directory. It never uses
# sudo. When that directory is not on PATH, it appends one marked line to
# the profile of the shell in $SHELL (zsh, bash or fish), once per
# directory; --no-modify-path or DOTAI_NO_MODIFY_PATH=1 only prints the
# line instead.
#
# Environment:
#   DOTAI_VERSION           the release to install, such as 0.1.0 or v0.1.0
#                           (default: the latest release); --version wins
#   DOTAI_INSTALL_DIR       where dotai goes, an absolute path
#                           (default: ~/.local/bin)
#   DOTAI_NO_MODIFY_PATH=1  never write a shell profile; --no-modify-path too
#   DOTAI_RELEASE_BASE_URL  the releases URL, for tests and mirrors: https,
#                           or http on 127.0.0.1 or localhost only
#                           (default: https://github.com/Lartweib/dotai/releases)
set -eu

die() {
  echo "dotai install: $*" >&2
  exit 1
}

# fetch <url> <file> downloads url into file, refusing any hop that is not
# HTTPS (except to a loopback base URL, see main).
fetch() {
  if [ "$have_curl" = 1 ]; then
    curl -fsSL --proto "$proto" --proto-redir "$proto" -o "$2" "$1"
  elif [ "$wget_https_only" = 1 ]; then
    wget -q --https-only -O "$2" "$1"
  else
    wget -q -O "$2" "$1"
  fi
}

# latest_tag prints the tag that <base>/latest redirects to.
latest_tag() {
  if [ "$have_curl" = 1 ]; then
    url=$(curl -fsSIL --proto "$proto" --proto-redir "$proto" -o /dev/null -w '%{url_effective}' "$base/latest") ||
      die "cannot reach $base/latest"
  else
    # Only when curl is missing: wget has no option that prints the final
    # URL, so the last Location header of the redirects is the release.
    # --spider requests the headers only; -S prints them on stderr.
    if [ "$wget_https_only" = 1 ]; then
      headers=$(wget --https-only -S --spider "$base/latest" 2>&1 || true)
    else
      headers=$(wget -S --spider "$base/latest" 2>&1 || true)
    fi
    url=$(printf '%s\n' "$headers" | tr -d '\r' |
      sed -n 's/^ *[Ll]ocation: *\([^ ]*\).*/\1/p' | tail -n 1)
  fi
  case $url in
    */tag/*) echo "${url##*/tag/}" ;;
    *) die "no release found at $base/latest" ;;
  esac
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  else
    die "neither sha256sum nor shasum is installed; cannot verify the download"
  fi
}

# strip_slashes prints $1 without trailing slashes, keeping a lone /.
strip_slashes() {
  s=$1
  while [ "${s%/}" != "$s" ]; do s=${s%/}; done
  printf '%s' "${s:-/}"
}

# on_path succeeds when $dir is an entry of PATH, trailing slashes aside.
on_path() {
  set -f
  old_ifs=$IFS
  IFS=:
  found=1
  for p in $PATH; do
    [ "$(strip_slashes "$p")" = "$dir" ] && found=0 && break
  done
  IFS=$old_ifs
  set +f
  return "$found"
}

# loopback succeeds when $1 is an http URL whose authority is exactly
# 127.0.0.1 or localhost, with an optional numeric port: no user info, so
# that http://127.0.0.1:80@example.com is refused.
loopback() {
  case $1 in
    http://*) ;;
    *) return 1 ;;
  esac
  rest=${1#http://}
  authority=${rest%%/*}
  host=${authority%%:*}
  port=${authority#"$host"}
  case $host in
    127.0.0.1 | localhost) ;;
    *) return 1 ;;
  esac
  case $port in
    '') return 0 ;;
    : | :*[!0-9]*) return 1 ;;
  esac
  return 0
}

marker='# added by the dotai installer'

# profile_line appends the marker and $line to the profile $1, unless the
# profile has that line already, and records it in $written. When the
# profile cannot be written, it prints the line to add by hand.
profile_line() {
  if [ -f "$1" ] && grep -qxF -- "$line" "$1"; then
    echo "$1 already puts $dir on PATH"
    return 0
  fi
  if { mkdir -p "$(dirname "$1")" && printf '\n%s\n%s\n' "$marker" "$line" >>"$1"; } 2>/dev/null; then
    echo "added $dir to PATH in $1"
    written=$1
  else
    echo "dotai is installed, but $1 could not be written; add this line to it by hand:"
    echo "  $line"
  fi
}

# add_to_path puts $dir on PATH in the profile of the user's shell, unless
# it is on PATH already or the user opted out.
add_to_path() {
  on_path && return 0
  # The directory is single-quoted, so no character of it runs as code.
  q=$(printf '%s' "$dir" | sed "s/'/'\\\\''/g")
  line="export PATH='$q':\"\$PATH\""
  shell=${SHELL:-}
  # Without HOME there is no profile to write.
  if [ "$modify_path" = 0 ] || [ -z "${HOME:-}" ]; then
    shell=
  fi
  written=
  case $shell in
    */zsh)
      profile_line "$HOME/.zshrc"
      ;;
    */bash)
      profile_line "$HOME/.bashrc"
      if [ "$os" = darwin ]; then
        # Terminal opens login shells, which read only the first of these
        # that exists; creating .bash_profile would hide the others.
        login=$HOME/.bash_profile
        for f in "$HOME/.bash_profile" "$HOME/.bash_login" "$HOME/.profile"; do
          if [ -f "$f" ]; then
            login=$f
            break
          fi
        done
        profile_line "$login"
      fi
      ;;
    */fish)
      # In fish single quotes, only \ and ' are escaped.
      q=$(printf '%s' "$dir" | sed -e 's/\\/\\\\/g' -e "s/'/\\\\'/g")
      line="fish_add_path '$q'"
      profile_line "$HOME/.config/fish/conf.d/dotai.fish"
      ;;
    *)
      echo "$dir is not on your PATH; add this line to your shell profile:"
      echo "  $line"
      return 0
      ;;
  esac
  # The hint names only a profile that was written: the last one.
  [ -n "$written" ] || return 0
  shown=\~/${written#"$HOME"/}
  case $written in
    *.fish) echo "open a new terminal, or run: source $shown" ;;
    *) echo "open a new terminal, or run: . $shown" ;;
  esac
}

main() {
  version=${DOTAI_VERSION:-}
  modify_path=1
  [ "${DOTAI_NO_MODIFY_PATH:-}" = 1 ] && modify_path=0
  while [ $# -gt 0 ]; do
    case $1 in
      --version) [ $# -ge 2 ] || die "--version needs a value"; version=$2; shift 2 ;;
      --version=*) version=${1#--version=}; shift ;;
      --no-modify-path) modify_path=0; shift ;;
      *) die "unknown argument: $1 (usage: install.sh [--version X.Y.Z] [--no-modify-path])" ;;
    esac
  done

  base=$(strip_slashes "${DOTAI_RELEASE_BASE_URL:-https://github.com/Lartweib/dotai/releases}")
  proto='=https'
  wget_https_only=1
  case $base in
    https://*) ;;
    *)
      loopback "$base" ||
        die "DOTAI_RELEASE_BASE_URL must be an https URL, or http on 127.0.0.1 or localhost: $base"
      # A loopback release, as in the tests.
      proto='=http,https'
      wget_https_only=0
      ;;
  esac

  if [ -n "${DOTAI_INSTALL_DIR:-}" ]; then
    dir=$DOTAI_INSTALL_DIR
  elif [ -n "${HOME:-}" ]; then
    dir=$HOME/.local/bin
  else
    die "HOME is not set; set HOME or DOTAI_INSTALL_DIR"
  fi
  case $dir in
    /*) dir=$(strip_slashes "$dir") ;;
    *) die "DOTAI_INSTALL_DIR must be an absolute path: $dir" ;;
  esac

  case $(uname -s) in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "unsupported OS $(uname -s); releases exist for Linux, macOS and Windows (use install.ps1 on Windows)" ;;
  esac
  case $(uname -m) in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "unsupported architecture $(uname -m); releases exist for amd64 and arm64" ;;
  esac
  have_curl=0
  if command -v curl >/dev/null 2>&1; then
    have_curl=1
  elif command -v wget >/dev/null 2>&1; then
    # BusyBox wget has no --https-only; it is used without it.
    wget --help 2>&1 | grep -q -- --https-only || wget_https_only=0
  else
    die "neither curl nor wget is installed"
  fi

  if [ -z "$version" ]; then
    version=$(latest_tag)
  fi
  version=${version#v}
  case $version in
    '' | *[!0-9A-Za-z.-]*) die "invalid version: $version" ;;
  esac
  tag=v$version
  archive=dotai_${version}_${os}_${arch}.tar.gz

  tmp=$(mktemp -d)
  new=
  trap 'rm -rf "$tmp"; [ -z "$new" ] || rm -f "$new"' EXIT
  trap 'exit 1' HUP INT TERM

  echo "downloading dotai $tag for $os/$arch"
  fetch "$base/download/$tag/$archive" "$tmp/$archive" ||
    die "cannot download $base/download/$tag/$archive; release $tag may not exist or have no ${os}_$arch archive"
  fetch "$base/download/$tag/SHA256SUMS" "$tmp/SHA256SUMS" ||
    die "cannot download $base/download/$tag/SHA256SUMS"

  want=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/SHA256SUMS")
  [ -n "$want" ] || die "SHA256SUMS of $tag has no line for $archive"
  got=$(sha256 "$tmp/$archive")
  [ "$got" = "$want" ] || die "checksum mismatch for $archive: got $got, want $want; nothing was installed"
  echo "checksum verified"

  mkdir -p "$tmp/x"
  tar -xzf "$tmp/$archive" -C "$tmp/x" dotai || die "cannot extract dotai from $archive"
  mkdir -p "$dir" || die "cannot create $dir"
  # Copied next to the target, then renamed over it: the rename is atomic.
  new=$dir/.dotai.new.$$
  if ! { cp "$tmp/x/dotai" "$new" && chmod 0755 "$new" && mv -f "$new" "$dir/dotai"; }; then
    die "cannot install $dir/dotai; the previous dotai, if any, is unchanged"
  fi
  new=
  echo "installed $dir/dotai"

  if command -v gh >/dev/null 2>&1; then
    echo "to check its build provenance, download $base/download/$tag/$archive and run:"
    echo "  gh attestation verify $archive --repo Lartweib/dotai"
  fi
  add_to_path
  "$dir/dotai" version
}

main "$@"
