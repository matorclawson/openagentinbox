#!/usr/bin/env bash
# Prove the release output-directory guard. It runs a copy of the script inside a throwaway
# repository layout, so the allowed root is that layout's build/ and nothing of the real tree is
# touched. The guard must claim only a new name directly under build/, check everything before
# writing, claim it exclusively, and remove nothing.
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(cd "$(mktemp -d)" && pwd -P)
err=$(mktemp)
trap 'rm -rf -- "$work" "$err"' EXIT
repo=$work/repo
mkdir -p "$repo/scripts" "$repo/build" "$work/elsewhere"
cp scripts/release-outdir.sh "$repo/scripts/"
guard=$repo/scripts/release-outdir.sh
physical=$repo
fail(){ echo "FAIL: $*" >&2; exit 1; }
refuses(){
  local why=$1; shift
  if bash "$guard" "$@" >/dev/null 2>"$err"; then fail "accepted: $why"; fi
  grep -Fq 'release output directory refused' "$err" || fail "no refusal message: $why"
}
snapshot(){ (cd "$work" && find . -mindepth 1 | sort); }
# A new name is claimed and printed in physical form.
[[ $(bash "$guard" "$repo/build/rel-a") == "$physical/build/rel-a" ]] || fail 'new name'
[[ -d $repo/build/rel-a ]] || fail 'the directory was not created'
(cd "$repo" && bash scripts/release-outdir.sh build/rel-relative >/dev/null) || fail 'relative name'
# Positive control: every output name the Makefile passes to the build script is accepted, as a
# relative path from the repository root (how make runs it) and as an absolute path.
names=$(grep -o 'scripts/build-release.sh build/[^ ]*' Makefile | sed 's|scripts/build-release.sh ||; s|\$(COMMIT)|0123456789ab|g')
[[ $(echo "$names" | wc -l) -ge 3 ]] || fail 'could not read the output names from the Makefile'
for name in $names; do
  (cd "$repo" && bash scripts/release-outdir.sh "$name" >/dev/null) || fail "the guard refused the Makefile's own name $name"
  [[ -d $repo/$name ]] || fail "the Makefile name $name was not created"
  bash "$guard" "$repo/${name}-abs" >/dev/null || fail "the guard refused the absolute form of $name"
done
# Refused, and nothing is created or changed: compare the whole tree before and after.
mkdir "$repo/build/empty" "$repo/build/full"; echo keep > "$repo/build/full/sentinel"; echo keep > "$repo/build/file"
ln -s "$work/elsewhere" "$repo/build/link"; echo keep > "$work/elsewhere/sentinel"
before=$(snapshot)
refuses 'an existing empty directory' "$repo/build/empty"
refuses 'a non-empty directory' "$repo/build/full"
refuses 'a regular file' "$repo/build/file"
refuses 'a symbolic link' "$repo/build/link"
refuses 'a nested name' "$repo/build/nested/deeper"
refuses 'a grandchild of build' "$repo/build/rel-a/inner"
refuses 'outside build' "$work/elsewhere/release"
refuses 'the build directory itself' "$repo/build"
refuses 'the repository root' "$repo"
refuses 'the filesystem root' /
refuses 'a parent component' "$repo/build/../elsewhere"
refuses 'a dot component' "$repo/build/./rel-b"
refuses 'an empty component' "$repo/build//rel-c"
refuses 'no argument'
INBOX_RELEASE_BUILD_ROOT=$work/elsewhere refuses 'a path under an environment-chosen root' "$work/elsewhere/release"
[[ $(snapshot) == "$before" ]] || fail 'a refusal created or changed something'
[[ $(cat "$repo/build/full/sentinel") == keep && $(cat "$work/elsewhere/sentinel") == keep ]] || fail 'content changed'
# No build directory: refused before anything is created.
mkdir "$work/bare"; mkdir -p "$work/bare/scripts"; cp "$guard" "$work/bare/scripts/"
if bash "$work/bare/scripts/release-outdir.sh" "$work/bare/build/x" >/dev/null 2>&1; then fail 'accepted without a build directory'; fi
[[ ! -e $work/bare/build ]] || fail 'a refusal created build/'
# A symlinked build directory is refused.
mkdir -p "$work/symrepo/scripts" "$work/target"; cp "$guard" "$work/symrepo/scripts/"; ln -s "$work/target" "$work/symrepo/build"
if bash "$work/symrepo/scripts/release-outdir.sh" "$work/symrepo/build/x" >/dev/null 2>&1; then fail 'accepted a symlinked build directory'; fi
[[ -z $(ls -A "$work/target") ]] || fail 'wrote through a symlinked build directory'
# Exclusive claim: of many concurrent claims on one name exactly one succeeds.
for i in 1 2 3 4 5 6 7 8; do ( bash "$guard" "$repo/build/race" >/dev/null 2>&1 && touch "$work/won.$i" || true ) & done
wait
winners=$(ls "$work"/won.* 2>/dev/null | wc -l | tr -d ' ')
[[ $((winners)) -eq 1 ]] || fail "expected exactly one winner, got $winners"
# No override of the allowed root, and the build script keeps no dirty override or output removal.
! grep -q 'INBOX_RELEASE_BUILD_ROOT' scripts/release-outdir.sh || fail 'the guard reads an environment override'
! grep -q 'ALLOW_DIRTY' scripts/build-release.sh || fail 'build-release.sh has a dirty-tree override'
! grep -Eq 'rm -rf[^"]*"\$(out|1|out_arg)"' scripts/build-release.sh || fail 'build-release.sh removes the output directory'
echo 'release-outdir: ok'
