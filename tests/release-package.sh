#!/usr/bin/env bash
# Prove the installer and wheel packaging is deterministic and independent of the tools and
# state of the machine: the same staged files give the same installer and wheel bytes, whatever
# their creation order, times, owner or umask. Uses Python's standard library only (no tar or
# gzip binary is involved in writing the archives). Writes only to a temporary directory.
set -euo pipefail
cd "$(dirname "$0")/.."
repo=$PWD
work=$(cd "$(mktemp -d)" && pwd -P)
trap 'rm -rf -- "$work"' EXIT
fail(){ echo "FAIL: $*" >&2; exit 1; }
epoch=1700000000
stage(){ # stage DIR REVERSE UMASK TOUCHTIME
  local d=$1 order=$2 mask=$3 when=$4 f
  local files=(install-openagentinbox.sh scripts/build-console-release.sh web/deploy/install-native.sh dist/linux-arm64/openagentinboxd dist/ui/index.html dist/ui/_next/static/a.js dist/ui/clients/linux-arm64)
  [[ $order == reverse ]] && files=("${files[@]:4}" "${files[@]:0:4}")
  (
    umask "$mask"
    for f in "${files[@]}"; do
      mkdir -p "$d/$(dirname "$f")"
      printf 'content of %s\n' "$f" > "$d/$f"
    done
    chmod 755 "$d/install-openagentinbox.sh" "$d/scripts/build-console-release.sh" "$d/dist/linux-arm64/openagentinboxd" "$d/dist/ui/clients/linux-arm64"
    find "$d" -exec touch -t "$when" {} +
  )
}
stage "$work/s1" forward 022 202001010000
stage "$work/s2" reverse 077 202506151230
mkdir "$work/o1" "$work/o2"
python3 -I scripts/package-release.py "$work/s1" "$work/o1" 9.9.9 "$epoch" >/dev/null
python3 -I scripts/package-release.py "$work/s2" "$work/o2" 9.9.9 "$epoch" >/dev/null
for f in openagentinbox-install-9.9.9.sh openagentinbox-0.1.0-py3-none-any.whl; do
  [[ -f $work/o1/$f && -f $work/o2/$f ]] || fail "$f was not written"
  cmp -s "$work/o1/$f" "$work/o2/$f" || fail "$f differs between two packagings of the same files"
done
# A change in the epoch must change the output, so the comparison above is not vacuous.
mkdir "$work/o3"
python3 -I scripts/package-release.py "$work/s1" "$work/o3" 9.9.9 $((epoch + 2)) >/dev/null
! cmp -s "$work/o1/openagentinbox-install-9.9.9.sh" "$work/o3/openagentinbox-install-9.9.9.sh" || fail 'the epoch does not reach the installer'
# The payload lists sorted names with root ownership and fixed times.
sed -n '/^base64 -d <<.INBOX_PAYLOAD./,/^INBOX_PAYLOAD$/p' "$work/o1/openagentinbox-install-9.9.9.sh" | sed '1d;$d' | base64 -d > "$work/payload.tgz"
python3 -I - "$work/payload.tgz" "$epoch" <<'PYEOF'
import sys, tarfile
t = tarfile.open(sys.argv[1])
ms = t.getmembers()
names = [m.name for m in ms]
assert names == sorted(names), "payload entries are not sorted"
assert all(m.uid == 0 and m.gid == 0 and m.mtime == int(sys.argv[2]) for m in ms), "payload ownership or time is not fixed"
assert {m.name for m in ms if m.isfile() and m.mode == 0o755} == {"install-openagentinbox.sh", "scripts/build-console-release.sh", "dist/linux-arm64/openagentinboxd", "dist/ui/clients/linux-arm64"}, "payload modes are not normalized"
PYEOF
[[ $(head -c 2 "$work/payload.tgz" | od -An -tx1 | tr -d ' \n') == 1f8b ]] || fail 'the payload is not gzip'
# The gzip header carries no time or name: bytes 4 to 8 are the modification time.
[[ $(head -c 8 "$work/payload.tgz" | tail -c 4 | od -An -tx1 | tr -d ' \n') == 00000000 ]] || fail 'the gzip header carries a time'
echo 'release-package: ok'
