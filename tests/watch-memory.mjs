// Linux. Usage: node tests/watch-memory.mjs DAEMON UI [agents=76] [seconds=60] [messages/min=0]
import {spawn} from 'node:child_process';
import {mkdtempSync,readFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {once} from 'node:events';
import {randomUUID} from 'node:crypto';
import {setTimeout as sleep} from 'node:timers/promises';
import {createServer} from 'node:net';
const [binary,ui,count='76',seconds='60',rate='0']=process.argv.slice(2);
const n=Number(count),duration=Number(seconds),rpm=Number(rate);
if(!Number.isInteger(n)||n<1||n>512||!(duration>=5)||!Number.isFinite(rpm)||rpm<0||rpm>10000)throw Error('invalid load');
const socket=createServer().listen(0,'127.0.0.1');await once(socket,'listening');
const port=socket.address().port;await new Promise(r=>socket.close(r));
const dir=mkdtempSync(join(tmpdir(),'watch-memory-')),origin='http://127.0.0.1:'+port;
const proc=spawn(resolve(binary),['serve'],{env:{...process.env,GOMAXPROCS:'2',INBOX_PORT:String(port),INBOX_DATA:dir,INBOX_UI:resolve(ui)},stdio:'ignore'});
const done=once(proc,'exit'),stop=new AbortController();
let cookie,errors=0,peakListening=0,workspaceBytes=0,workspaceReplies=0,renewals=0,sends=0,wakes=0;
async function api(path,body,token,signal){
 const r=await fetch(origin+'/api/'+path,{signal,method:body?'POST':'GET',headers:{origin,...(cookie&&!token?{cookie}:{}),...(token?{authorization:'Bearer '+token}:{}),...(body?{'content-type':'application/json'}:{})},body:body?JSON.stringify(body):undefined});
 if(!r.ok)throw Error(path+' '+r.status);
 if(r.headers.has('set-cookie'))cookie=r.headers.get('set-cookie').split(';')[0];
 const raw=await r.text();if(path.startsWith('desk/live')){workspaceBytes+=Buffer.byteLength(raw);workspaceReplies++}
 return JSON.parse(raw)
}
function rss(){return Number(readFileSync('/proc/'+proc.pid+'/status','utf8').match(/^VmRSS:\s+(\d+)/m)[1])}
try{
 for(let i=0;;i++){try{await api('health');break}catch(e){if(i>100)throw e;await sleep(50)}}
 await api('setup',{username:'owner',password:'disposable-password',code:readFileSync(join(dir,'setup-code'),'utf8')});
 const keys=[];for(let i=0;i<n;i++)keys.push((await api('desk/agents',{name:'seat-'+i})).credential);
 const baseline=rss(), samples=[];
 const ticks=()=>{const f=readFileSync('/proc/'+proc.pid+'/stat','utf8').split(') ')[1].split(' ');return Number(f[11])+Number(f[12])};
 const startTicks=ticks();
 const tasks=keys.map(async token=>{let cursor=0;while(!stop.signal.aborted){try{const v=await api('agent/watch?wait=50&after='+cursor,null,token,stop.signal);if(v.cursor>cursor){cursor=v.cursor;wakes++}else renewals++}catch(e){if(!stop.signal.aborted)errors++;break}}});
 tasks.push((async()=>{let cursor='';while(!stop.signal.aborted){try{const v=await api('desk/live?compact=1&after='+cursor,null,null,stop.signal);cursor=v.cursor;if(v.watching)peakListening=Math.max(peakListening,Object.keys(v.watching).length);await sleep(800)}catch(e){if(!stop.signal.aborted)errors++;break}}})());
 if(rpm>0)tasks.push((async()=>{
 const start=performance.now();
 for(let i=0;i<Math.floor(duration*rpm/60);i++){await sleep(Math.max(0,start+i*60000/rpm-performance.now()));try{await api('agent/send',{to:'seat-'+(i%n),subject:'Load check',body:'x'.repeat(512),key:randomUUID()},keys[(i+1)%n]);sends++}catch{errors++;break}}
 })());
 for(let i=0;i<duration;i++){await sleep(1000);samples.push(rss())}
 stop.abort();await Promise.all(tasks);await sleep(300);
 console.log(JSON.stringify({agents:n,seconds:duration,baselineKiB:baseline,medianKiB:samples.toSorted((a,b)=>a-b)[Math.floor(samples.length/2)],maxSampleKiB:Math.max(...samples),peakListening,renewals,cpuTicks:ticks()-startTicks,workspaceBytes,workspaceReplies,sends,wakes,errors}));
 if(errors||peakListening!==n)process.exitCode=1;
}finally{stop.abort();proc.kill();await done;rmSync(dir,{recursive:true,force:true})}
