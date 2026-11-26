"""Check that a released older client still works against this checkout's daemon.

Linux only, no provider key, disposable accounts and data. INBOX_DAEMON is the candidate
daemon; INBOX_OLD_CLIENT is the older client, for example built from its tag:

    old=$(mktemp -d) && git archive v0.9.1 | tar -x -C "$old"
    (cd "$old" && go build -o old-client ./cmd/openagentinbox-client)
    go build -o build/openagentinboxd ./cmd/openagentinboxd
    INBOX_DAEMON=$PWD/build/openagentinboxd INBOX_OLD_CLIENT=$old/old-client python3 tests/old-client.py

Not covered: onboard, login, pairing, tickets and notify, which need a browser approval or a service.
"""
import json
import os
import select
import socket
import subprocess
import tempfile
import time
from pathlib import Path
from urllib.request import Request, urlopen
from uuid import uuid4


def exercise(old, origin, root, code):
    def post(path, data, cookie=""):
        request = Request(origin + "/api/" + path, json.dumps(data).encode(),
                          {"Content-Type": "application/json", "Origin": origin, "Cookie": cookie})
        return urlopen(request, timeout=5)

    with post("setup", {"username": "owner", "password": "temporary-test-password", "code": code}) as response:
        cookie = response.headers["Set-Cookie"].split(";")[0]
    env = {"HOME": str(root), "PATH": os.environ["PATH"]}

    def client(seat, *args, stdin="", ok=True):
        done = subprocess.run([old, "--seat", seat, *args], env=env, input=stdin,
                              capture_output=True, text=True, timeout=30)
        assert (done.returncode == 0) == ok, f"{seat} {args[0]}: exit {done.returncode}: {done.stderr}"
        return done.stdout

    for name in ("builder", "reviewer", "unrelated"):
        with post("desk/agents", {"name": name}, cookie) as response:
            token = json.load(response)["credential"]
        client(name, "bind", origin, "--token-stdin", stdin=token)
        assert json.loads(client(name, "whoami"))["name"] == name
        json.loads(client(name, "authorization"))
        client(name, "verify", origin, name)
    print("PASS bind, whoami, authorization and verify")

    key = str(uuid4())
    sent = json.loads(client("builder", "send", "reviewer", "Compat", "fixture", key))["id"]
    assert json.loads(client("builder", "send", "reviewer", "Compat", "fixture", key))["id"] == sent
    listing = json.loads(client("reviewer", "inbox"))
    assert [row["id"] for row in listing["rows"]] == [sent]
    if listing.get("next"):
        json.loads(client("reviewer", "inbox", str(listing["next"])))
    assert json.loads(client("reviewer", "read", sent))["body"] == "fixture"
    client("unrelated", "read", sent, ok=False)
    print("PASS send, replay with the same retry key, inbox, read and denial to an unrelated seat")

    watcher = subprocess.Popen([old, "--seat", "reviewer", "watch"], env=env, stdout=subprocess.PIPE)
    try:
        assert select.select([watcher.stdout], [], [], 20)[0], "watch reported nothing within 20 s"
        notice = json.loads(os.read(watcher.stdout.fileno(), 65536))
        assert int(notice.get("cursor", notice.get("Cursor", 0))) > 0, notice
    finally:
        watcher.kill()
        watcher.wait(timeout=5)
    print("PASS watch reports the delivery")


if __name__ == "__main__":
    old = os.environ["INBOX_OLD_CLIENT"]
    print("old client", subprocess.check_output([old, "version"], text=True).strip())
    with tempfile.TemporaryDirectory() as temp:
        root = Path(temp)
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        origin = f"http://127.0.0.1:{port}"
        (root / "index.html").write_text("fixture")
        daemon = subprocess.Popen([os.environ["INBOX_DAEMON"], "serve"], env={
            **os.environ, "INBOX_DATA": temp, "INBOX_UI": temp, "INBOX_PORT": str(port), "INBOX_ORIGIN": origin})
        try:
            for _ in range(100):
                try:
                    with urlopen(origin + "/api/health", timeout=1):
                        break
                except OSError:
                    time.sleep(0.05)
            exercise(old, origin, root, (root / "setup-code").read_text())
        finally:
            daemon.terminate()
            daemon.wait(timeout=5)
