#!/usr/bin/env bash
# Builds the release archives of dotai and their SHA256SUMS.
#
# Usage: scripts/release.sh <tag> <out-dir>
#        scripts/release.sh --self-test
#        scripts/release.sh --check-notices
#
# <tag> is a SemVer 2.0.0 tag such as v0.1.0 or v0.2.0-rc.1, without build
# metadata. It is stamped into the binary as the version that
# `dotai version` prints. The archives are named after it without the
# leading v, for example dotai_0.1.0_linux_amd64.tar.gz. <out-dir> must be
# missing or empty: the script refuses to write into a directory that
# already holds files.
#
# The script needs GNU tools (bash, GNU tar and coreutils, Info-ZIP zip)
# and is meant for Linux, as in the release workflow. It checks them before
# it builds anything. --self-test checks the tag pattern and needs none of
# them. --check-notices builds the third-party notices of every target and
# needs only go.
#
# Every archive holds THIRD_PARTY_NOTICES: the license texts of the Go
# standard library, of each module linked into that target's binary, read
# from the module cache, and of the fonts embedded in the web UI. It is
# generated from the module graph at build time, so it cannot drift from
# go.mod.
set -euo pipefail
# Byte order for sort and globs, so that the notices do not depend on the
# runner's locale.
export LC_ALL=C

# SemVer 2.0.0: numeric identifiers have no leading zeros, and pre-release
# identifiers are non-empty, alphanumeric or hyphen.
num='(0|[1-9][0-9]*)'
pre_id="($num|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
semver="^v$num\\.$num\\.$num(-$pre_id(\\.$pre_id)*)?\$"

valid_tag() { [[ $1 =~ $semver ]]; }

self_test() {
  local tag failed=0
  for tag in v0.1.0 v1.2.3 v10.20.30 v0.2.0-rc.1 v1.0.0-alpha v1.0.0-alpha.beta \
    v1.0.0-0.3.7 v1.0.0-x.7.z.92 v1.0.0-x-y-z.-- v1.0.0-0a v1.0.0-a01; do
    valid_tag "$tag" || { echo "self-test: $tag was rejected, want accepted" >&2; failed=1; }
  done
  for tag in 0.1.0 v1.2 v1.2.3.4 v01.0.0 v1.02.0 v1.0.00 v1.0.0- v1.0.0-rc..1 v1.0.0-rc. \
    v1.0.0-.rc v1.0.0-01 v1.0.0-rc.01 v1.0.0+build v1.0.0-rc_1 V1.0.0; do
    valid_tag "$tag" && { echo "self-test: $tag was accepted, want rejected" >&2; failed=1; }
  done
  [ "$failed" = 0 ] && echo "self-test: ok"
  return "$failed"
}

# The platforms of the rulesync checksum table, the releases map in
# internal/rulesync/rulesync.go. A platform is added to both together.
targets="windows/amd64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"

root=$(cd "$(dirname "$0")/.." && pwd)

rule() { printf '
%s
%s
' "========================================================================" "$1"; }

# license_files prints the license, notice and copying files at the top of a
# module directory, one per line.
license_files() {
  local f
  for f in "$1"/*; do
    [ -f "$f" ] || continue
    case ${f##*/} in
      [Ll][Ii][Cc][Ee][Nn][CcSs][Ee]* | [Nn][Oo][Tt][Ii][Cc][Ee]* | [Cc][Oo][Pp][Yy][Ii][Nn][Gg]*) echo "$f" ;;
    esac
  done
}

