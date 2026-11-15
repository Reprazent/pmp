#!/usr/bin/env bash
# Render Formula/pmp.rb from packaging/pmp.rb.tmpl.
# Usage: scripts/render-formula.sh <version> <checksums.txt>
set -euo pipefail

version="$1"
checksums="$2"
root="$(cd "$(dirname "$0")/.." && pwd)"

args=(-e "s/@VERSION@/${version}/g")
for target in darwin_arm64 darwin_amd64 linux_arm64 linux_amd64; do
  sha="$(awk -v f="pmp_${version}_${target}.tar.gz" '$2 == f { print $1 }' "$checksums")"
  if [ -z "$sha" ]; then
    echo "missing checksum for ${target}" >&2
    exit 1
  fi
  args+=(-e "s/@SHA_${target}@/${sha}/g")
done

sed "${args[@]}" "$root/packaging/pmp.rb.tmpl"
