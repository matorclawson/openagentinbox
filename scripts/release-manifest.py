#!/usr/bin/env python3
"""Write MANIFEST.json and SHA256SUMS for a release directory.

Usage: release-manifest.py OUTDIR SRCDIR SOURCE.TAR STAGE   (inputs from the environment: COMMIT TREE VERSION EPOCH
GO_VERSION NODE_VERSION NPM_VERSION PY_VERSION UNAME)
       release-manifest.py --check-source SOURCE.ZIP SOURCE.TAR   (environment: COMMIT VERSION EPOCH)
The zip must have been made with TZ=UTC: git archive writes zip member times in the host's local time.
The manifest holds no wall-clock time or host name, so equal inputs give an equal manifest."""
import hashlib, json, os, sys

import tarfile, time, zipfile
sha = lambda p: hashlib.sha256(open(p, "rb").read()).hexdigest()
env = os.environ


def verify_source(zip_path, tar_path):
    """The source zip must be the committed tree: the zip comment is the commit id, every member has
    the commit time, and the members match the tar the build was exported from, byte for byte."""
    prefix = "openagentinbox-%s/" % env["VERSION"]
    expect_time = time.gmtime(int(env["EPOCH"]))[:5] + (time.gmtime(int(env["EPOCH"]))[5] & ~1,)
    with tarfile.open(tar_path) as t:
        tar_files = {m.name: hashlib.sha256(t.extractfile(m).read()).hexdigest() for m in t.getmembers() if m.isfile()}
    with zipfile.ZipFile(zip_path) as z:
        if z.comment.decode() != env["COMMIT"]:
            sys.exit("source zip records commit %r, expected %s" % (z.comment.decode(), env["COMMIT"]))
        zip_files = {}
        for i in z.infolist():
            if i.date_time != expect_time:
                sys.exit("source zip member %s has time %s, expected the commit time %s" % (i.filename, i.date_time, expect_time))
            if not i.is_dir():
                zip_files[i.filename[len(prefix):]] = hashlib.sha256(z.read(i)).hexdigest()
    if zip_files != tar_files:
        sys.exit("source zip and the exported tree differ: %s" % sorted(set(zip_files.items()) ^ set(tar_files.items()))[:3])
    return hashlib.sha256(open(tar_path, "rb").read()).hexdigest()


def main():
    out, src, source_tar, stage = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
    write(out, src, source_tar, stage)


def write(out, src, source_tar, stage):
    payload = {}
    for root, dirs, names in os.walk(stage):
        dirs.sort()
        for n in sorted(names):
            p = os.path.join(root, n)
            payload[os.path.relpath(p, stage)] = sha(p)
    source_tar_sha256 = verify_source(os.path.join(out, "openagentinbox-%s-source.zip" % env["VERSION"]), source_tar)
    files = {n: sha(os.path.join(out, n)) for n in sorted(os.listdir(out)) if n not in ("MANIFEST.json", "SHA256SUMS")}
    manifest = {
        "product": "openagentinbox", "version": env["VERSION"], "commit": env["COMMIT"], "tree": env["TREE"],
        "source_date_epoch": int(env["EPOCH"]), "source_tar_sha256": source_tar_sha256,
        "toolchain": {"go": env["GO_VERSION"], "node": env["NODE_VERSION"], "npm": env["NPM_VERSION"],
                      "python": env["PY_VERSION"], "build_host_kind": env["UNAME"]},
        "inputs": {"web/package-lock.json": sha(os.path.join(src, "web/package-lock.json")), "go.sum": sha(os.path.join(src, "go.sum")),
                   "go.mod": sha(os.path.join(src, "go.mod"))},
        "files": files,
        "installer_payload": payload,
    }
    with open(os.path.join(out, "MANIFEST.json"), "w") as f:
        json.dump(manifest, f, indent=2, sort_keys=True)
        f.write("\n")
    names = sorted(n for n in os.listdir(out) if n != "SHA256SUMS")
    with open(os.path.join(out, "SHA256SUMS"), "w") as f:
        for n in names:
            f.write("%s  %s\n" % (sha(os.path.join(out, n)), n))


if __name__ == "__main__":
    if len(sys.argv) == 4 and sys.argv[1] == "--check-source":
        verify_source(sys.argv[2], sys.argv[3])
        print("source zip matches the commit")
    elif len(sys.argv) == 5:
        main()
    else:
        sys.exit(__doc__)
