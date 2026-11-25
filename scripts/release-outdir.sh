#!/usr/bin/env bash
# release-outdir.sh OUT: validate and create a release output directory, then print its
# absolute path. OUT must be a path that does not exist yet, or an existing empty directory,
# strictly inside the repository's build/ directory (no "..", no symlink). Nothing is ever
# removed here or by the caller: a release build never deletes a path it was given.
# INBOX_RELEASE_BUILD_ROOT replaces build/ as the allowed root, for tests only.
set -euo pipefail
die(){ echo "release output directory refused: $*" >&2; exit 2; }
root=$(cd "$(dirname "$0")/.." && pwd -P)
allowed=${INBOX_RELEASE_BUILD_ROOT:-$root/build}
out=${1:-}
[[ -n $out ]] || die 'no directory given'
case $out in /*) ;; *) out=$PWD/$out;; esac
case /$out/ in *"/../"*|*"/./"*) die "$out contains a relative component";; esac
out=${out%/}
case $out in "$allowed"/?*) ;; *) die "$out is not inside $allowed";; esac
[[ ! -L $out ]] || die "$out is a symbolic link"
if [[ -e $out ]]; then
  [[ -d $out ]] || die "$out exists and is not a directory"
  [[ -z $(ls -A "$out") ]] || die "$out is not empty; choose a new directory or empty it yourself"
fi
mkdir -p "$(dirname "$out")"
physical=$(cd "$(dirname "$out")" && pwd -P)/$(basename "$out")
allowed_physical=$(cd "$allowed" && pwd -P)
case $physical in "$allowed_physical"/?*) ;; *) die "$out resolves outside $allowed";; esac
mkdir -p "$physical"
echo "$physical"
