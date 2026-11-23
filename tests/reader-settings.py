import os,uuid,subprocess,tempfile,urllib.request,time,socket,pathlib,sqlite3,datetime
from playwright.sync_api import sync_playwright
with tempfile.TemporaryDirectory() as root:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1",0));port=sock.getsockname()[1]
    url=f"http://127.0.0.1:{port}"
    env=dict(os.environ,INBOX_DATA=root,INBOX_PORT=str(port),INBOX_ORIGIN=url,INBOX_UI=os.path.abspath("web/out"))
    proc=subprocess.Popen(["dist/linux-amd64/openagentinboxd","serve"],env=env)
    try:
        for i in range(200):
            try:urllib.request.urlopen(url+"/api/health").close();break
            except OSError:time.sleep(.05)
        with sync_playwright() as pw:
            b=pw.chromium.launch(executable_path=os.environ.get("CHROMIUM_BINARY"),args=["--no-sandbox","--disable-dev-shm-usage","--no-zygote","--single-process"])
            page=b.new_page(viewport={"width":1440,"height":910})
            page.set_default_timeout(6000)
            page.goto(url)
            page.get_by_label("Setup code").fill(pathlib.Path(root,"setup-code").read_text())
            page.get_by_label("Username").fill("admin");page.get_by_label("Password").fill("disposable-password")
            page.get_by_role("button",name="Create account").click()
            page.get_by_role("button",name="Add your first agent").wait_for()
            def post(path,data,token=None):
                r=page.request.post(url+"/api/"+path,data=data,headers={"Origin":url,**({"Authorization":"Bearer "+token} if token else {})})
                assert r.ok,(path,r.text());return r.json()
            names=["architect","studio-review","build-engineer","release-editor","field-research","design-systems"]
            tokens={n:post("desk/agents",{"name":n})["credential"] for n in names}
            def send(src,to,subject,body,parent=""):
                return post("agent/send",{"to":to,"subject":subject,"body":body,"key":str(uuid.uuid4()),"replyTo":parent},tokens[src])["id"]
            subjects=["November delivery notes","Review notes from the design team","Release checklist for Friday","Mailbox recovery drill","Gate results for the release candidate","Research digest for the next briefing","Updated component library","Staging build is ready for review"]
            for i in range(40):
                send(names[1+i%5],"architect",subjects[i%8],["The revised handoff includes the release notes and the rollback instructions.","Two comments remain on the illustrated cover. Both are linked in the review.","Friday’s build has passed the packaging checks and is ready for staging.","We restored the previous snapshot and checked every receipt against the archive.","The release candidate has passed the Linux ARM64 gate.","Field notes are ready. The second finding changes our recommended rollout order.","The new controls are in the component library. The reader now uses the same button heights.","Please review the staging build before the next dispatch."][i%8])
            first=send("design-systems","architect","A quieter way to read release updates","The revised reader is ready.\n\nEach conversation now has a permanent link. Can you review the narrow-screen layout before we publish?")
            send("architect","design-systems","Re: A quieter way to read release updates","The mobile layout is clear. Please ship the revision.",first)
            opened=send("studio-review","architect","Design review for the November release","The latest build passed our checks.\n\nWe verified the message view at phone and desktop widths. Long subjects wrap and the reading area stays clear.")
            h={"Authorization":"Bearer "+tokens["architect"]}
            assert page.request.get(url+"/api/agent/message/"+opened,headers=h).ok
            # Give the oldest and latest rows realistic times. All messages are synthetic.
            dbpath=next(p for p in pathlib.Path(root).rglob('*') if p.is_file() and p.read_bytes()[:15]==b'SQLite format 3')
            db=sqlite3.connect(dbpath)
            for seq, in db.execute("select seq from messages").fetchall():
                mins=(44-seq)*4 if seq>36 else 1440+(36-seq)*9
                ts=datetime.datetime.now(datetime.timezone.utc)-datetime.timedelta(minutes=mins)
                db.execute("update messages set created_text=? where seq=?",(ts.isoformat().replace("+00:00","Z"),seq))
                db.execute("update deliveries set accepted_at=? where seq=?",(int(ts.timestamp()*1000),seq))
            db.commit();db.close()
            page.goto(url+"/inboxes/architect")
            page.locator(".post-row").first.wait_for()
            assert page.locator(".post-row").count()==40
            assert page.get_by_text("Opened",exact=True).count()>=1
            assert page.get_by_text("Replied",exact=True).count()>=1
            assert page.locator("time").first.get_attribute("title").startswith("Received")
            assert page.locator(".post-day").count()>1
            page.screenshot(path=root+"/inbox.png")
            page.locator(".post-row").filter(has_text="A quieter way").click()
            page.locator(".timeline .post-row").first.click()
            page.locator(".thread-body pre").wait_for()
            page.screenshot(path=root+"/thread.png")
            page.get_by_text("Open message page").click()
            page.get_by_role("button",name="Copy message ID").wait_for()
            page.reload();page.locator(".reader h1").wait_for()
            page.goto(url+"/messages/"+opened)
            page.locator(".letter-body").wait_for()
            page.screenshot(path=root+"/reader.png")
            assert page.locator(".reader-actions .mail-status").count()==0
            assert page.locator(".message-meta .mail-status").inner_text()=="Opened"
            assert not page.locator(".reader .mator").count()
            page.get_by_role("button",name="Settings",exact=True).click()
            version = page.request.get(url+"/api/health").json()["version"]
            assert version
            page.get_by_text(version,exact=True).wait_for()
            assert page.url.endswith("/settings")
            page.get_by_label("Dark",exact=True).check()
            page.wait_for_function("document.documentElement.dataset.palette==='dark'")
            page.reload();page.get_by_label("Dark",exact=True).wait_for()
            assert page.get_by_label("Dark",exact=True).is_checked()
            page.screenshot(path=root+"/settings.png")
            page.route("**/api/health",lambda route:route.fulfill(status=503,body="unavailable"))
            page.reload();page.get_by_text("Unavailable",exact=True).wait_for()
            page.unroute("**/api/health");page.get_by_text("Check again").click();page.get_by_text(version,exact=True).wait_for()
            page.get_by_label("System",exact=True).check()
            for mode in ("light","dark"):
                page.emulate_media(color_scheme=mode)
                page.wait_for_function("mode => document.documentElement.dataset.palette===mode",arg=mode)
            page.goto(url+"/messages/"+opened);page.locator(".letter-body").wait_for()
            page.screenshot(path=root+"/reader-dark.png")
            page.goto(url+"/inboxes/architect");page.locator(".post-row").first.wait_for()
            stamps=page.locator(".post-row .sender-stamp").evaluate_all("els=>new Set(els.map(e=>e.innerHTML)).size")
            assert stamps>=5
            page.screenshot(path=root+"/inbox-dark.png")
            page.set_viewport_size({"width":390,"height":844})
            for path in ("/messages/"+opened,"/inboxes/architect"):
                page.goto(url+path);page.locator(".reader,.post-row").first.wait_for()
                assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
            page.screenshot(path=root+"/mobile.png")
            page.get_by_role("button",name="Mailboxes").click()
            page.get_by_label("Find an inbox").fill("design")
            page.locator(".directory-list a").first.click()
            assert page.url.endswith("/inboxes/design-systems")
            print("PASS: 40 conversation heads, linked replies, singleton and direct routes, receipt states, relative and previous-day timestamps, inline body, desktop and mobile layout, directory navigation.")
            b.close()
    finally:proc.terminate();proc.wait(timeout=10)
