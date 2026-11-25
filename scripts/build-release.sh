#!/usr/bin/env bash
# Build the release artifacts from the exact committed tree into a NEW directory under build/.
#   openagentinbox-install-VERSION.sh       the installer: daemons, static web app and deploy scripts
#   openagentinbox-VERSION-source.zip       git archive of the commit
#   openagentinbox-client-VERSION-TARGET    the client for linux-arm64, linux-amd64 and darwin-arm64
#   openagentinbox-0.1.0-py3-none-any.whl   the Python client
#   MANIFEST.json                           commit, tree, toolchains, lockfile hashes, every file's sha256
#   SHA256SUMS                              sha256 of every file above, MANIFEST.json included
# Usage: build-release.sh OUTDIR   (OUTDIR is new or empty, inside build/; see release-outdir.sh)
# Run it through `make release` on ENG-UK. Nothing is built from the working tree: the commit is
# exported with git archive into a temporary directory and built there, with npm ci from the
# lockfile and the Go modules read-only. A dirty working tree is refused; there is no override.
# Nothing already on disk is deleted except the temporary export this script creates.
set -euo pipefail
cd "$(dirname "$0")/.."
repo=$PWD
out=$(bash scripts/release-outdir.sh "${1:?usage: build-release.sh OUTDIR}")
if [[ -n $(git status --porcelain --untracked-files=all) ]]; then
  echo 'Refusing to build a release from a dirty working tree.' >&2; exit 2
fi
commit=$(git rev-parse HEAD)
tree=$(git rev-parse 'HEAD^{tree}')
epoch=$(git log -1 --format=%ct)
export SOURCE_DATE_EPOCH=$epoch INBOX_BUILD_ID=${commit:0:12} NEXT_TELEMETRY_DISABLED=1
export GOFLAGS=-mod=readonly GOTOOLCHAIN=local CGO_ENABLED=0
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
src=$work/src; stage=$work/stage
mkdir -p "$src" "$stage/dist" "$stage/scripts" "$stage/web"
git archive --format=tar HEAD | tar -x -C "$src"
version=$(tr -d '[:space:]' < "$src/VERSION")
echo "building $version from commit $commit tree $tree"
echo "go: $(go version)"
echo "node: $(node --version) npm: $(npm --version) python: $(python3 --version 2>&1)"
cd "$src"
(cd web && npm ci --include=dev --no-audit --no-fund && npm run typecheck && npm run build)
cp -R web/out "$stage/dist/ui"
bash scripts/build-clients.sh "$stage/dist/ui"
for target in linux-arm64 linux-amd64 darwin-arm64; do
  mkdir -p "$stage/dist/$target"
  GOOS=${target%-*} GOARCH=${target#*-} go build -trimpath -ldflags='-s -w -buildid=' -o "$stage/dist/$target/openagentinboxd" ./cmd/openagentinboxd
done
install -m 755 install-openagentinbox.sh "$stage/install-openagentinbox.sh"
install -m 755 scripts/build-console-release.sh "$stage/scripts/build-console-release.sh"
cp -R web/deploy "$stage/web/deploy"
find "$stage/dist/ui" -name .DS_Store -delete
python3 scripts/package-release.py "$stage" "$out" "$version" "$epoch" >/dev/null
(cd "$repo" && git archive --format=zip --prefix="openagentinbox-$version/" -o "$out/openagentinbox-$version-source.zip" "$commit")
for target in linux-arm64 linux-amd64 darwin-arm64; do
  install -m 755 "$stage/dist/ui/clients/$target" "$out/openagentinbox-client-$version-$target"
done
GO_VERSION=$(go version) NODE_VERSION=$(node --version) NPM_VERSION=$(npm --version) PY_VERSION=$(python3 --version 2>&1) \
UNAME=$(uname -sm) COMMIT=$commit TREE=$tree VERSION=$version EPOCH=$epoch \
python3 scripts/release-manifest.py "$out" "$src"
echo "built in $out from commit $commit:"
cat "$out/SHA256SUMS"
