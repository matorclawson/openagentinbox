import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { randomUUID } from "node:crypto";
import { pathToFileURL } from "node:url";
type Instance = Awaited<ReturnType<typeof service>>;
import { service } from "./daemon.ts";
const origin = "https://inbox.example";
function fixture() {
  return mkdtempSync(join(tmpdir(), "inbox-test-"));
}
function post(a: Instance, path: string, payload: object, cookie = "") {
  return a.inject({
    method: "POST",
    url: path,
    payload,
    headers: { origin, cookie },
  });
}
function cookie(res: { headers: Record<string, unknown> }) {
  return String(res.headers["set-cookie"]).split(";")[0];
}
test("setup, idempotent conversations, agent isolation, revocation and restart", async () => {
  const dir = fixture();
  let a = await service({ dir, origin });
  try {
    const account = {
      code: readFileSync(join(dir, "setup-code"), "utf8"),
      username: "rod",
      password: "test-only-long-password",
    };
    assert.equal(
      (await post(a, "/api/setup", { ...account, code: "wrong" })).statusCode,
      403,
    );
    const session = cookie(await post(a, "/api/setup", account));
    assert.equal((await post(a, "/api/setup", account)).statusCode, 409);
    assert.equal((await a.inject("/api/desk/accounts")).statusCode, 401);
    assert.equal(
      (
        await a.inject({
          method: "POST",
          url: "/api/desk/agents",
          payload: { name: "fake" },
          headers: { cookie: session },
        })
      ).statusCode,
      403,
    );
    const { credential } = (
      await post(a, "/api/desk/agents", { name: "assistant" }, session)
    ).json();
    const auth = { authorization: "Bearer " + credential };
    const m = {
      to: "assistant",
      subject: "Question",
      body: "What arrived?",
      key: randomUUID(),
    };
    const id = (await post(a, "/api/desk/send", m, session)).json().id;
    assert.ok(id);
    assert.equal((await post(a, "/api/desk/send", m, session)).json().id, id);
    assert.equal(
      (await post(a, "/api/desk/send", { ...m, subject: "changed" }, session))
        .statusCode,
      409,
    );
    assert.equal(
      (await a.inject({ url: "/api/agent/inbox", headers: auth })).json()
        .rows[0].id,
      id,
    );
    assert.equal(
      (
        await a.inject({ url: "/api/agent/message/" + id, headers: auth })
      ).json().body,
      m.body,
    );
    const answer = await a.inject({
      url: "/api/agent/send",
      method: "POST",
      headers: auth,
      payload: {
        to: "operator",
        subject: "Re: Question",
        body: "Received",
        key: randomUUID(),
      },
    });
    assert.equal(answer.statusCode, 200);
    assert.equal(
      (
        await a.inject({
          url: "/api/agent/message/" + answer.json().id,
          headers: auth,
        })
      ).statusCode,
      404,
    );
    await a.close();
    a = await service({ dir, origin });
    assert.equal(
      (
        await a.inject({ url: "/api/desk/list", headers: { cookie: session } })
      ).json().rows[0].id,
      answer.json().id,
    );
    assert.equal(
      (await post(a, "/api/desk/revoke", { name: "assistant" }, session))
        .statusCode,
      200,
    );
    assert.equal(
      (await a.inject({ url: "/api/agent/inbox", headers: auth })).statusCode,
      401,
    );
    await post(a, "/api/logout", {}, session);
    assert.equal(
      (
        await a.inject({
          url: "/api/desk/accounts",
          headers: { cookie: session },
        })
      ).statusCode,
      401,
    );
    assert.equal((await post(a, "/api/login", account)).statusCode, 200);
  } finally {
    await a.close();
    rmSync(dir, { recursive: true });
  }
});
test(
  "OpenPassword 0.2.0 consent, exchange, browser binding and upstream logout",
  { skip: !process.env.OPENPASSWORD_SOURCE },
  async () => {
    const dir = fixture(),
      odir = fixture(),
      issuer = "http://127.0.0.1:41789";
    const { service: open } = await import(
      pathToFileURL(join(process.env.OPENPASSWORD_SOURCE!, "server/service.ts"))
        .href
    );
    const op = open(odir, issuer);
    await op.listen({port:41789,host:"127.0.0.1"});
    const h = cookie(
      await op.inject({
        url: "/admin/login",
        method: "POST",
        headers: { origin: issuer },
        payload: { token: readFileSync(join(odir, "operator.token"), "utf8") },
      }),
    );
    const opPost = (url: string, payload: object) =>
      op.inject({
        url,
        method: "POST",
        payload,
        headers: { cookie: h, origin: issuer },
      });
    const app = (
      await opPost("/admin/apps", {
        label: "OpenAgentInbox",
        callback: origin + "/api/callback",
        audience: "openagentinbox",
        days: 1,
        classes: [],
      })
    ).json().id;
    assert.ok(app);
    await opPost("/admin/apps/" + app, { active: true });
    const a = await service({ dir, origin, op: { origin: issuer, app, audience:"openagentinbox" } });
    try {
      const start = await post(a, "/api/connect", {
        code: readFileSync(join(dir, "setup-code"), "utf8"),
      });
      const u = new URL(start.json().url),
        pair = cookie(start);
      const redirect = new URL(
        String((await op.inject(u.pathname + u.search)).headers.location),
      );
      const consent = await op.inject({
        url: redirect.pathname + redirect.search,
        headers: { cookie: h },
      });
      const csrf = /name="csrf" value="([^"]+)"/.exec(consent.body)![1];
      const confirmed = await opPost("/delegate/confirm", {
        attempt: redirect.searchParams.get("attempt"),
        csrf,
      });
      const callback = new URL(String(confirmed.headers.location));
      const url = callback.pathname + callback.search;
      assert.equal((await a.inject(url)).statusCode, 401);
      const result = await a.inject({ url, headers: { cookie: pair } });
      assert.equal(result.statusCode, 303, result.body);
      const session = cookie(result);
      assert.equal(
        (await a.inject({ url, headers: { cookie: pair } })).statusCode,
        401,
      );
      assert.equal(
        (
          await a.inject({
            url: "/api/desk/accounts",
            headers: { cookie: session },
          })
        ).statusCode,
        200,
      );
      await opPost("/admin/logout", {});
      assert.equal(
        (
          await a.inject({
            url: "/api/desk/accounts",
            headers: { cookie: session },
          })
        ).statusCode,
        401,
      );
    } finally {
      await a.close();
      await op.close();
      rmSync(dir, { recursive: true });
      rmSync(odir, { recursive: true });
    }
  },
);
test("self enrollment needs owner consent, survives retries and cannot seize a seat", async () => {
  const dir = fixture();
  let a = await service({ dir, origin });
  const p = "a".repeat(43),
    other = "b".repeat(43);
  const joinAgent = (t: string) =>
    a.inject({
      url: "/api/agent/enroll",
      method: "POST",
      headers: { authorization: "Bearer " + t },
      payload: { name: "new-agent" },
    });
  try {
    const s = cookie(
      await post(a, "/api/setup", {
        code: readFileSync(join(dir, "setup-code"), "utf8"),
        username: "owner",
        password: "temporary-password",
      }),
    );
    const request = (await joinAgent(p)).json();
    assert.equal(request.status, "pending");
    assert.equal((await joinAgent(p)).json().code, request.code);
    assert.equal(
      (
        await a.inject({
          url: "/api/agent/inbox",
          headers: { authorization: "Bearer " + p },
        })
      ).statusCode,
      401,
    );
    const approve = { name: request.name, code: request.code, approve: true };
    assert.equal(
      (await post(a, "/api/desk/enrollment", approve)).statusCode,
      401,
    );
    await a.close();
    a = await service({ dir, origin });
    assert.equal(
      (await post(a, "/api/desk/enrollment", approve, s)).statusCode,
      200,
    );
    assert.equal(
      (await post(a, "/api/desk/enrollment", approve, s)).statusCode,
      404,
    );
    assert.equal((await joinAgent(other)).statusCode, 409);
    assert.equal((await joinAgent(p)).json().status, "active");
    await post(a, "/api/desk/revoke", { name: "new-agent" }, s);
    assert.equal((await joinAgent(p)).statusCode, 409);
  } finally {
    await a.close();
    rmSync(dir, { recursive: true });
  }
});

