#!/usr/bin/env bash
set -euo pipefail
umask 077
src=$(cd "$(dirname "$0")/web" && pwd)
prefix=${INBOX_INSTALL_DIR:-"$HOME/.local/opt/openagentinbox"}
bindir=${INBOX_BIN_DIR:-"$HOME/.local/bin"}
case "$prefix" in /*) ;; *) echo 'INBOX_INSTALL_DIR must be absolute' >&2; exit 2;; esac
if [[ "${1:-}" == agent ]]; then
  shift
  export INBOX_INSTALL_SOURCE="$(cd "$src/.." && pwd)"
  case "$(uname -s)/$(uname -m)" in
    Linux/aarch64|Linux/arm64) platform=linux-arm64;;
    Linux/x86_64) platform=linux-amd64;;
    Darwin/arm64) platform=darwin-arm64;;
    *) platform=source;;
  esac
  if [[ -x "$INBOX_INSTALL_SOURCE/dist/$platform/openagentinboxd" ]]; then
    exec "$INBOX_INSTALL_SOURCE/dist/$platform/openagentinboxd" deploy "$@"
  fi
  cd "$INBOX_INSTALL_SOURCE"
  exec go run ./cmd/openagentinboxd deploy "$@"
fi
if [[ "${1:-}" == "--upgrade" ]]; then shift; exec bash "$src/deploy/upgrade-native.sh" "$@"; fi
if [[ "${1:-}" == "--vps" ]]; then
  shift
  exec bash "$src/deploy/install-native.sh" "$@"
fi
if [[ $# != 0 ]]; then echo 'Usage: installer [--vps inbox.example.org]' >&2; exit 2; fi
mkdir -p "$prefix/releases" "$bindir"
stage=$(mktemp -d "$prefix/releases/install.XXXXXXXX")
trap 'if [[ -n "${stage:-}" ]]; then rm -rf -- "$stage"; fi' EXIT
bash "$src/../scripts/build-console-release.sh" "$stage"
ln -s "$stage" "$prefix/active.next.$$"
# Replace the symlink atomically on both GNU and BSD systems.
if [[ $(uname -s) == Darwin ]]; then mv -fh "$prefix/active.next.$$" "$prefix/active"; else mv -Tf "$prefix/active.next.$$" "$prefix/active"; fi
stage=''
launcher=$(mktemp "$bindir/.openagentinbox.XXXXXXXX")
printf '#!/usr/bin/env bash\nset -eu\ncd %q\nexec ./openagentinboxd serve\n' "$prefix/active" > "$launcher"
chmod 700 "$launcher"
mv -f "$launcher" "$bindir/openagentinbox"
echo "Installed launcher: $bindir/openagentinbox"
if [[ "${INBOX_INSTALL_ONLY:-0}" != 1 ]]; then exec "$bindir/openagentinbox"; fi
