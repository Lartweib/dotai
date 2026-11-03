#!/usr/bin/env bash
# Builds the release archives of dotai and their SHA256SUMS.
#
# Usage: scripts/release.sh <tag> <out-dir>
#
# <tag> is a SemVer tag such as v0.1.0. It is stamped into the binary as
# the version that `dotai version` prints. The archives are named after it
# without the leading v, for example dotai_0.1.0_linux_amd64.tar.gz.
# The release workflow runs this script; it can be run locally as well.
set -euo pipefail

if [ $# -ne 2 ]; then
  echo "usage: $0 <tag> <out-dir>" >&2
  exit 2
fi
tag=$1
out=$2

semver='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$'
if ! [[ $tag =~ $semver ]]; then
  echo "$tag is not a SemVer tag such as v0.1.0" >&2
  exit 2
fi
version=${tag#v}

# The platforms of the rulesync checksum table in internal/rulesync.
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
    (cd "$dir" && zip -q -X "$out/$name.zip" "$bin" LICENSE README.md)
  else
    tar -C "$dir" --sort=name --mtime="@$mtime" --owner=0 --group=0 --numeric-owner \
      -cf - "$bin" LICENSE README.md | gzip -n -9 >"$out/$name.tar.gz"
  fi
done

(cd "$out" && sha256sum dotai_"${version}"_* >SHA256SUMS)
cat "$out/SHA256SUMS"
