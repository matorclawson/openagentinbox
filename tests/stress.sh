#!/usr/bin/env bash
# Run the packaged Linux tools or build the same tools from a source checkout.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
[[ $(uname -s) == Linux ]] || { echo 'Linux is required for per-process metrics.' >&2; exit 1; }
case $(uname -m) in aarch64|arm64) arch=arm64;; x86_64|amd64) arch=amd64;; *) arch=source;; esac
tools="$root/bin/linux-$arch"
if [[ ! -x "$tools/openagentinbox-stress" ]]; then
  tools=$(mktemp -d)
  trap 'rm -rf -- "$tools"' EXIT
  (cd "$root"; for tool in openagentinboxd openagentinbox-backup openagentinbox-stress; do
    CGO_ENABLED=0 go build -trimpath -o "$tools/$tool" "./cmd/$tool"
  done)
fi
"$tools/openagentinbox-stress" -daemon "$tools/openagentinboxd" -backup "$tools/openagentinbox-backup" "$@"
