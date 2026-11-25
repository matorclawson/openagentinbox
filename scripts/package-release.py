#!/usr/bin/env python3
"""Package a staged release tree into the installer script and the Python wheel.

Usage: package-release.py STAGE OUTDIR VERSION SOURCE_DATE_EPOCH

STAGE holds install-openagentinbox.sh, scripts/build-console-release.sh, web/deploy/ and dist/.
The installer is a shell script that carries the tree as a base64 gzip tar payload. Every
archive here is written with sorted names, fixed times, root ownership and normalized modes,
so the same inputs produce the same bytes. Standard library only.
"""
import base64, gzip, hashlib, io, os, re, sys, tarfile, zipfile

HEADER = """#!/usr/bin/env bash
set -euo pipefail
umask 077
export INBOX_INSTALL_ARCHIVE="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
staging=$(mktemp -d)
trap 'rm -rf "$staging"' EXIT
base64 -d <<'INBOX_PAYLOAD' | tar -xz -C "$staging"
"""
FOOTER = """INBOX_PAYLOAD
bash "$staging/install-openagentinbox.sh" "$@"
"""


def tree(stage):
    out = []
    for root, dirs, files in os.walk(stage):
        dirs.sort()
        for name in sorted(files) + dirs:
            out.append(os.path.relpath(os.path.join(root, name), stage))
    return sorted(set(out))


def payload(stage, epoch):
    raw = io.BytesIO()
    with tarfile.open(fileobj=raw, mode="w", format=tarfile.GNU_FORMAT) as tar:
        for rel in tree(stage):
            path = os.path.join(stage, rel)
            info = tarfile.TarInfo(rel)
            info.mtime = epoch
            info.uid = info.gid = 0
            info.uname = info.gname = ""
            if os.path.isdir(path):
                info.type, info.mode = tarfile.DIRTYPE, 0o755
                tar.addfile(info)
            elif os.path.isfile(path):
                info.size = os.path.getsize(path)
                info.mode = 0o755 if os.access(path, os.X_OK) else 0o644
                with open(path, "rb") as f:
                    tar.addfile(info, f)
            else:
                sys.exit("unsupported entry in the release tree: " + rel)
    gz = io.BytesIO()
    with gzip.GzipFile(fileobj=gz, mode="wb", mtime=0, compresslevel=9) as z:
        z.write(raw.getvalue())
    return gz.getvalue()


def installer(stage, out, version, epoch):
    body = base64.encodebytes(payload(stage, epoch)).decode()
    path = os.path.join(out, "openagentinbox-install-%s.sh" % version)
    with open(path, "w", newline="\n") as f:
        f.write(HEADER + body + FOOTER)
    os.chmod(path, 0o755)
    return path


def wheel(repo, out, epoch):
    src = os.path.join(repo, "integrations", "python")
    meta = open(os.path.join(src, "pyproject.toml")).read()
    field = lambda k: re.search(r'^%s\s*=\s*"([^"]*)"' % k, meta, re.M).group(1)
    name, ver, summary, req = field("name"), field("version"), field("description"), field("requires-python")
    dist = "%s-%s.dist-info" % (name, ver)
    files = {}
    pkg = os.path.join(src, name)
    for root, dirs, names in os.walk(pkg):
        dirs[:] = sorted(d for d in dirs if d != "__pycache__")
        for n in sorted(names):
            if n.endswith((".pyc", ".pyo")):
                continue
            p = os.path.join(root, n)
            files[os.path.relpath(p, src).replace(os.sep, "/")] = open(p, "rb").read()
    files[dist + "/METADATA"] = ("Metadata-Version: 2.1\nName: %s\nVersion: %s\nSummary: %s\nLicense: MIT\nRequires-Python: %s\n\n"
                                  % (name, ver, summary, req) + open(os.path.join(src, "README.md")).read()).encode()
    files[dist + "/WHEEL"] = b"Wheel-Version: 1.0\nGenerator: openagentinbox-release\nRoot-Is-Purelib: true\nTag: py3-none-any\n"
    files[dist + "/LICENSE"] = open(os.path.join(src, "LICENSE"), "rb").read()
    files[dist + "/top_level.txt"] = (name + "\n").encode()
    record = "".join("%s,sha256=%s,%d\n" % (n, base64.urlsafe_b64encode(hashlib.sha256(d).digest()).rstrip(b"=").decode(), len(d))
                     for n, d in sorted(files.items())) + dist + "/RECORD,,\n"
    files[dist + "/RECORD"] = record.encode()
    path = os.path.join(out, "%s-%s-py3-none-any.whl" % (name, ver))
    t = __import__("time").gmtime(max(epoch, 315532800))[:6]
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
        for n, d in sorted(files.items()):
            zi = zipfile.ZipInfo(n, date_time=t)
            zi.compress_type = zipfile.ZIP_DEFLATED
            zi.external_attr = 0o644 << 16
            z.writestr(zi, d)
    return path


if __name__ == "__main__":
    if len(sys.argv) != 5:
        sys.exit(__doc__)
    stage, out, version, epoch = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
    repo = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    print(installer(stage, out, version, epoch))
    print(wheel(repo, out, epoch))