# notices prints THIRD_PARTY_NOTICES for the binary of one target. It fails
# when a linked module has no license file, so that a new dependency cannot
# ship without its notice.
notices() {
  local goos=$1 goarch=$2 mods path version dir files f font fontname
  mods=$(cd "$root" && CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go list -deps     -f '{{if not .Standard}}{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}{{end}}'     ./cmd/dotai | sort -u)
  echo "Third-party notices for the dotai binary ($goos/$goarch)"
  echo
  echo "dotai is under the MIT license in LICENSE. Its binary includes the software"
  echo "below; each entry reproduces that software's license and notice files."
  rule "Go standard library ($(go env GOVERSION))"
  echo
  cat "$(go env GOROOT)/LICENSE"
  while read -r path version dir; do
    [ -n "$path" ] || continue
    if [ -z "$dir" ]; then
      echo "module $path $version is not in the module cache" >&2
      return 1
    fi
    files=$(license_files "$dir")
    if [ -z "$files" ]; then
      echo "module $path $version has no license file" >&2
      return 1
    fi
    rule "$path $version"
    while read -r f; do
      printf '
--- %s ---

' "${f##*/}"
      cat "$f"
    done <<<"$files"
  done <<<"$mods"
  for font in "$root"/internal/server/web/fonts/OFL-*.txt; do
    [ -f "$font" ] || { echo "no font license in internal/server/web/fonts" >&2; return 1; }
    fontname=${font##*/}
    fontname=${fontname#OFL-}
    rule "Font ${fontname%.txt} (embedded in the web UI)"
    echo
    cat "$font"
  done
}

if [ "${1-}" = --self-test ] && [ $# -eq 1 ]; then
  self_test
  exit
fi
if [ "${1-}" = --check-notices ] && [ $# -eq 1 ]; then
  for target in $targets; do
    notices "${target%/*}" "${target#*/}" >/dev/null
  done
  echo "notices: ok"
  exit
fi
if [ $# -ne 2 ]; then
  echo "usage: $0 <tag> <out-dir> | $0 --self-test" >&2
  exit 2
fi
tag=$1
out=$2

if ! valid_tag "$tag"; then
  echo "$tag is not a SemVer tag such as v0.1.0 or v0.2.0-rc.1" >&2
  exit 2
fi
version=${tag#v}

# Preflight: every tool the archives need, before anything is built.
missing=()
for tool in go git zip sha256sum gzip; do
  command -v "$tool" >/dev/null || missing+=("$tool")
done
tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -cf /dev/null --files-from /dev/null 2>/dev/null ||
  missing+=("GNU tar (--sort, --mtime)")
probe=$(mktemp)
touch -d @0 "$probe" 2>/dev/null || missing+=("GNU touch (-d @<seconds>)")
rm -f "$probe"
if [ ${#missing[@]} -gt 0 ]; then
  printf 'missing: %s\n' "${missing[@]}" >&2
  echo "this script needs GNU tools and is meant for Linux, as in the release workflow" >&2
  exit 1
fi

if [ -e "$out" ] && [ -n "$(ls -A "$out")" ]; then
  echo "$out is not empty; give a missing or empty output directory" >&2
  exit 1
fi

# The commit time, so that the archive entries do not depend on when the
# build ran.
mtime=$(git -C "$root" log -1 --format=%ct)

mkdir -p "$out"
out=$(cd "$out" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

for target in $targets; do
  goos=${target%/*}
  goarch=${target#*/}
  name="dotai_${version}_${goos}_${goarch}"
  dir="$work/$name"
  bin=dotai
  [ "$goos" = windows ] && bin=dotai.exe
  mkdir -p "$dir"
  echo "building $goos/$goarch"
  (cd "$root" && CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath \
    -ldflags "-s -w -X github.com/lartweib/dotai/internal/app.version=$tag" \
    -o "$dir/$bin" ./cmd/dotai)
  cp "$root/LICENSE" "$root/README.md" "$dir/"
  notices "$goos" "$goarch" >"$dir/THIRD_PARTY_NOTICES"
  chmod 0755 "$dir/$bin"
  chmod 0644 "$dir/LICENSE" "$dir/README.md" "$dir/THIRD_PARTY_NOTICES"
  touch -d "@$mtime" "$dir"/*
  if [ "$goos" = windows ]; then
    # zip stores local time; UTC keeps the entries independent of the
    # runner's time zone.
    (cd "$dir" && TZ=UTC zip -q -X "$out/$name.zip" "$bin" LICENSE README.md THIRD_PARTY_NOTICES)
  else
    tar -C "$dir" --sort=name --mtime="@$mtime" --owner=0 --group=0 --numeric-owner \
      -cf - "$bin" LICENSE README.md THIRD_PARTY_NOTICES | gzip -n -9 >"$out/$name.tar.gz"
  fi
done

(cd "$out" && sha256sum dotai_"${version}"_* >SHA256SUMS)
cat "$out/SHA256SUMS"
