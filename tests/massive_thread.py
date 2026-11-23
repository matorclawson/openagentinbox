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
    start=dt.datetime.now(dt.timezone.utc)-dt.timedelta(minutes=50000)
    titles=["Update the delivery checklist","Review the acceptance criteria","Decide the staging window","Confirm release ownership"]
    with sqlite3.connect(Path(root)/"console.sqlite") as db:
        previous=None
        for n in range(1,10001):
            incoming=n%2==1
            label="Delivery planning" if n==1 else titles[n%4]
            body="The update is ready for review.\n\n"+("We chose the heliotrope release window after the overnight restore test." if n==5000 else "The staging schedule is recorded in the handoff.")
            stamp=start+dt.timedelta(minutes=(n//3)*15 if n%11 else (n-5)*5)
            if 500<=n<620:stamp=(dt.datetime.now(dt.timezone.utc)-dt.timedelta(days=10)).replace(hour=14,minute=n%60,second=0,microsecond=0)
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
    page.locator(".outline-messages .post-row").first.wait_for()
    assert 0<page.locator(".timeline .post-row").count()<=40
    assert "5,000 received" in page.locator(".mail-heading").inner_text()
    def shot(name):
        dest=os.getenv("INBOX_TEST_PREVIEWS")
        if dest:page.screenshot(path=str(Path(dest)/(name+".png")))
    page.set_viewport_size({"width":390,"height":844})
    def selected(level):
        return page.locator(f".time-branch[data-level={level}][aria-expanded=true]")
    def branch(level,at):
        return page.locator(f".time-branch[data-level={level}][data-start='{at}']")
    def check_targets():
        for node in page.locator(".time-branch:visible").all():
            rect=node.bounding_box()
            assert rect and rect["width"]>=48 and rect["height"]>=48
        assert page.evaluate("document.documentElement.scrollWidth<=innerWidth")
    selected("month").tap()
    page.wait_for_function("location.search.includes('period=all')")
    assert page.locator(".time-branch[data-level=week]").count()==0
    shot("months-mobile")
    selected_day=(dt.datetime.now(dt.timezone.utc)-dt.timedelta(days=10)).date()
    target=dt.datetime.combine(selected_day,dt.time.min,tzinfo=dt.timezone.utc)
    target_ms=int(target.timestamp()*1000)
    month_ms=int(target.replace(day=1).timestamp()*1000)
    week_ms=max(month_ms,target_ms-target.weekday()*86400000)
    for level,at in (("month",month_ms),("week",week_ms),("day",target_ms),("band",target_ms+12*3600000),("hour",target_ms+14*3600000)):
        branch(level,at).tap()
        selected(level).wait_for()
    page.locator(".outline-messages .post-row").first.wait_for()
    assert page.locator(".outline-messages .post-row").count()==40
    for width in (320,390,768,1440):
        page.set_viewport_size({"width":width,"height":850});check_targets()
    page.set_viewport_size({"width":390,"height":844})
    selected("hour").scroll_into_view_if_needed()
    shot("expanded-hour")
    deep_link=page.url
    page.get_by_role("button",name="← Older").click()
    page.wait_for_function("location.search.includes('before=')")
    assert page.locator(".outline-messages .post-row").count()<=40
    page.goto(deep_link);selected("hour").wait_for()
    selected("day").tap();selected("week").wait_for()
    assert not page.locator(".time-branch[data-level=band]").count()
    page.go_back();selected("hour").wait_for()
    selected("band").scroll_into_view_if_needed();shot("time-of-day")
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
    headers={"Authorization":"Bearer "+tokens["build-engineer"]}
    for level,at in (("months",0),("days",month_ms),("hours",target_ms)):
        private=page.request.get(url+"/api/agent/threads/"+root_id+f"?outline={level}&at={at}",headers=headers)
        assert private.ok and private.json()["items"]==[]
    page.set_viewport_size({"width":1440,"height":910})
    shot("outline-desktop")
    print("PASS month/week/day/daypart/hour expansion, subtree collapse, browser Back, deep links, bounded hour paging, touch targets and aggregate isolation.")
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
