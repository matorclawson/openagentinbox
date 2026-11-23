"""Large-thread regression fixture. Invoked from reader-settings.py with disposable accounts."""
import datetime as dt
import os
from pathlib import Path
import sqlite3
import time
import uuid
from statistics import median

def check_thread(page,url,root,tokens):
    root_id=str(uuid.uuid4())
    start=dt.datetime.now(dt.timezone.utc)-dt.timedelta(days=7)
    titles=["Update the delivery checklist","Review the acceptance criteria","Decide the staging window","Confirm release ownership"]
    with sqlite3.connect(Path(root)/"console.sqlite") as db:
        previous=None
        for n in range(1,10001):
            incoming=n%2==1
            label="Delivery planning" if n==1 else titles[n%4]
            body="The update is ready for review.\n\n"+("We chose the heliotrope release window after the overnight restore test." if n==5000 else "The staging schedule is recorded in the handoff.")
            stamp=start+dt.timedelta(minutes=n)
            ms=int(stamp.timestamp()*1000)
            message_id=root_id if n==1 else str(uuid.uuid4())
            c=db.execute("INSERT INTO messages(id,sender,subject,body,bytes,created_at,created_text,display_body,parent_id) VALUES(?,?,?,?,?,?,?,?,?)",
                (message_id,"studio-review" if incoming else "architect",label,body.encode(),len(body),ms,stamp.isoformat(),body.splitlines()[0],previous))
            previous=message_id
            db.execute("INSERT INTO deliveries(seat,seq,accepted_at) VALUES(?,?,?)",("architect" if incoming else "studio-review",c.lastrowid,ms))
            if n==5000:middle=str(c.lastrowid)
    route="/inboxes/architect/threads/"+root_id
    api=url+"/api/desk/threads/"+root_id+"?box=architect"
    page.goto(url+route)
    page.get_by_role("heading",name="Delivery planning").wait_for()
    assert page.locator(".timeline .post-row").count()==40
    assert "5,000 received" in page.locator(".mail-heading").inner_text()
    def shot(name):
        dest=os.getenv("INBOX_TEST_PREVIEWS")
        if dest:page.screenshot(path=str(Path(dest)/(name+".png")))
    page.get_by_role("button",name="First",exact=True).click()
    page.wait_for_function("location.search.includes('after=0') && document.querySelectorAll('.timeline .post-row').length===40 && document.querySelector('.thread-steps button').disabled")
    page.get_by_role("button",name="Newer →").click()
    page.wait_for_function("!location.search.includes('after=0') && location.search.includes('after=')")
    page.get_by_label("Message number from the beginning").fill("5000")
    page.get_by_role("button",name="Jump",exact=True).click()
    page.locator(".post-row.anchored").wait_for()
    assert page.url.endswith("?atSeq="+middle)
    shot("thread-middle")
    page.locator(".anchored").click()
    assert "heliotrope" in page.locator(".thread-body pre").inner_text()
    search=page.get_by_label("Search this conversation")
    search.fill("heliotrope");search.press("Enter")
    page.get_by_text("heliotrope",exact=True).first.wait_for()
    assert page.locator(".timeline .post-row").count()==1
    assert page.locator("mark").inner_text()=="heliotrope"
    shot("thread-search")
    page.get_by_text("View surrounding replies").click()
    page.locator(".anchored").wait_for()
    page.set_viewport_size({"width":390,"height":844})
    assert page.evaluate("document.documentElement.scrollWidth<=innerWidth")
    shot("thread-mobile")
    page.goto(url+"/inboxes/architect/sent?q=heliotrope")
    assert page.locator(".thread-origin").inner_text()=="In Delivery planning"
    assert page.locator("mark").inner_text()=="heliotrope"
    page.set_viewport_size({"width":1440,"height":910})
    shot("mail-search")
    page.get_by_text("View options").click()
    page.get_by_label("Compact rows").uncheck()
    page.get_by_text("View options").click()
    assert page.locator(".mail-tabs").count()==0
    res=page.request.get(url+"/api/agent/threads/"+root_id+"?q=heliotrope",headers={"Authorization":"Bearer "+tokens["build-engineer"]})
    assert res.ok and not res.json()["rows"]
    positions=[];times=[];size=0;cursor=""
    while True:
        tick=time.perf_counter()
        res=page.request.get(api+("&before="+cursor if cursor else ""))
        assert res.ok
        times.append((time.perf_counter()-tick)*1000)
        size=max(size,len(res.body()))
        data=res.json()
        assert len(data["rows"])<=40 and all("body" not in r for r in data["rows"])
        positions.extend(int(row["position"]) for row in data["rows"])
        cursor=data["next"]
        if not cursor:break
    assert len(positions)==len(set(positions))==10000 and positions==sorted(positions,reverse=True)
    assert len(times)==250
    print(f"PASS 10,000 alternating messages, 250 disjoint pages, maximum 40 rows. Loopback HTTP page median {median(times):.1f} ms, p95 {sorted(times)[237]:.1f} ms, largest JSON {size} bytes.")
