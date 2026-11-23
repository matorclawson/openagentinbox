import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { randomUUID } from "node:crypto";
import { spawnSync } from "node:child_process";
import { service } from "./daemon.ts";

test("mailbox identity survives rename, collision, credential handoff and restart", async () => {
  const dir=mkdtempSync(join(tmpdir(),"inbox-addresses-"));
  const origin="https://inbox.example";
  let instance=await service({dir,origin});
  try {
    const post=(path:string,payload:object,headers:Record<string,string>={})=>
      instance.inject({method:"POST",url:"/api/"+path,payload,headers:{origin,...headers}});
    const auth=(token:string)=>({authorization:"Bearer "+token});
    const setup=await post("setup",{username:"owner",password:"temporary-test-password",code:readFileSync(join(dir,"setup-code"),"utf8")});
    const owner={cookie:String(setup.headers["set-cookie"]).split(";")[0]};
    const create=async(name:string)=>(await post("desk/agents",{name},owner)).json().credential;
    const original=await create("support");
    await create("existing");
    const who=async(token:string)=>instance.inject({url:"/api/agent/whoami",headers:auth(token)});
    const id=(await who(original)).json().mailbox_id;
    assert.match(id,/^[0-9a-f]{32}$/);
    const move={from:"support",to:"support-maintainer"};
    const reconcile=(moves:object[],apply:boolean)=>post("desk/addresses",{moves,apply},owner);
    assert.equal((await post("desk/addresses",{moves:[move],apply:true},auth(original))).statusCode,401);
    const letter={subject:"work order",body:"preserve the original",key:randomUUID(),to:"support"};
    const sent=(await post("desk/send",letter,owner)).json().id;
    assert.ok(sent);
    assert.equal((await reconcile([move],false)).json().applied,false);
    assert.equal((await who(original)).json().name,"support");
    assert.equal((await reconcile([move,{from:"existing",to:"support"}],true)).json().conflicts,1);
    assert.equal((await who(original)).json().name,"support");
    assert.equal((await reconcile([move],true)).json().applied,true);
    assert.equal((await reconcile([move],true)).json().rows[0].status,"already current");
    assert.deepEqual((await who(original)).json(),{name:move.to,original_address:"support",mailbox_id:id});
    assert.equal((await post("desk/send",{...letter,to:move.to},owner)).json().id,sent);
    assert.equal((await post("desk/agents",{name:move.to},owner)).statusCode,409);
    for(const box of ["support",move.to]){
      assert.equal((await instance.inject({url:"/api/desk/list?box="+box,headers:owner})).json().rows[0].id,sent);
    }
    const newToken=(await post("desk/rotate",{name:move.to},owner)).json().credential;
    assert.equal((await who(original)).statusCode,401);
    const cli=resolve("../dist/ui/clients/"+process.platform+"-"+(process.arch==="x64"?"amd64":process.arch));
    const call=(args:string[],input?:string)=>spawnSync(cli,["--seat",move.to,...args],{env:{...process.env,HOME:dir},encoding:"utf8",input});
    assert.equal(call(["bind",instance.base,"--token-stdin"],newToken).status,0);
    assert.equal(call(["verify",instance.base,move.to,id]).status,0);
    assert.equal(call(["verify",instance.base,move.to,"wrong-id"]).status,1);
    assert.equal(call(["verify","https://other.example",move.to,id]).status,1);
    assert.equal(JSON.parse(call(["read",sent]).stdout).id,sent);
    const later={from:move.to,to:"support-lead"};
    assert.equal((await reconcile([later],true)).json().applied,true);
    assert.equal(call(["use-name",instance.base,later.to,id]).status,0);
    assert.equal(call(["use-name",instance.base,later.to,id]).status,0);
    const fromCurrent=spawnSync(cli,["--seat",later.to,"verify",instance.base,later.to,id],{env:{...process.env,HOME:dir},encoding:"utf8"});
    assert.equal(fromCurrent.status,0);
    await instance.close();
    instance=await service({dir,origin});
    assert.equal((await who(newToken)).json().mailbox_id,id);
    assert.equal((await who(newToken)).json().name,later.to);
    assert.equal((await post("desk/send",{...letter,key:randomUUID()},owner)).statusCode,200);
  } finally {await instance.close();rmSync(dir,{recursive:true,force:true})}
});
