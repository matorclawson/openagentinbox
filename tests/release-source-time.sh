#!/usr/bin/env bash
# The release's source-zip check must pass on a host that is not on UTC. git archive writes zip
# member times in the host's local time, so the build exports TZ=UTC before it archives. This
# makes a commit in British Summer Time, archives it the way the build does and runs the check
# under TZ=Europe/London. Writes only to a temporary directory.
set -euo pipefail
cd "$(dirname "$0")/.."
repo=$PWD
work=$(cd "$(mktemp -d)" && pwd -P)
trap 'rm -rf -- "$work"' EXIT
fail(){ echo "FAIL: $*" >&2; exit 1; }
git init -q "$work/r"
cd "$work/r"
mkdir -p dir/sub
printf 'one\n' > a.txt; printf 'two\n' > dir/b.txt; printf 'three\n' > dir/sub/c.txt
git add -A
when='2026-07-01T12:00:01+0100'
GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.invalid GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.invalid \
  GIT_AUTHOR_DATE=$when GIT_COMMITTER_DATE=$when git commit -q -m fixture
commit=$(git rev-parse --verify 'HEAD^{commit}')
epoch=$(git log -1 --format=%ct "$commit")
# Exactly how the build archives: TZ=UTC, from the captured commit.
TZ=UTC git archive --format=tar "$commit" > "$work/source.tar"
TZ=UTC git archive --format=zip --prefix=openagentinbox-1.2.3/ -o "$work/source.zip" "$commit"
for zone in UTC Europe/London America/Los_Angeles Asia/Kolkata; do
  TZ=$zone VERSION=1.2.3 COMMIT=$commit EPOCH=$epoch python3 -I "$repo/scripts/release-manifest.py" --check-source "$work/source.zip" "$work/source.tar" >/dev/null \
    || fail "the source check failed under TZ=$zone"
done
# The build script must set TZ=UTC before its first git archive.
cd "$repo"
tz_line=$(grep -n '^[^#]*export TZ=UTC' scripts/build-release.sh | head -n 1 | cut -d: -f1)
archive_line=$(grep -n '^[^#]*git archive' scripts/build-release.sh | head -n 1 | cut -d: -f1)
[[ -n $tz_line && -n $archive_line && $tz_line -lt $archive_line ]] || fail 'build-release.sh does not export TZ=UTC before git archive'
echo 'release-source-time: ok'
