#!/usr/bin/env bash
set -euo pipefail
out=${1:?expected static UI directory}/clients
mkdir -p "$out"
out=$(cd "$out" && pwd)
cd "$(dirname "$0")/.."
for target in linux-arm64 linux-amd64 darwin-arm64; do
  (CGO_ENABLED=0 GOOS=${target%-*} GOARCH=${target#*-} go build -trimpath -ldflags='-s -w' -o "$out/$target" ./cmd/openagentinbox-client)
done
(cd "$out"; for target in linux-* darwin-*; do shasum -a 256 "$target"; done > SHA256SUMS)
