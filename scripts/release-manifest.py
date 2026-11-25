#!/usr/bin/env python3
"""Write MANIFEST.json and SHA256SUMS for a release directory.

Usage: release-manifest.py OUTDIR SRCDIR   (inputs from the environment: COMMIT TREE VERSION EPOCH
GO_VERSION NODE_VERSION NPM_VERSION PY_VERSION UNAME)
The manifest holds no wall-clock time or host name, so equal inputs give an equal manifest."""
import hashlib, json, os, sys

out, src = sys.argv[1], sys.argv[2]
sha = lambda p: hashlib.sha256(open(p, "rb").read()).hexdigest()
env = os.environ
files = {n: sha(os.path.join(out, n)) for n in sorted(os.listdir(out)) if n not in ("MANIFEST.json", "SHA256SUMS")}
manifest = {
    "product": "openagentinbox", "version": env["VERSION"], "commit": env["COMMIT"], "tree": env["TREE"],
    "source_date_epoch": int(env["EPOCH"]),
    "toolchain": {"go": env["GO_VERSION"], "node": env["NODE_VERSION"], "npm": env["NPM_VERSION"],
                  "python": env["PY_VERSION"], "build_host_kind": env["UNAME"]},
    "inputs": {"web/package-lock.json": sha(os.path.join(src, "web/package-lock.json")), "go.sum": sha(os.path.join(src, "go.sum")),
               "go.mod": sha(os.path.join(src, "go.mod"))},
    "files": files,
}
with open(os.path.join(out, "MANIFEST.json"), "w") as f:
    json.dump(manifest, f, indent=2, sort_keys=True)
    f.write("\n")
names = sorted(n for n in os.listdir(out) if n != "SHA256SUMS")
with open(os.path.join(out, "SHA256SUMS"), "w") as f:
    for n in names:
        f.write("%s  %s\n" % (sha(os.path.join(out, n)), n))
