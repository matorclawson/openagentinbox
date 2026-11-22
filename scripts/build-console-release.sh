#!/usr/bin/env bash
# Build the service and copy the static web application into an empty release.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
out=${1:?supply a release directory}
mkdir -p "$out"
out=$(cd "$out" && pwd)
case "$(uname -s)/$(uname -m)" in
 Linux/aarch64|Linux/arm64) platform=linux-arm64;;
 Darwin/arm64) platform=darwin-arm64;;
 Linux/x86_64) platform=linux-amd64;;
 *) platform=source;;
esac
if [[ -f "$root/dist/$platform/openagentinboxd" && -d "$root/dist/ui" ]]; then
 install -m 755 "$root/dist/$platform/openagentinboxd" "$out/openagentinboxd"
 cp -R "$root/dist/ui" "$out/ui"
else
 (cd "$root"; CGO_ENABLED=0 go build -trimpath -o "$out/openagentinboxd" ./cmd/openagentinboxd)
 (cd "$root/web"; npm ci --no-audit --no-fund; npm run typecheck; NEXT_TELEMETRY_DISABLED=1 npm run build)
 cp -R "$root/web/out" "$out/ui"
fi
