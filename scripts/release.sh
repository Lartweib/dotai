#!/usr/bin/env bash
# Builds the release archives of dotai and their SHA256SUMS.
#
# Usage: scripts/release.sh <tag> <out-dir>
#        scripts/release.sh --self-test
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
# them.
set -euo pipefail

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

if [ "${1-}" = --self-test ] && [ $# -eq 1 ]; then
  self_test
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

# The platforms of the rulesync checksum table, the releases map in
# internal/rulesync/rulesync.go. A platform is added to both together.
targets="windows/amd64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"

root=$(cd "$(dirname "$0")/.." && pwd)
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
  chmod 0755 "$dir/$bin"
  chmod 0644 "$dir/LICENSE" "$dir/README.md"
  touch -d "@$mtime" "$dir"/*
  if [ "$goos" = windows ]; then
    # zip stores local time; UTC keeps the entries independent of the
    # runner's time zone.
    (cd "$dir" && TZ=UTC zip -q -X "$out/$name.zip" "$bin" LICENSE README.md)
  else
    tar -C "$dir" --sort=name --mtime="@$mtime" --owner=0 --group=0 --numeric-owner \
      -cf - "$bin" LICENSE README.md | gzip -n -9 >"$out/$name.tar.gz"
  fi
done

(cd "$out" && sha256sum dotai_"${version}"_* >SHA256SUMS)
cat "$out/SHA256SUMS"
