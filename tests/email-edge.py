"""Linux loopback integration and paced mixed-mail test; no public network."""
import argparse
import base64
import concurrent.futures
import http.cookiejar
import json
import os
from pathlib import Path
import smtplib
import socket
import socketserver
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid

parser = argparse.ArgumentParser()
parser.add_argument("daemon", type=Path)
parser.add_argument("ui", type=Path)
parser.add_argument("--tcp-fixture", action="store_true",help="use a Go test binary with loopback listeners instead of the release daemon")
parser.add_argument("--rounds", type=int, default=100)
parser.add_argument("--per-minute", type=float, default=100)
args = parser.parse_args()
assert args.rounds > 0 and args.per_minute > 0
captured = []
envelopes = {}


class Relay(socketserver.StreamRequestHandler):
    def handle(self):
        self.wfile.write(b"220 fixture ESMTP\r\n")
        recipient = b""
        while line := self.rfile.readline():
            verb = line.split(b" ", 1)[0].strip().upper()
            response = b"250 ok"
            if verb == b"MAIL":
                envelope=line.split(b"<",1)[1].split(b">",1)[0].decode()
            if verb == b"RCPT":
                recipient = line
                if b"reject@" in line:
                    response = b"550 rejected"
                if b"later@" in line:
                    response = b"450 later"
            if verb == b"DATA":
                self.wfile.write(b"354 go\r\n")
                parts = []
                while line := self.rfile.readline():
                    if line == b".\r\n":
                        break
                    parts.append(line)
                captured.append(b"".join(parts))
                envelopes[captured[-1]]=envelope
                if b"uncertain@" in recipient:
                    return
            self.wfile.write(response + b"\r\n")


