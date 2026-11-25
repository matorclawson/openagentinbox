#!/usr/bin/env bash
# Prove the release output-directory guard: it accepts only a new or empty directory strictly
# inside the designated root, and never removes anything. Uses a temporary directory only.
set -euo pipefail
cd "$(dirname "$0")/.."
guard=$PWD/scripts/release-outdir.sh
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
root=$work/build
mkdir -p "$root" "$work/elsewhere"
export INBOX_RELEASE_BUILD_ROOT=$root
fail(){ echo "FAIL: $*" >&2; exit 1; }
refuses(){
  local why=$1; shift
  if bash "$guard" "$@" >/dev/null 2>"$work/err"; then fail "accepted: $why"; fi
  grep -Fq 'release output directory refused' "$work/err" || fail "no refusal message: $why"
}
# Accepted: a new directory, nested, and an existing empty directory.
[[ $(bash "$guard" "$root/release-a") == "$(cd "$root" && pwd -P)/release-a" ]] || fail 'new directory'
[[ -d $root/release-a ]] || fail 'new directory was not created'
mkdir "$root/empty"; bash "$guard" "$root/empty" >/dev/null || fail 'empty directory'
bash "$guard" "$root/nested/deeper/release" >/dev/null || fail 'nested new directory'
# Refused, and untouched: a directory with content keeps its content.
mkdir "$root/full"; echo keep > "$root/full/sentinel"
refuses 'a non-empty directory' "$root/full"
[[ $(cat "$root/full/sentinel") == keep ]] || fail 'a refused directory lost its content'
echo keep > "$root/file"; refuses 'a regular file' "$root/file"
[[ $(cat "$root/file") == keep ]] || fail 'a refused file changed'
# Refused: outside the root, the root itself, relative components, symlinks, nothing.
echo keep > "$work/elsewhere/sentinel"
refuses 'outside the root' "$work/elsewhere/release"
refuses 'the root itself' "$root"
refuses 'the filesystem root' /
refuses 'a parent component' "$root/../elsewhere/release"
refuses 'a dot component' "$root/./release-b"
refuses 'no argument'
ln -s "$work/elsewhere" "$root/link"; refuses 'a symbolic link' "$root/link"
mkdir -p "$work/real"; ln -s "$work/real" "$root/dirlink"; refuses 'a path through a symlinked directory' "$root/dirlink/release"
[[ $(cat "$work/elsewhere/sentinel") == keep && ! -e $work/real/release ]] || fail 'something outside the root changed'
# The build script has no override for a dirty tree and never removes a caller-supplied path.
! grep -q 'ALLOW_DIRTY' scripts/build-release.sh || fail 'build-release.sh has a dirty-tree override'
! grep -Eq 'rm -rf[^"]*"\$out"|rm -rf[^"]*"\$1"' scripts/build-release.sh || fail 'build-release.sh removes the output directory'
echo 'release-outdir: ok'
