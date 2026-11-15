#!/usr/bin/env bash
# Run every Fuzz* target in the module for $FUZZTIME (default 30s) each.
set -euo pipefail

fuzztime="${FUZZTIME:-30s}"

for pkg in $(go list ./...); do
  for target in $(go test -list '^Fuzz' "$pkg" | grep '^Fuzz' || true); do
    echo "==> $pkg $target ($fuzztime)"
    go test "$pkg" -run '^$' -fuzz "^${target}\$" -fuzztime "$fuzztime"
  done
done
