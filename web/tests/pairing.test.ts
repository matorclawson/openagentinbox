import test from "node:test";
import assert from "node:assert/strict";
import {createHmac,randomBytes} from "node:crypto";
import {mkdtempSync,readFileSync,rmSync} from "node:fs";
import {DatabaseSync} from "node:sqlite";
import {tmpdir} from "node:os";
import {join} from "node:path";
import {service} from "./daemon.ts";

test("owner-issued code pairs exactly one CLI and revocation removes its outstanding tickets",async()=>{
 const dir=mkdtempSync(join(tmpdir(),"inbox-pair-")),origin="https://inbox.example";
 const a=await service({dir,origin});
 const post=(url:string,payload:object,headers:Record<string,string>={})=>a.inject({method:"POST",url,payload,headers:{origin,...headers}});
 const key=()=>randomBytes(32).toString("base64url");
 const auth=(token:string)=>({authorization:"Bearer "+token});
 try{
 const setup=await post("/api/setup",{username:"owner",password:"test-only-password",code:readFileSync(join(dir,"setup-code"),"utf8")});
 const owner={cookie:setup.headers["set-cookie"].split(";")[0]};
 assert.equal((await post("/api/desk/pair",{})).statusCode,401);
 const {code,id}=(await post("/api/desk/pair",{names:["seat-one"]},owner)).json();
 const sessionKeys=[key(),key()];
 const exchanges=await Promise.all(sessionKeys.map(cred=>post("/api/agent/pair",{label:"Mac mini",code},auth(cred))));
 assert.deepEqual(exchanges.map(r=>r.statusCode).sort(),[200,403]);
 const cred=sessionKeys[exchanges.findIndex(r=>r.statusCode===200)];
 const snap=await a.inject({url:"/api/desk/live",headers:owner});
 assert.ok(!snap.body.includes(code)&&!snap.body.includes(cred));
 assert.equal((await a.inject({url:"/api/agent/inbox",headers:auth(cred)})).statusCode,401);
 assert.equal((await post("/api/desk/pair/roster",{id,names:["seat-two"]},owner)).statusCode,200);
 const tickets=await post("/api/agent/pair/tickets",{},auth(cred));
 assert.deepEqual(tickets.json().names,["seat-one","seat-two"]);
 const ticket=(name:string)=>createHmac("sha256",cred).update(name).digest("base64url");
 assert.equal((await post("/api/agent/enroll",{name:"seat-one",ticket:ticket("seat-one")},auth(key()))).json().status,"active");
 assert.equal((await post("/api/desk/pair/revoke",{id},owner)).statusCode,200);
 assert.equal((await post("/api/agent/pair/tickets",{},auth(cred))).statusCode,401);
 assert.equal((await post("/api/agent/enroll",{name:"seat-two",ticket:ticket("seat-two")},auth(key()))).statusCode,403);
 const second=(await post("/api/desk/pair",{names:[]},owner)).json();
 const db=new DatabaseSync(join(dir,"console.sqlite"));try{db.prepare("UPDATE paired_cli SET deadline=0 WHERE id=?").run(second.id)}finally{db.close()}
 assert.equal((await post("/api/agent/pair",{code:second.code,label:"late"},auth(key()))).statusCode,403);
 }finally{await a.close();rmSync(dir,{recursive:true,force:true})}
});
