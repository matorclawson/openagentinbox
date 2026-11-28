"""Black-box OpenAgentInbox contracts. See docs/design/go-ownership.md."""
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import unittest
from urllib.error import HTTPError, URLError
from urllib.request import ProxyHandler, Request, build_opener
from uuid import uuid4


class ProductContracts(unittest.TestCase):
    def setUp(self):
        self.candidate = Path(os.environ["OAI_BIN_DIR"]).resolve()
        reference = Path(os.environ.get("OAI_REFERENCE_BIN_DIR", self.candidate)).resolve()
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.data = self.root / "store"
        self.ui = self.root / "ui"
        self.ui.mkdir()
        (self.ui / "index.html").write_text("<title>fixture</title>")
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            self.port = sock.getsockname()[1]
        self.origin = f"http://127.0.0.1:{self.port}"
        self.env = {"HOME": str(self.root), "PATH": os.defpath, "INBOX_DATA": str(self.data),
                    "INBOX_UI": str(self.ui), "INBOX_PORT": str(self.port), "INBOX_ORIGIN": self.origin}
        self.cookie = ""
        self.tokens = {}
        self.proc = None
        self.addCleanup(self.stop)
        self.start(reference)
        _, headers = self.http("setup", data={"username": "owner", "password": "temporary-password",
                                              "code": (self.data / "setup-code").read_text().strip()})
        self.cookie = headers["Set-Cookie"].split(";")[0]
        for seat in ("writer", "editor", "visitor"):
            result, _ = self.http("desk/agents", data={"name": seat})
            self.tokens[seat] = result["credential"]

    def stop(self):
        if self.proc is not None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait()
            self.proc = None

    def start(self, binaries):
        instance = str(uuid4())
        self.proc = subprocess.Popen([binaries / "openagentinboxd", "serve"], env=dict(self.env, INBOX_DEPLOY_INSTANCE=instance),
                                     stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for _ in range(200):
            if self.proc.poll() is not None:
                self.fail(f"fixture daemon exited with {self.proc.returncode}")
            try:
                health, _ = self.http("health")
                self.assertEqual((health["product"], health["instance"]), ("openagentinbox", instance))
                return
            except URLError:
                time.sleep(.05)
        self.fail("fixture daemon never became ready")

    def cutover(self):
        self.stop()
        self.start(self.candidate)

    def http(self, path, seat=None, data=None, expected=200):
        headers = {"Content-Type": "application/json", "Origin": self.origin}
        if seat:
            headers["Authorization"] = "Bearer " + self.tokens[seat]
        elif self.cookie:
            headers["Cookie"] = self.cookie
        url = self.origin + (path if path.startswith("/") else "/api/" + path)
        request = Request(url, None if data is None else json.dumps(data).encode(), headers)
        try:
            response = build_opener(ProxyHandler({})).open(request, timeout=6)
        except HTTPError as exc:
            response = exc
        with response:
            self.assertEqual(response.status, expected, f"{request.get_method()} {path}")
            return json.load(response), response.headers

    def letter(self, **overrides):
        return {"to": "editor", "subject": "Révision", "body": "Private draft λ", "key": str(uuid4()), **overrides}

    def tool(self, name, *args, ok=True, env=None, error=""):
        result = subprocess.run([self.candidate / name, *args], env=env or self.env, timeout=10,
                                capture_output=True, text=True)
        self.assertEqual(result.returncode == 0, ok, f"{name} {args[:1]} exit {result.returncode}")
        self.assertIn(error, result.stderr, f"{name} {args[:1]} failed for another reason")
        return result.stdout

    def test_send_retry_scope_and_thread_after_cutover(self):
        letter = self.letter(intent="urgent")
        mid = self.http("agent/send", "writer", letter)[0]["id"]
        self.cutover()
        def replay(_):
            return self.http("agent/send", "writer", letter)[0]["id"]
        with concurrent.futures.ThreadPoolExecutor(max_workers=6) as workers:
            self.assertEqual(list(workers.map(replay, range(12))), [mid] * 12)
        self.http("agent/send", "writer", dict(letter, body="changed"), 409)
        inbox = self.http("agent/inbox", "editor")[0]["rows"]
        self.assertEqual([row["id"] for row in inbox], [mid])
        self.assertEqual([r["id"] for r in self.http("agent/inbox?folder=sent", "writer")[0]["rows"]], [mid])
        receipt = "agent/receipt/" + mid
        before = self.http(receipt, "writer")[0]
        self.assertEqual((before["state"], before["opened"], before["replied"]), ("pending", 0, 0))
        self.assertGreater(before["due"], 0)
        self.http("agent/message/" + mid, "visitor", expected=404)
        self.http(receipt, "visitor", expected=404)
        self.assertEqual(self.http("agent/message/" + mid, "writer")[0]["opened"], 0)
        first = self.http("agent/message/" + mid, "editor")[0]
        self.assertEqual(first["body"], letter["body"])
        self.assertGreater(first["opened"], 0)
        self.assertEqual(self.http("agent/message/" + mid, "editor")[0]["opened"], first["opened"])
        reply = self.letter(to="visitor", replyTo=mid, body="done")
        self.http("agent/send", "editor", reply, 400)
        reply["to"] = "writer"
        rid = self.http("agent/send", "editor", reply)[0]["id"]
        self.assertEqual(self.http("agent/message/" + rid, "writer")[0]["thread"], mid)
        after = self.http(receipt, "writer")[0]
        self.assertEqual((after["opened"], after["state"], after["due"]), (first["opened"], "resolved", 0))
        self.assertGreaterEqual(after["replied"], first["opened"])

    def test_identity_rename_is_atomic_and_old_credentials_revoke(self):
        identity = self.http("agent/whoami", "editor")[0]["mailbox_id"]
        msg = self.letter()
        mid = self.http("agent/send", "writer", msg)[0]["id"]
        self.cutover()
        move = {"from": "editor", "to": "reviews"}
        def rename(moves, apply):
            return self.http("desk/addresses", data={"moves": moves, "apply": apply})[0]
        self.assertFalse(rename([move], False)["applied"])
        self.assertFalse(rename([move, {"from": "visitor", "to": "writer"}], True)["applied"])
        self.assertEqual(self.http("agent/whoami", "editor")[0]["name"], "editor")
        self.assertTrue(rename([move], True)["applied"])
        self.assertEqual(self.http("agent/send", "writer", dict(msg, to="reviews"))[0]["id"], mid)
        fresh = self.http("desk/rotate", data={"name": "reviews"})[0]["credential"]
        self.http("agent/whoami", "editor", expected=401)
        self.tokens["reviews"] = fresh
        who = self.http("agent/whoami", "reviews")[0]
        self.assertEqual((who["mailbox_id"], who["name"], who["original_address"]), (identity, "reviews", "editor"))
        self.assertEqual(self.http("agent/message/" + mid, "reviews")[0]["body"], msg["body"])

    def test_cli_profile_and_mcp_use_the_same_authority(self):
        mid = self.http("agent/send", "writer", self.letter())[0]["id"]
        self.cutover()
        profiles = self.root / ".config/openagentinbox/seats"
        profiles.mkdir(parents=True)
        for seat in ("visitor", "editor"):
            profile = profiles / (seat + ".json")
            profile.touch(mode=0o600)
            profile.write_text(json.dumps({"name": seat, "token": self.tokens[seat], "origin": self.origin}))
        ambient = dict(self.env, OPENAGENTINBOX_PROFILE=str(profiles / "editor.json"))
        refused = self.tool("openagentinbox-client", "--seat", "visitor", "read", mid, env=ambient, ok=False, error="HTTP 404:")
        self.assertNotIn("Private draft", refused)
        read = self.tool("openagentinbox-client", "--seat", "editor", "read", mid)
        self.assertEqual(json.loads(read)["id"], mid)
        rpc = {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "read", "arguments": {"id": mid}}}
        denied = self.http("/mcp", "visitor", rpc)[0]["result"]
        self.assertTrue(denied["isError"])
        self.assertNotIn("Private draft", json.dumps(denied))
        allowed = self.http("/mcp", "editor", rpc)[0]["result"]
        self.assertFalse(allowed["isError"])
        self.assertEqual(json.loads(allowed["content"][0]["text"])["id"], mid)

    def test_acknowledged_reply_survives_abrupt_exit(self):
        msg = self.letter(intent="request")
        mid = self.http("agent/send", "writer", msg)[0]["id"]
        reply = self.letter(to="writer", replyTo=mid)
        rid = self.http("agent/send", "editor", reply)[0]["id"]
        self.proc.kill()
        self.proc.wait(timeout=5)
        self.proc = None
        self.start(self.candidate)
        self.assertEqual(self.http("agent/send", "editor", reply)[0]["id"], rid)
        receipt = self.http("agent/receipt/" + mid, "writer")[0]
        self.assertEqual((receipt["state"], receipt["due"]), ("resolved", 0))
        self.assertGreater(receipt["replied"], 0)
        self.assertEqual([r["id"] for r in self.http("agent/inbox", "writer")[0]["rows"]], [rid])

    def test_snapshot_reopens_with_receipts_and_excludes_later_mail(self):
        letter = self.letter()
        mid = self.http("agent/send", "writer", letter)[0]["id"]
        opened = self.http("agent/message/" + mid, "editor")[0]["opened"]
        snapshot = self.root / "snapshot.db"
        db = self.data / "console.sqlite"
        args = ("-db", db, "-out", snapshot)
        manifest = json.loads(self.tool("openagentinbox-backup", *args))
        self.assertEqual(manifest["sha256"], hashlib.sha256(snapshot.read_bytes()).hexdigest())
        self.assertEqual((manifest["messages"], manifest["deliveries"]), (1, 1))
        self.assertEqual(snapshot.stat().st_mode & 0o077, 0)
        self.tool("openagentinbox-backup", *args, ok=False)
        self.assertEqual(json.loads(self.tool("openagentinbox-backup", "-verify", "-db", snapshot)), manifest)
        later = self.http("agent/send", "writer", self.letter())[0]["id"]
        self.stop()
        self.data = self.root / "restored"
        self.data.mkdir(mode=0o700)
        snapshot.rename(self.data / "console.sqlite")
        self.env["INBOX_DATA"] = str(self.data)
        self.start(self.candidate)
        self.assertEqual(self.http("agent/send", "writer", letter)[0]["id"], mid)
        self.assertEqual(self.http("agent/receipt/" + mid, "writer")[0]["opened"], opened)
        self.http("agent/message/" + later, "editor", expected=404)

    def test_cursor_survives_cutover_and_concurrent_arrivals(self):
        ids = []
        for i in range(43):
            msg = self.letter(subject=f"Revision {i}")
            ids.append(self.http("agent/send", "writer", msg)[0]["id"])
        first = self.http("agent/inbox", "editor")[0]
        self.assertEqual([row["id"] for row in first["rows"]], ids[::-1][:40])
        self.assertTrue(first["next"])
        self.cutover()
        new = self.http("agent/send", "writer", self.letter())[0]["id"]
        older = self.http("agent/inbox?before=" + first["next"], "editor")[0]
        self.assertEqual([row["id"] for row in older["rows"]], ids[::-1][40:])
        self.assertIsNone(older["next"])
        self.assertEqual(self.http("agent/inbox", "editor")[0]["rows"][0]["id"], new)
        private_search = "agent/threads?folder=inbox&preview=1&q=Revision"
        self.assertTrue(self.http(private_search, "editor")[0]["rows"])
        self.assertEqual(self.http(private_search, "visitor")[0]["rows"], [])
        self.assertEqual(self.http("agent/receipt/" + ids[-1], "editor")[0]["opened"], 0)

    def test_notice_is_private_body_free_and_does_not_open_mail(self):
        msg = self.letter(intent="request")
        mid = self.http("agent/send", "writer", msg)[0]["id"]
        self.cutover()
        notice = self.http("agent/notice?after=0", "editor")[0]
        self.assertEqual(set(notice), {"cursor", "count"})
        self.assertEqual(notice["count"], 1)
        self.assertGreater(notice["cursor"], 0)
        self.assertEqual(self.http(f"agent/notice?after={notice['cursor']}", "editor")[0]["count"], 0)
        self.assertEqual(self.http("agent/notice?after=0", "visitor")[0], {"cursor": 0, "count": 0})
        self.http("agent/notice?after=-1", "editor", expected=400)
        self.assertEqual(self.http("agent/receipt/" + mid, "editor")[0]["opened"], 0)

    def test_external_send_requires_separate_grant(self):
        self.cutover()
        msg = self.letter(to="human@outside.example")
        self.http("agent/send", "writer", msg, 403)
        self.assertEqual(self.http("agent/inbox?folder=sent", "writer")[0]["rows"], [])
        local = dict(msg, to="editor")
        mid = self.http("agent/send", "writer", local)[0]["id"]
        self.assertEqual(self.http("agent/message/" + mid, "editor")[0]["body"], msg["body"])

    def test_local_deploy_plan_has_no_side_effects(self):
        destination = self.root / "proposed-install"
        env = dict(self.env, INBOX_INSTALL_DIR=str(destination))
        command = ("openagentinboxd", "deploy", "plan", "--target", "local")
        plan = json.loads(self.tool(*command, env=env))
        self.assertEqual((plan["schema"], plan["status"]), ("openagentinbox.deploy/v1", "planned"))
        self.tool(*command, "--port", "65536", env=env, ok=False)
        self.assertFalse(destination.exists())

    def test_offline_admin_requires_exact_fence_reason(self):
        self.stop()
        args = ("-db", self.data / "console.sqlite")
        def admin(*action, ok=True):
            return self.tool("openagentinbox-store-admin", *args, *action, ok=ok)
        admin("-fence", "migration-A")
        admin("-clear-fence", "migration-B", ok=False)
        admin("-clear-fence", "migration-A")
        admin("-clear-fence", "migration-A", ok=False)


if __name__ == "__main__":
    unittest.main(verbosity=2)