def run(directory, relay):
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    origin = f"http://127.0.0.1:{port}"
    path = str(directory / "lmtp")
    env = dict(os.environ, INBOX_PORT=str(port), INBOX_DATA=str(directory),
               INBOX_UI=str(args.ui.resolve()), GOMAXPROCS="2", INBOX_EMAIL_DOMAIN="agents.example",
               INBOX_EMAIL_SOCKET=path, INBOX_EMAIL_RELAY="127.0.0.1:"+str(relay.server_address[1]),
               INBOX_EMAIL_SENDERS="seat-0 seat-1")
    if args.tcp_fixture:
        for key in ["OAI_TEST_LMTP","OAI_TEST_MAP"]:
            with socket.socket() as listener:
                listener.bind(("127.0.0.1",0))
                env[key]="127.0.0.1:"+str(listener.getsockname()[1])
    owner = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def api(route, data=None, token=None):
        headers = {"Origin": origin, "Content-Type": "application/json"}
        if token:
            headers["Authorization"] = "Bearer " + token
        req = urllib.request.Request(origin+"/api/"+route, headers=headers,
                                     data=None if data is None else json.dumps(data).encode())
        with (urllib.request.urlopen(req, timeout=5) if token else owner.open(req, timeout=5)) as response:
            return json.load(response)

    def start():
        p = subprocess.Popen([args.daemon.resolve(), "-test.run=^TestFixtureDaemon$" if args.tcp_fixture else "serve"], env=env,stdout=sys.stderr)
        for _ in range(200):
            if p.poll() is not None:
                raise RuntimeError("daemon failed to start")
            try:
                api("health")
                return p
            except OSError:
                time.sleep(.025)
        p.kill()
        p.wait()
        raise TimeoutError("health")

    def lm(body, target="seat-0@agents.example", sender="sender@outside.example"):
        host, port = env["OAI_TEST_LMTP"].split(":") if args.tcp_fixture else (path,0)
        with smtplib.LMTP(host, int(port), timeout=5) as client:
            client.sendmail(sender, [target], body)

    def send(i, to, token, key=None):
        return api("agent/send", dict(to=to, subject=f"Round {i}", body="hello",
                                      key=key or str(uuid.uuid4())), token)["id"]

    def state(mid, token, want):
        for _ in range(200):
            receipt = api("agent/receipt/"+mid, token=token)["email"]
            if receipt["state"] == want:
                return receipt
            time.sleep(.025)
        raise AssertionError((receipt, want))

    def refused(fn, code):
        try:
            fn()
        except urllib.error.HTTPError as e:
            assert e.code == code, e.code
            return
        raise AssertionError("expected HTTP refusal")

    p = start()
    try:
        api("setup", dict(username="owner", password="disposable-password",
                          code=(directory/"setup-code").read_text()))
        keys = [api("desk/agents", {"name": f"seat-{i}"})["credential"] for i in range(76)]
        assert api("agent/whoami", token=keys[0])["email_address"] == "seat-0@agents.example"
        def lookup(address):
            request = ("inboxes "+address).encode()
            with socket.socket(socket.AF_INET if args.tcp_fixture else socket.AF_UNIX) as c:
                c.connect(("127.0.0.1",int(env["OAI_TEST_MAP"].split(":")[1])) if args.tcp_fixture else path+".map")
                c.sendall(str(len(request)).encode()+b":"+request+b",")
                return c.recv(512)
        assert lookup("seat-0@agents.example") == b"4:OK 1,"
        assert b"NOTFOUND" in lookup("absent@agents.example")
        raw = (b"Received: by edge id fixture-1\r\nFrom: operator <sender@outside.example>\r\n"
               b"Message-ID: <fixture@outside.example>\r\nSubject: =?utf-8?q?An_email?=\r\nMIME-Version: 1.0\r\n"
               b"Content-Type: multipart/mixed; boundary=test\r\n\r\n--test\r\n"
               b"Content-Type: text/plain\r\nContent-Transfer-Encoding: base64\r\n\r\naGVsbG8=\r\n"
               b"--test\r\nContent-Type: application/octet-stream\r\n\r\nopaque\r\n--test--\r\n")
        lm(raw)
        row = api("agent/inbox", token=keys[0])["rows"][0]
        mid = row["id"]
        def read():
            return api("agent/message/"+mid, token=keys[0])
        assert read()["trust"] == "external" and read()["body"] == "hello"
        assert base64.b64decode(api("agent/email-original/"+mid, token=keys[0])["base64"]) == raw
        refused(lambda: api("agent/email-original/"+mid, token=keys[1]), 404)
        for addr in ["absent@agents.example", "seat-0@outside.example"]:
            try:
                lm(raw, addr)
            except smtplib.SMTPRecipientsRefused as e:
                assert e.recipients[addr][0] == 550
            else:
                raise AssertionError("Unexpected acceptance")
        refused(lambda: send(0, "x@outside.example", keys[2]), 403)
        outbound_key = str(uuid.uuid4())
        out = send(0, "ok@outside.example", keys[0], outbound_key)
        assert out == send(0, "ok@outside.example", keys[0], outbound_key)
        payload=dict(to="sender@outside.example",subject="Re: attachment",body="hello",replyTo=mid,
                     key=str(uuid.uuid4()),fileName="report.txt",fileData=base64.b64encode(b"artifact").decode())
        attached=api("agent/send",payload,keys[0])["id"]
        state(attached,keys[0],"relayed")
        import email
        from email import policy
        msg=email.message_from_bytes(next(w for w in captured if attached.encode() in w), policy=policy.default)
        assert msg["In-Reply-To"]=="<fixture@outside.example>"
        assert next(msg.iter_attachments()).get_payload(decode=True)==b"artifact"
        refused(lambda:api("agent/send",dict(payload,fileData="bmV3"),keys[0]),409)
        state(out, keys[0], "relayed")
        assert sum(out.encode() in wire for wire in captured) == 1
        state(send(1, "reject@outside.example", keys[0]), keys[0], "failed")
        uncertain = send(2, "uncertain@outside.example", keys[0])
        state(uncertain, keys[0], "uncertain")
        later = send(3, "later@outside.example", keys[0])
        for _ in range(100):
            receipt = api("agent/receipt/"+later, token=keys[0])["email"]
            if receipt["smtp_code"] == 450:
                break
            time.sleep(.025)
        assert receipt["state"] == "queued" and receipt["smtp_code"] == 450
        p.kill()
        p.wait()
        p = start()
        lm(raw)
        assert [r["id"] for r in api("agent/inbox", token=keys[0])["rows"]] == [mid]
        assert read()["body"] == "hello"
        assert api("desk/email/uncertain")["rows"][0]["id"] == uncertain
        bounceaddr=next(address for wire,address in envelopes.items() if out.encode() in wire)
        assert bounceaddr.startswith("bounce+")
        dsn=(b"From: Mailer <postmaster@outside.example>\r\nSubject: Returned mail\r\n"
             b"Content-Type: multipart/report; report-type=delivery-status; boundary=dsn\r\n\r\n"
             b"--dsn\r\nContent-Type: message/delivery-status\r\n\r\nReporting-MTA: dns; mx.outside.example\r\n\r\n"
             b"Final-Recipient: rfc822; ok@outside.example\r\nAction: failed\r\nStatus: 5.1.1\r\n\r\n--dsn--\r\n")
        lm(dsn,bounceaddr,"")
        assert state(out,keys[0],"relayed")["remote_status"]=="5.1.1"
        decision = dict(id=uncertain, attempt=1, decision="confirmed", evidence="fixture/accepted")
        api("desk/email/reconcile", decision)
        refused(lambda: api("desk/email/reconcile", decision), 409)
        assert sum(uncertain.encode() in wire for wire in captured) == 1
        timings = {"inbound": [], "internal": [], "outbound": []}
        outbound = []
        def roundtrip(i):
            typ = i % 3
            begin = time.monotonic()
            if typ == 0:
                lm(raw.replace(b"fixture-1", f"load-{i}".encode()), f"seat-{i%76}@agents.example")
            elif typ == 1:
                send(i, f"seat-{(i+1)%76}", keys[i%76])
            else:
                m = send(i, "ok@outside.example", keys[i%2])
                outbound.append((m,keys[i%2]))
            return ["inbound","internal","outbound"][typ], (time.monotonic()-begin)*1000
        start_time = time.monotonic()
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
            futures = []
            for i in range(args.rounds):
                time.sleep(max(0,start_time+i*60/args.per_minute-time.monotonic()))
                futures.append(pool.submit(roundtrip,i))
            for f in futures:
                kind, elapsed = f.result()
                timings[kind].append(elapsed)
        for m, token in outbound:
            state(m, token, "relayed")
        peak = next(line for line in Path(f"/proc/{p.pid}/status").read_text().splitlines() if line.startswith("VmHWM"))
        import sqlite3
        with sqlite3.connect(directory/"console.sqlite") as db:
            n = db.execute("SELECT count(*) FROM messages").fetchone()[0]
        assert n == args.rounds + 7, (n,args.rounds)
        print(json.dumps({"seats":76,"scheduled":args.rounds,"persisted_load_messages":n-7,
            "rate_per_minute":args.per_minute,"peak_daemon_rss_kib_since_restart":int(peak.split()[1]),
            "elapsed_s":round(time.monotonic()-start_time,2),"stages":{
                k:{"count":len(v),"p95_ms":round(sorted(v)[(len(v)*95+99)//100-1],2)} for k,v in timings.items() if v}},
            indent=2))
    finally:
        p.terminate()
        p.wait(timeout=30)


with tempfile.TemporaryDirectory() as d, socketserver.ThreadingTCPServer(("127.0.0.1",0), Relay) as relay:
    threading.Thread(target=relay.serve_forever, daemon=True).start()
    try:
        run(Path(d), relay)
    finally:
        relay.shutdown()
