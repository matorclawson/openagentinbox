import test from "node:test";
import assert from "node:assert/strict";
import {mkdtempSync,readFileSync,rmSync} from "node:fs";
import {tmpdir} from "node:os";
import {join} from "node:path";
import {randomUUID} from "node:crypto";
import {service} from "./daemon.ts";

test("reviewed list revokes 50 old credentials together and preserves history and replacements",async()=>{
 const dir=mkdtempSync(join(tmpdir(),"revoke-")),origin="https://inbox.example",a=await service({dir,origin});
 try{
  const post=(path:string,payload:object,cookie="")=>a.inject({method:"POST",url:"/api/"+path,payload,headers:{origin,cookie}});
  const setup=await post("setup",{username:"owner",password:"temporary-test-password",code:readFileSync(join(dir,"setup-code"),"utf8")});
  const cookie=setup.headers["set-cookie"].split(";")[0];
  const names=Array.from({length:50},(_,i)=>"old-"+i);
  const tokens:string[]=[];
  for(const name of [...names,"new-seat"])tokens.push((await post("desk/agents",{name},cookie)).json().credential);
  const authorized=(t:string)=>a.inject({url:"/api/agent/whoami",headers:{authorization:"Bearer "+t}});
  const batch=(list:string[],preview="")=>post("desk/revoke-batch",{names:list,preview},cookie);
  const message=(await post("desk/send",{to:names[0],subject:"preserve",body:"original",key:randomUUID()},cookie)).json().id;
  assert.equal((await post("desk/revoke-batch",{names})).statusCode,401);
  assert.equal((await batch([names[0],names[0]])).statusCode,400);
  assert.equal((await batch(["operator"])).statusCode,400);
  await post("desk/addresses",{moves:[{from:"new-seat",to:"current-seat"}],apply:true},cookie);
  const invalid=(await batch([...names,"missing","new-seat"])).json();
  assert.equal(invalid.conflicts,2);
  assert.equal((await batch([...names,"missing","new-seat"],invalid.preview)).statusCode,409);
  const first=(await batch(names)).json();
  assert.equal(first.count,50);assert.equal(first.applied,false);
  tokens[49]=(await post("desk/rotate",{name:names[49]},cookie)).json().credential;
  assert.equal((await batch(names,first.preview)).statusCode,409);
  for(const t of tokens)assert.equal((await authorized(t)).statusCode,200);
  const review=(await batch(names)).json();
  assert.equal((await batch(names,review.preview)).json().count,50);
  for(const t of tokens.slice(0,50))assert.equal((await authorized(t)).statusCode,401);
  assert.equal((await authorized(tokens[50])).json().name,"current-seat");
  assert.equal((await batch(names)).json().count,0);
  assert.equal((await a.inject({url:"/api/desk/message/"+message,headers:{cookie}})).json().body,"original");
 }finally{await a.close();rmSync(dir,{recursive:true,force:true})}
});