test("0.2.x upgrade preserves the owner, agent key, retry identity, pagination and recovery copy", async () => {
 const { DatabaseSync } = await import("node:sqlite");
 const { scryptSync, createHash } = await import("node:crypto");
 const { readdirSync } = await import("node:fs");
 const dir=fixture(),path=join(dir,"console.sqlite"),id=randomUUID(),key=randomUUID(),secret="c".repeat(43);
 const db=new DatabaseSync(path);
 db.exec("CREATE TABLE letters(seq INTEGER PRIMARY KEY,id TEXT UNIQUE,src TEXT,dst TEXT,subject TEXT,body TEXT,at TEXT,operation TEXT,UNIQUE(src,operation)); CREATE TABLE owner(id INTEGER PRIMARY KEY,username TEXT,salt TEXT,password TEXT,principal TEXT);CREATE TABLE agents(name TEXT PRIMARY KEY,key TEXT UNIQUE)");
 db.prepare("INSERT INTO owner VALUES(1,'owner','salt',?,NULL)").run(scryptSync("previous-password","salt",64).toString("hex"));
 db.prepare("INSERT INTO agents VALUES('robot',?)").run(createHash("sha256").update(secret).digest("hex"));
 db.prepare("INSERT INTO letters VALUES(93,?,'operator','robot','Question','Original body',?,?)").run(id,new Date().toISOString(),key);db.close();
 const a=await service({dir,origin});
 try{
 const login=await post(a,"/api/login",{username:"owner",password:"previous-password"});assert.equal(login.statusCode,200);
 const auth={authorization:"Bearer "+secret};
 const page=(await a.inject({url:"/api/agent/inbox?before=94",headers:auth})).json();assert.equal(page.rows[0].seq,93);assert.equal(page.rows[0].id,id);
 assert.equal((await a.inject({url:"/api/agent/message/"+id,headers:auth})).json().body,"Original body");
 assert.equal((await post(a,"/api/desk/send",{to:"robot",subject:"Question",body:"Original body",key},cookie(login))).json().id,id);
 const snapshot=readdirSync(dir).find(n=>n.startsWith("pre-native-"));assert.ok(snapshot);const recovery=new DatabaseSync(join(dir,snapshot),{readOnly:true});assert.equal(recovery.prepare("SELECT id FROM letters").get()?.id,id);recovery.close();
 const native=new DatabaseSync(path,{readOnly:true});assert.equal(native.prepare("SELECT count(*) AS n FROM messages").get()?.n,1);assert.equal(native.prepare("SELECT count(*) AS n FROM sqlite_master WHERE name='letters'").get()?.n,0);native.close();
 }finally{await a.close();rmSync(dir,{recursive:true})}
});

