import test from "node:test";
import assert from "node:assert/strict";
import {createHmac,randomBytes,randomUUID} from "node:crypto";
import {mkdtempSync,readFileSync,rmSync} from "node:fs";
import {tmpdir} from "node:os";
import {join} from "node:path";
import {service} from "./daemon.ts";

test("full ordinary queue, one handshake for 76 agents, scoped wake and atomic review",async()=>{
 const dir=mkdtempSync(join(tmpdir(),"inbox-roster-")),origin="https://inbox.example";
 const a=await service({dir,origin});
 try{
 const post=(url:string,payload:object,headers:Record<string,string>={})=>a.inject({method:"POST",url,payload,headers:{origin,...headers}});
 const setup=await post("/api/setup",{username:"owner",password:"test-only-password",code:readFileSync(join(dir,"setup-code"),"utf8")});
 const owner={cookie:setup.headers["set-cookie"].split(";")[0]};
 const key=()=>randomBytes(32).toString("base64url");
 const auth=(token:string)=>({authorization:"Bearer "+token});
 const enroll=(name:string,token:string,ticket?:string)=>post("/api/agent/enroll",{name,ticket,host:"mac-mini"},auth(token));
 const desk=(url:string)=>a.inject({url:"/api/desk/"+url,headers:owner});
 const names=Array.from({length:76},(_,i)=>"agent-"+i),keys=names.map(key);
 const pending=[];
 for(let i=0;i<8;i++){const r=await enroll(names[i],keys[i]);assert.equal(r.statusCode,200);pending.push(r.json())}
 assert.equal((await enroll(names[8],keys[8])).statusCode,429);
 const snap=(await desk("live")).json();assert.equal(snap.pending.length,8);
 assert.equal((await a.inject("/api/desk/live")).statusCode,401);
 assert.equal((await post("/api/desk/batch",{approve:true,requests:[pending[0],{name:"missing",code:"missing"}].map(({name,code})=>({name,code}))},owner)).statusCode,409);
 assert.equal((await desk("live")).json().pending.length,8);
 const live=desk("live?after="+snap.cursor);
 const grant=await post("/api/desk/roster",{host:"mac-mini",ip:"127.0.0.1",names},owner);assert.equal(grant.statusCode,200);
 const secret=grant.json().secret;
 const req=pending[0];assert.equal((await post("/api/desk/enrollment",{name:req.name,code:req.code,approve:true},owner)).statusCode,409);
 assert.equal((await post("/api/agent/handshake",{host:"other"},auth(secret))).statusCode,403);
 const wrong=await post("/api/desk/roster",{host:"other",ip:"192.0.2.9",names:["remote-only"]},owner);
 assert.equal((await post("/api/agent/handshake",{host:"other"},{...auth(wrong.json().secret),"X-Inbox-Peer-IP":"192.0.2.9"})).statusCode,403);
 await post("/api/agent/enroll",{name:"remote-only",host:"other",ticket:createHmac("sha256",wrong.json().secret).update("remote-only").digest("base64url")},auth(key())).then(r=>assert.equal(r.statusCode,403));
 const extras=(await desk("live")).json().roster;await post("/api/desk/roster/cancel",{id:extras.find((v:unknown[])=>v[0]==="other")[3]},owner);

 assert.ok((await live).json().roster.some((r:unknown[])=>r[0]==="mac-mini"&&r[1]===76));
 assert.equal((await post("/api/agent/handshake",{host:"mac-mini"},auth(secret))).json().names.length,76);
 const ticket=(name:string)=>createHmac("sha256",secret).update(name).digest("base64url");
 assert.equal((await enroll("outsider",key(),ticket(names[0]))).statusCode,403);
 const joined=await Promise.all(names.map((n,i)=>enroll(n,keys[i],ticket(n))));
 assert.ok(joined.every(r=>r.statusCode===200&&r.json().status==="active"));
 assert.equal((await enroll(names[0],keys[0],ticket(names[0]))).statusCode,200);
 assert.equal((await enroll(names[0],key(),ticket(names[0]))).statusCode,409);
 const state=(await desk("live")).json();assert.equal(state.agents.length,76);assert.equal(state.pending.length,0);assert.equal(state.roster.length,0);
 const watch=a.inject({url:"/api/agent/watch?after=0",headers:auth(keys[1])});
 const send=await post("/api/agent/send",{to:names[1],subject:"Fleet ready",body:"hello",key:randomUUID()},auth(keys[0]));assert.equal(send.statusCode,200);
 assert.ok((await watch).json().cursor>0);
 assert.equal((await desk("live")).json().mail[0][0],"Fleet ready");
 await post("/api/desk/revoke",{name:names[0]},owner);
 assert.equal((await a.inject({url:"/api/agent/inbox",headers:auth(keys[0])})).statusCode,401);
 const extra=await Promise.all(["extra-one","extra-two"].map(n=>enroll(n,key())));
 const refs=extra.map(r=>({name:r.json().name,code:r.json().code}));
 assert.equal((await post("/api/desk/batch",{requests:refs,approve:true},owner)).json().updated,2);
 }finally{await a.close();rmSync(dir,{recursive:true,force:true})}
});
