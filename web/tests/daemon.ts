import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { resolve } from 'node:path';

type Request = {url:string;method?:string;payload?:object;headers?:Record<string,string>};
type Config = {dir:string;origin:string;op?:{origin:string;app:string;audience:string}};
export async function service(cfg:Config){
 const port=String(30000+Math.floor(Math.random()*25000)),base='http://127.0.0.1:'+port;
 const child=spawn(process.env.INBOX_DAEMON||resolve('../dist/'+process.platform+'-'+(process.arch==='x64'?'amd64':process.arch)+'/openagentinboxd'),['serve'],{env:{...process.env,INBOX_ORIGIN:cfg.origin,INBOX_PORT:port,INBOX_UI:resolve('out'),INBOX_DATA:cfg.dir,OPENPASSWORD_ORIGIN:cfg.op?.origin||'',OPENPASSWORD_APP:cfg.op?.app||''},stdio:'inherit'});
 async function close(){if(child.exitCode===null && child.signalCode===null){const done=once(child,'exit');child.kill();await done}}
 for(let i=0;;i++){try{if((await fetch(base+'/api/session')).ok)break}catch{}if(i>100||child.exitCode!==null){await close();throw Error('daemon failed to start')}await new Promise(r=>setTimeout(r,40))}
 return {base,close, async inject(arg:string|Request){const q=typeof arg==='string'?{url:arg}:arg;const res=await fetch(base+q.url,{method:q.method,headers:{...q.headers,...(q.payload?{'content-type':'application/json'}:{})},body:q.payload?JSON.stringify(q.payload):undefined,redirect:'manual'});const body=await res.text();return {statusCode:res.status,headers:Object.fromEntries(res.headers),body,json:()=>JSON.parse(body)}}}
}
