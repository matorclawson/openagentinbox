#!/usr/bin/env bash
# Generate the standalone source installer from the committed source.
set -euo pipefail
root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
target=${1:?usage: package-installer.sh OUTPUT.sh}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for file in dist/linux-amd64/openagentinboxd dist/linux-arm64/openagentinboxd dist/darwin-arm64/openagentinboxd dist/ui/index.html; do
  [[ -f "$root/$file" ]] || { echo "Missing release asset: $file" >&2; exit 1; }
done
tar -czf "$tmp/payload.tgz" -C "$root" install-openagentinbox.sh scripts/build-console-release.sh web/deploy dist
cat > "$target" <<'HEADER'
#!/usr/bin/env bash
set -euo pipefail
umask 077
export INBOX_INSTALL_ARCHIVE="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
staging=$(mktemp -d)
trap 'rm -rf "$staging"' EXIT
base64 -d <<'INBOX_PAYLOAD' | tar -xz -C "$staging"
HEADER
base64 < "$tmp/payload.tgz" >> "$target"
printf '\nINBOX_PAYLOAD\nbash "$staging/install-openagentinbox.sh" "$@"\n' >> "$target"
chmod 755 "$target"
