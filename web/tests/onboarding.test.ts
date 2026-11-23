import test from "node:test";
import assert from "node:assert/strict";
import {spawn,spawnSync} from "node:child_process";
import {mkdtempSync,readFileSync,writeFileSync,mkdirSync,existsSync,rmSync,readdirSync} from "node:fs";
import {tmpdir} from "node:os";
import {join,resolve} from "node:path";
import {service} from "./daemon.ts";

test("paired host reconciles a mixed 73-seat roster using direct owner authority",async()=>{
 const dir=mkdtempSync(join(tmpdir(),"inbox-onboard-")),origin="https://inbox.example";
 const a=await service({dir,origin});
 const cli=resolve("../dist/ui/clients/"+process.platform+"-"+(process.arch==="x64"?"amd64":process.arch));
 const run=(args:string[],input?:string)=>spawnSync(cli,args,{env:{...process.env,HOME:dir},input,encoding:"utf8"});
 const post=(url:string,payload:object,headers:Record<string,string>={})=>a.inject({method:"POST",url,payload,headers:{origin,...headers}});
 try{
  const setup=await post("/api/setup",{username:"Rod",password:"temporary-test-password",code:readFileSync(join(dir,"setup-code"),"utf8")});
  const owner={cookie:setup.headers["set-cookie"].split(";")[0]};
  const names=Array.from({length:73},(_,i)=>"seat-"+i);
  const profiles=join(dir,".config/openagentinbox/seats");mkdirSync(profiles,{recursive:true});
  const store=(name:string,token:string)=>writeFileSync(join(profiles,name+".json"),JSON.stringify({name,token,origin:a.base}),{mode:0o600});
  const existing=(await post("/api/desk/agents",{name:names[0]},owner)).json().credential;
  store(names[0],existing);
  const missing=(await post("/api/desk/agents",{name:names[1]},owner)).json().credential;
  const pair=(await post("/api/desk/pair",{names:[]},owner)).json();
  assert.equal(run(["login",a.base],pair.code+"\n").status,0);
  if(process.platform==="linux"&&process.getuid?.()!==0){
    const bin=join(dir,"bin");mkdirSync(bin);
    writeFileSync(join(bin,"systemctl"),'#!/bin/sh\nexit 0\n',{mode:0o700});
    for(let i=0;i<2;i++){
      const install=spawnSync(cli,["onboard",a.base,"--install-worker"],{env:{...process.env,HOME:dir,PATH:bin+":"+process.env.PATH},encoding:"utf8"});
      assert.equal(install.status,0,install.stderr);
    }
    const units=join(dir,".config/systemd/user"),files=readdirSync(units);
    assert.equal(files.length,1);assert.ok(readFileSync(join(units,files[0]),"utf8").includes('onboard "'+a.base+'" --watch'));
  }
  const watcher=spawn(cli,["onboard",a.base,"--watch"],{env:{...process.env,HOME:dir},stdio:"ignore"});
  try{
    let confirmed=false;
    for(let i=0;i<90;i++){
      const live=(await a.inject({url:"/api/desk/live",headers:owner})).json();
      if(live.probes?.some((p:any[])=>p[4]===pair.id&&p[1]>0&&p[2]===0)){confirmed=true;break}
      await new Promise(r=>setTimeout(r,200));
    }
    assert.ok(confirmed,"native host must fetch and confirm a probe without a harness");
    assert.equal((await a.inject({url:"/api/desk/list",headers:owner})).json().rows.length,0);
  }finally{if(watcher.exitCode===null&&watcher.signalCode===null){const stopped=new Promise(r=>watcher.once("exit",r));watcher.kill();await stopped}}
  const auth={authorization:"Bearer "+existing};
  assert.equal((await post("/api/desk/onboarding",{id:pair.id,names},auth)).statusCode,401);
  assert.equal((await post("/api/desk/onboarding",{id:pair.id,names},owner)).json().authorized,73);
  const first=run(["onboard",a.base]);assert.equal(first.status,1,first.stderr);
  const results=first.stdout.trim().split("\n").map(s=>JSON.parse(s));
  assert.equal(results.filter(r=>r.status==="verified").length,72,JSON.stringify(results.slice(0,4)));
  assert.equal(results.find(r=>r.name===names[1]).status,"profile-missing");
  assert.equal(existsSync(join(profiles,names[1]+".json")),false);
  assert.equal(existsSync(join(profiles,"../agent.json")),false);
  assert.equal(JSON.parse(readFileSync(join(profiles,names[0]+".json"),"utf8")).token,existing);
  store(names[1],missing);
  const retry=run(["onboard",a.base]);assert.equal(retry.status,0,retry.stderr);
  assert.equal(JSON.parse(retry.stdout).name,names[1]);
  assert.equal(run(["onboard",a.base]).stdout,"");
  const proof=JSON.parse(run(["--seat",names[0],"authorization"]).stdout);
  assert.equal(proof.authorized_by,"Rod");assert.ok(proof.verified_at>=proof.issued_at);
  const state=await a.inject({url:"/api/desk/live",headers:owner});
  assert.equal(state.json().onboarding.filter((r:unknown[])=>r[3]).length,73);
  assert.ok(!state.body.includes(existing)&&!first.stdout.includes(existing));
  await post("/api/desk/pair/revoke",{id:pair.id},owner);
  assert.equal(run(["onboard",a.base]).status,1);
  assert.equal(run(["--seat",names[0],"whoami"]).status,0);
 }finally{await a.close();rmSync(dir,{recursive:true,force:true})}
});
