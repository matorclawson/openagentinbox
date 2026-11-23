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
    start=dt.datetime.now(dt.timezone.utc)-dt.timedelta(minutes=20000)
    titles=["Update the delivery checklist","Review the acceptance criteria","Decide the staging window","Confirm release ownership"]
    with sqlite3.connect(Path(root)/"console.sqlite") as db:
        previous=None
        for n in range(1,10001):
            incoming=n%2==1
            label="Delivery planning" if n==1 else titles[n%4]
            body="The update is ready for review.\n\n"+("We chose the heliotrope release window after the overnight restore test." if n==5000 else "The staging schedule is recorded in the handoff.")
            stamp=start+dt.timedelta(minutes=(n//3)*6 if n%11 else (n-5)*2)
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
    page.set_viewport_size({"width":390,"height":844})
    button=lambda name:page.get_by_role("button",name=name,exact=True)
    def check_targets():
        nodes=page.locator(".period-browser button:visible,.period-browser input:visible,.thread-steps button:visible")
        for node in nodes.all():
            box=node.bounding_box()
            assert box and box["width"]>=48 and box["height"]>=48,(node.inner_text(),box)
        assert page.evaluate("document.documentElement.scrollWidth<=innerWidth")
    button("Last hour").tap()
    page.locator(".timeline .post-row").first.wait_for()
    assert page.locator(".scale-hour,.time-bars").count()==0
    assert 0<page.locator(".timeline .post-row").count()<=40
    check_targets()
    shot("hour-mobile")
    button("Day").tap()
    button("30 days").tap()
    button("Choose a day").tap()
    page.get_by_role("dialog").wait_for()
    selected_day=(dt.datetime.now(dt.timezone.utc)-dt.timedelta(days=10)).date()
    target=dt.datetime.combine(selected_day,dt.time.min,tzinfo=dt.timezone.utc)
    target_ms=int(target.timestamp()*1000)
    page.get_by_role("dialog").get_by_role("button",name="10 days ago",exact=False).tap()
    page.wait_for_function("new URLSearchParams(location.search).get('zoom')==='day'")
    assert page.url.endswith("zoom=day&start="+str(target_ms))
    button("Choose an hour").wait_for()
    shot("day-mobile")
    # Sent times include ties and out-of-order inserts. Verify each boundary and cursor.
    with sqlite3.connect(Path(root)/"console.sqlite") as db:
        expected=[row[0] for row in db.execute("SELECT m.seq FROM thread_members t JOIN messages m USING(seq) WHERE t.root=? AND m.created_at>=? AND m.created_at<? ORDER BY m.created_at DESC,m.seq DESC",(root_id,target_ms,target_ms+86400000))]
    period=api+"&zoom=day&start="+str(target_ms)
    positions=[];cursor="";pages=0
    while True:
        response=page.request.get(period+("&before="+cursor if cursor else ""))
        assert response.ok,response.text()
        data=response.json()
        assert sum(data["buckets"])==len(expected)
        positions.extend(int(row["position"]) for row in data["rows"]);pages+=1
        cursor=data["next"]
        if not cursor:break
    assert positions==expected
    assert pages>1
    page.get_by_role("button",name="← Older").click()
    page.wait_for_function("location.search.includes('before=')")
    page.get_by_role("button",name="Newer →").click()
    page.wait_for_function("location.search.includes('after=')")
    button("Choose an hour").tap()
    check_targets()
    shot("hours-mobile")
    page.get_by_role("button",name="Afternoon",exact=False).tap()
    for width in (320,390,768,1440):
        page.set_viewport_size({"width":width,"height":844})
        check_targets()
        assert page.get_by_role("dialog").evaluate("d=>d.scrollWidth<=d.clientWidth+1")
    page.set_viewport_size({"width":390,"height":844})
    pick=page.get_by_role("button",name="14:00 UTC",exact=False)
    pick.scroll_into_view_if_needed();shot("afternoon-mobile")
    pick.tap(position={"x":12,"y":12})
    page.wait_for_function("new URLSearchParams(location.search).get('zoom')==='hour'")
    assert page.url.endswith("zoom=hour&start="+str(target_ms+14*3600000))
    page.reload();page.locator(".timeline .post-row").first.wait_for()
    page.set_viewport_size({"width":1440,"height":910})
    shot("hour-desktop")
    button("Day").click()
    button("Choose an hour").click()
    page.keyboard.press("Escape")
    assert not page.get_by_role("dialog").count()
    assert button("Choose an hour").evaluate("el=>document.activeElement===el")
    unrelated=page.request.get(url+"/api/agent/threads/"+root_id+"?zoom=day&start="+str(target_ms),headers={"Authorization":"Bearer "+tokens["build-engineer"]}).json()
    assert not unrelated["rows"] and not sum(unrelated["buckets"])
    button("30 days").click()
    button("Choose a day").click()
    page.get_by_label("Go to date (UTC)").fill("2020-01-01")
    page.get_by_text("No messages in this period").wait_for()
    button("30 days").click()
    button("All time").click()
    page.set_viewport_size({"width":1440,"height":910})
    print(f"PASS last hour → 30 days → ten days ago → 14:00 UTC, calendar, empty dates, mobile layout, mailbox isolation and all {len(expected)} historical messages in sent-time order.")
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