test("MCP uses the enrolled identity, the HTTP mailbox and the same retry key", async()=>{
 const dir=fixture(), a=await service({dir,origin});
 const {spawn}=await import("node:child_process");
 const {writeFileSync}=await import("node:fs");
 try {
 const owner=cookie(await post(a,"/api/setup",{username:"owner",password:"temporary-password",code:readFileSync(join(dir,"setup-code"),"utf8")}));
 const agent=(await post(a,"/api/desk/agents",{name:"researcher"},owner)).json();
 const token=agent.credential;
 const rpc=(method:string,params:object={},credential=token)=>a.inject({method:"POST",url:"/mcp",headers:{authorization:"Bearer "+credential},payload:{jsonrpc:"2.0",id:1,method,params}});
 assert.equal((await rpc("tools/list",{},"bogus")).statusCode,401);
 assert.deepEqual((await rpc("tools/list")).json().result.tools.map((t:{name:string})=>t.name),["whoami","inbox","read","send","receipt","request","alerts"]);
 const msg={to:"operator",subject:"MCP report",body:"One copy",key:randomUUID()};
 const sent=(await rpc("tools/call",{name:"send",arguments:msg})).json().result;assert.equal(sent.isError,false);
 const id=JSON.parse(sent.content[0].text).id;
 assert.equal((await a.inject({method:"POST",url:"/api/agent/send",headers:{authorization:"Bearer "+token},payload:msg})).json().id,id);
 const rows=(await a.inject({url:"/api/desk/list",headers:{cookie:owner}})).json().rows;assert.deepEqual(rows.map((m:{id:string})=>m.id),[id]);
 assert.equal((await rpc("tools/call",{name:"read",arguments:{id}})).json().result.isError,true);
 const incoming=(await post(a,"/api/desk/send",{...msg,to:"researcher"},owner)).json().id;
 const profile=join(dir,"agent.json");writeFileSync(profile,JSON.stringify({name:"researcher",token,origin:a.base}),{mode:0o600});
 const child=spawn(process.execPath,["out/openagentinbox.mjs","mcp"],{env:{...process.env,OPENAGENTINBOX_PROFILE:profile}});
 const frames=[{id:1,method:"initialize",params:{protocolVersion:"2025-11-25"}},{method:"notifications/initialized"},{id:2,method:"tools/call",params:{name:"inbox",arguments:{}}},{id:3,method:"tools/call",params:{name:"read",arguments:{id:incoming}}}];
 const exited=new Promise(resolve=>child.on("close",resolve));
 child.stdin.end(frames.map(q=>JSON.stringify({jsonrpc:"2.0",...q})).join("\n")+"\n");let output="";for await(const part of child.stdout)output+=part;assert.equal(await exited,0);
 const replies=output.trim().split("\n").map(x=>JSON.parse(x));assert.deepEqual(replies.map(q=>q.id),[1,2,3]);assert.equal(replies[0].result.protocolVersion,"2025-11-25");assert.equal(JSON.parse(replies[1].result.content[0].text).rows[0].id,incoming);assert.equal(JSON.parse(replies[2].result.content[0].text).body,msg.body);
 await post(a,"/api/desk/revoke",{name:"researcher"},owner);assert.equal((await rpc("tools/list")).statusCode,401);
 } finally {await a.close();rmSync(dir,{recursive:true});}
});

