"""Exercise real export text locally. Usage: python3 tests/corpus-layout.py DAEMON EXPORT WEB_OUT

Requires Playwright Chromium. CHROMIUM may point to a local executable.
Only a temporary loopback server receives message bodies. Never print screens or text.
"""
import json
import os
import pathlib
import socket
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.request
import uuid

from playwright.sync_api import sync_playwright

daemon, corpus, ui = map(os.path.abspath, sys.argv[1:])
with open(corpus) as source:
    records = [json.loads(line) for line in source]
missing = [r for r in records if r["body"].startswith("Priority:")]
titled = [r for r in records if r["body"].startswith("Subject:")]
urgent = next(r for r in records if "Priority: p0\n" in r["body"][:512])
sample = missing[:20] + titled[:20] + [urgent]
with tempfile.TemporaryDirectory() as data, socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    base = f"http://127.0.0.1:{port}"
    env = dict(PATH=os.environ["PATH"], HOME=data, INBOX_DATA=data, INBOX_UI=ui,
               INBOX_PORT=str(port), INBOX_ORIGIN=base)
    proc = subprocess.Popen([daemon, "serve"], env=env, stderr=subprocess.DEVNULL)
    try:
        for attempt in range(120):
            try:
                urllib.request.urlopen(base+"/api/health",timeout=1).close()
                break
            except OSError:
                time.sleep(.05)
        with sync_playwright() as pw:
            browser = pw.chromium.launch(executable_path=os.environ.get("CHROMIUM"),
                args=["--no-sandbox","--disable-dev-shm-usage","--no-zygote","--single-process"])
            page = browser.new_page(viewport={"width":1440,"height":900})
            page.goto(base)
            page.get_by_label("Setup code").fill(pathlib.Path(data,"setup-code").read_text())
            page.get_by_label("Username").fill("owner")
            page.get_by_label("Password").fill("disposable-layout-test")
            page.get_by_role("button",name="Create account").click()
            page.get_by_role("button",name="Add your first agent").wait_for()
            def post(path,payload,token=None):
                response = page.request.post(base+"/api/"+path,data=payload,headers={
                    "Origin":base, **({"Authorization":"Bearer "+token} if token else {})})
                assert response.ok,(path,response.status)
                return response.json()
            token=post("desk/agents",{"name":"sender"})["credential"]
            with sqlite3.connect(os.path.join(data,"console.sqlite")) as db:
                for i,row in enumerate(sample):
                    sent=post("agent/send",{"subject":"Fixture","key":str(uuid.uuid4()),
                        "to":"operator","body":row["body"]},token)
                    # Simulate historical envelopes without a separate subject column.
                    db.execute("UPDATE messages SET subject='' WHERE id=?",(sent["id"],))
                    db.commit()
                db.execute("insert into deliveries(seat,seq,accepted_at) select 'observer',seq,created_at from messages where id=?",(sent["id"],))
                db.commit()
            page.goto(base+"/inboxes/operator")
            page.locator(".post-row").first.wait_for()
            page.get_by_role("button",name="Load older messages").click()
            page.wait_for_function("document.querySelectorAll('.post-row').length===41")
            assert page.locator(".urgent-mail").count()>=1
            assert all(t not in ("(No subject)","Priority: p1","Priority: p2")
                       for t in page.locator(".post-main strong").all_text_contents())
            for width in [1440,390]:
                page.set_viewport_size({"width":width,"height":850})
                assert page.evaluate("document.documentElement.scrollWidth<=innerWidth")
            page.locator(".post-row").first.click()
            page.get_by_text("All 2 recipients").click()
            assert page.get_by_text("observer",exact=True).is_visible()
            page.locator(".envelope-fields summary").click()
            assert "Priority: p0" in page.locator(".envelope-fields pre").inner_text()
            assert page.locator(".letter-body").inner_text().strip()
            page.evaluate("document.fonts.ready")
            for family in ("Space Grotesk", "Source Serif 4", "JetBrains Mono"):
                assert page.evaluate("family => [...document.fonts].some(f=>f.family.replaceAll('\"','')===family && f.status==='loaded')",family)
            assert "Source Serif 4" in page.locator(".letter-body").evaluate("node=>getComputedStyle(node).fontFamily")
            assert page.evaluate("document.documentElement.scrollWidth<=innerWidth")
            browser.close()
            print("PASS: 41 actual bodies, priority and subject fallback, pagination, raw headers and 1440/390px layouts.")
    finally:
        proc.terminate()
        proc.wait(timeout=10)
