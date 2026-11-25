#!/usr/bin/env bash
# Build the release artifacts from the current clean commit into OUTDIR (default build/release):
#   openagentinbox-install-VERSION.sh   the installer, carrying the daemons, the static web app and the deploy scripts
#   openagentinbox-VERSION-source.zip   git archive of the commit
#   openagentinbox-client-VERSION-TARGET  the client for linux-arm64, linux-amd64 and darwin-arm64
#   openagentinbox-0.1.0-py3-none-any.whl  the Python client
#   SHA256SUMS                          sha256 of every file above
# Run it through `make release` on ENG-UK, which builds the web app. The inputs are the commit
# and the pinned toolchains: the file times come from the commit, the Go binaries use -trimpath
# and no build id, and the Next build id is the commit. Build twice with `make release-check`.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
out=${1:-build/release}
if [[ -n $(git status --porcelain) && ${INBOX_RELEASE_ALLOW_DIRTY:-} != yes ]]; then
  echo 'Refusing to build a release from a dirty tree.' >&2; exit 2
fi
version=$(tr -d '[:space:]' < VERSION)
commit=$(git rev-parse HEAD)
epoch=$(git log -1 --format=%ct)
export SOURCE_DATE_EPOCH=$epoch INBOX_BUILD_ID=${commit:0:12} NEXT_TELEMETRY_DISABLED=1
rm -rf "$out"; mkdir -p "$out"
out=$(cd "$out" && pwd)
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
echo "building $version from $commit"
echo "go: $(go version)"
echo "node: $(node --version) npm: $(npm --version) python: $(python3 --version)"
(cd web && npm ci --include=dev --no-audit --no-fund && npm run typecheck && npm run build)
mkdir -p "$stage/dist" "$stage/scripts"
cp -R web/out "$stage/dist/ui"
bash scripts/build-clients.sh "$stage/dist/ui"
for target in linux-arm64 linux-amd64 darwin-arm64; do
  mkdir -p "$stage/dist/$target"
  CGO_ENABLED=0 GOOS=${target%-*} GOARCH=${target#*-} go build -trimpath -ldflags='-s -w -buildid=' -o "$stage/dist/$target/openagentinboxd" ./cmd/openagentinboxd
done
install -m 755 install-openagentinbox.sh "$stage/install-openagentinbox.sh"
install -m 755 scripts/build-console-release.sh "$stage/scripts/build-console-release.sh"
mkdir -p "$stage/web"
cp -R web/deploy "$stage/web/deploy"
find "$stage/dist/ui" -name .DS_Store -delete
python3 scripts/package-release.py "$stage" "$out" "$version" "$epoch" >/dev/null
git archive --format=zip --prefix="openagentinbox-$version/" -o "$out/openagentinbox-$version-source.zip" HEAD
for target in linux-arm64 linux-amd64 darwin-arm64; do
  install -m 755 "$stage/dist/ui/clients/$target" "$out/openagentinbox-client-$version-$target"
done
python3 - "$out" <<'PYEOF'
import hashlib, os, sys
out = sys.argv[1]
names = sorted(n for n in os.listdir(out) if n != "SHA256SUMS")
with open(os.path.join(out, "SHA256SUMS"), "w") as f:
    for n in names:
        f.write("%s  %s\n" % (hashlib.sha256(open(os.path.join(out, n), "rb").read()).hexdigest(), n))
PYEOF
echo "built in $out from $commit:"
cat "$out/SHA256SUMS"