test("recipient detail records a first open, linked replies close the request atomically",async()=>{
 const dir=fixture();const a=await service({dir,origin});
 try{
 const owner=cookie(await post(a,"/api/setup",{username:"owner",password:"temporary-password",code:readFileSync(join(dir,"setup-code"),"utf8")}));
 const token=(await post(a,"/api/desk/agents",{name:"worker"},owner)).json().credential;
 const headers={authorization:"Bearer "+token};
 const original={to:"worker",subject:"Review",body:"Please review",key:randomUUID(),intent:"urgent"};
 const msg=await post(a,"/api/desk/send",original,owner);assert.equal(msg.statusCode,200);const id=msg.json().id;
 const audit=async()=>(await a.inject({url:"/api/desk/receipt/"+id,headers:{cookie:owner}})).json();
 await a.inject({url:"/api/desk/message/"+id,headers:{cookie:owner}});
 await a.inject({url:"/api/agent/inbox",headers});
 let r=await audit();assert.equal(r.opened,0);assert.equal(r.state,"pending");assert.ok(Math.abs(r.due-Date.now()-120000)<5000);
 await a.inject({url:"/api/agent/message/"+id,headers});const first=(await audit()).opened;assert.ok(first>0);
 await a.inject({url:"/api/agent/message/"+id,headers});assert.equal((await audit()).opened,first);
 const until=Date.now()+3600000;
 for(const action of ["defer","accept"]){const res=await a.inject({url:"/api/agent/request",method:"POST",headers,payload:{action,id,until}});assert.equal(res.statusCode,200);assert.equal(res.json().due,until)}
 const reply={to:"operator",subject:"Result",body:"Done",replyTo:id,key:randomUUID()};
 const send=(payload:object)=>a.inject({url:"/api/agent/send",method:"POST",headers,payload});
 assert.equal((await send({...reply,to:"worker"})).statusCode,400);
 const sent=await send(reply);assert.equal(sent.statusCode,200);assert.equal((await send(reply)).json().id,sent.json().id);
 r=await audit();assert.equal(r.state,"resolved");assert.ok(r.replied>=first);assert.equal(r.due,0);
 assert.equal((await post(a,"/api/desk/send",{...original,intent:"request"},owner)).statusCode,409);
 }finally{await a.close();rmSync(dir,{recursive:true})}
});

test("performance diagnostics count successful operations and keep concurrent opens idempotent",async()=>{
 const dir=fixture(),a=await service({dir,origin});
 try{
  const owner=cookie(await post(a,"/api/setup",{username:"owner",password:"temporary-password",code:readFileSync(join(dir,"setup-code"),"utf8")}));
  const {credential}=(await post(a,"/api/desk/agents",{name:"worker"},owner)).json();
  const payload={to:"worker",key:randomUUID(),subject:"Review",body:"Read concurrently"};
  const id=(await post(a,"/api/desk/send",payload,owner)).json().id;
  await post(a,"/api/desk/send",payload,owner);
  const get=()=>a.inject({url:"/api/agent/message/"+id,headers:{authorization:"Bearer "+credential}});
  const opens=await Promise.all(Array.from({length:16},get));const stamp=opens[0].json().opened;
  assert.ok(stamp>0);assert.ok(opens.every(r=>r.statusCode===200&&r.json().opened===stamp));
  assert.equal((await a.inject({url:"/api/desk/performance"})).statusCode,401);
  const stats=(await a.inject({url:"/api/desk/performance",headers:{cookie:owner}})).json();
  assert.equal(stats.successful_sends.count,2);assert.equal(stats.successful_body_reads.count,16);
  assert.equal(Math.round(stats.estimated_recipient_mailboxes),1);assert.ok(stats.successful_body_reads.p95_upper_ms>0);
 }finally{await a.close();rmSync(dir,{recursive:true})}
});
