#!/usr/bin/env bash
# release-outdir.sh OUT: claim a new release output directory and print its absolute path.
# OUT must be a name that does not exist, as a direct child of the repository's build/
# directory, which must already exist and not be a symbolic link. Every check runs before any
# write, and the claim is a single mkdir without -p, which fails when the name exists, so two
# concurrent builds cannot share a directory. Nothing is ever removed here or by the caller.
set -euo pipefail
die(){ echo "release output directory refused: $*" >&2; exit 2; }
root=$(cd "$(dirname "$0")/.." && pwd -P)
allowed=$root/build
out=${1:-}
[[ -n $out ]] || die 'no directory given'
case $out in /*) ;; *) out=$PWD/$out;; esac
case /$out/ in *"/../"*|*"/./"*|*"//"*) die "$out contains a relative or empty component";; esac
out=${out%/}
[[ $(dirname "$out") == "$allowed" ]] || die "$out is not a direct child of $allowed"
name=$(basename "$out")
[[ -n $name && $name != . && $name != .. ]] || die 'invalid name'
[[ -d $allowed && ! -L $allowed ]] || die "$allowed does not exist or is a symbolic link; create it with mkdir build"
[[ ! -e $out && ! -L $out ]] || die "$out already exists; choose a new name or remove it yourself"
mkdir "$out" 2>/dev/null || die "$out was claimed by another build"
physical=$(cd "$out" && pwd -P)
[[ $physical == "$(cd "$allowed" && pwd -P)/$name" ]] || die "$out resolved outside $allowed"
echo "$physical"
