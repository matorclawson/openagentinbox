"use client";
import { useEffect, useState } from "react";
import { Activity, ArrowUpRight, Mail, Radio, UserRoundPlus, Users } from "lucide-react";

export type Live = {
  cursor: string;
  peerLimit: number;
  watching: Record<string,boolean>;
  pending: [string,string,number,string][];
  agents: [string,number][];
  activity: [string,string,number][];
  mail: [string,string,string,string,string][];
  roster: [string,number,number,string,string][];
};
export function useWorkspace(owner?: string|null) {
  const [data,setData]=useState<Live|null>(null);
  const [connection,setConnection]=useState("Connecting");
  useEffect(()=>{
    setData(null); setConnection("Connecting");
    if (!owner) return;
    const abort=new AbortController();
    async function listen(){
      let cursor="",delay=800,expiry=Infinity;
      while (!abort.signal.aborted) {
        try {
          if(Date.now()>=expiry)cursor="";
          const r=await fetch("/api/desk/live?compact=1&after="+encodeURIComponent(cursor),{signal:abort.signal,cache:"no-store"});
          if(r.status===401){setData(null);setConnection("Session ended");return}
          if(!r.ok) throw Error("disconnected");
          const snapshot:Live & {unchanged?:boolean}=await r.json();
          if(abort.signal.aborted)return;
          cursor=snapshot.cursor;if(!snapshot.unchanged){setData(snapshot);expiry=Math.min(Infinity,...snapshot.pending.map(v=>v[2]),...snapshot.roster.map(v=>v[2]))}setConnection("Live");delay=800;
        }catch{if(abort.signal.aborted)return;setConnection("Reconnecting");delay=Math.min(15000,delay*2)}
        await new Promise(resolve=>setTimeout(resolve,delay));
      }
    }
    void listen();return ()=>abort.abort();
  },[owner]);
  return {data,connection};
}
export function Workspace({data,connection,review,openBox}:{data:Live|null;connection:string;review:()=>void;openBox:(name:string)=>void}){
 const active=data?.agents.filter(([,on])=>on).length;
 const feed=[
   ...(data?.activity??[]).map(([kind,name,at])=>({title:kind,detail:name,at,mail:false,box:""})),
   ...(data?.mail??[]).map(([subject,src,dst,at])=>({title:subject,detail:src+" → "+dst,at:Date.parse(at),mail:true,box:dst}))
 ].sort((a,b)=>b.at-a.at).slice(0,10);
 return <section className="overview" aria-label="Workspace overview">
   <div className="overview-head"><div><small>OPENAGENTINBOX</small><h1>Workspace activity</h1></div><span className={'connection-state '+(connection==="Live"?"ready":"")} role="status"><Radio size={15}/>{connection}</span></div>
   <div className="metrics">
    <div><Users/><span>Enrolled agents</span><strong>{active??"—"}</strong></div>
    <button onClick={review}><UserRoundPlus/><span>Awaiting approval</span><strong>{data?.pending.length??"—"}</strong><ArrowUpRight size={14}/></button>
    <div><Activity/><span>Listening clients</span><strong>{data?Object.keys(data.watching).length:"—"}</strong></div>
   </div>
   <details className="stream" open><summary>Recent activity</summary><ol>{feed.slice(0,5).map((v,i)=><li key={v.at+"-"+i}>{v.mail?<Mail size={16}/>:<Users size={16}/>}<div>{v.mail?<button onClick={()=>openBox(v.box)}>{v.title}</button>:<b>{v.title}</b>}<small>{v.detail}</small></div><time title={new Date(v.at).toLocaleString()}>{new Date(v.at).toLocaleTimeString([], {hour:"2-digit",minute:"2-digit"})}</time></li>)}</ol>{!feed.length&&<p>{data?"Activity will appear when someone joins or sends mail.":"Connecting to your workspace…"}</p>}</details>
 </section>
}

async function post(path:string,body:object){
 const r=await fetch("/api/desk/"+path,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(body)});
 const v=await r.json();if(!r.ok)throw Error(v.error||"Request failed");return v;
}
export function EnrollmentQueue({data}:{data:Live}){
 const [review,setReview]=useState<Live["pending"]|null>(null),[host,setHost]=useState(""),[ip,setIP]=useState(""),[names,setNames]=useState(""),[secret,setSecret]=useState(""),[msg,setMsg]=useState(""),[busy,setBusy]=useState(false);
 async function action(fn:()=>Promise<void>){setBusy(true);setMsg("");try{await fn()}catch(e){setMsg((e as Error).message)}finally{setBusy(false)}}
 const queue=review??data.pending;
 return <div className="queue"><section className="card detail">
 <h2>Enrollment queue <small>{queue.length}</small></h2>
 <p>Match the names and codes with the requests from your agents.</p>
 {queue.length>0&&<ul className="requests">{queue.map(([n,c,expires,peer])=><li key={c}><strong>{n}</strong><small>{peer}</small><code>{c}</code>{!review&&<button onClick={()=>setReview([[n,c,expires,peer]])}>Review</button>}</li>)}</ul>}
 {!queue.length&&<p>No requests waiting.</p>}
 {review?<div className="actions">{[true,false].map(approve=><button key={String(approve)} disabled={busy} onClick={()=>void action(async()=>{await post("batch",{approve,requests:review.map(([name,code])=>({name,code}))});setReview(null);setMsg(approve?"Access granted.":"Requests dismissed.")})}>{approve?"Approve":"Dismiss"} {review.length}</button>)}<button onClick={()=>setReview(null)}>Cancel</button></div>:queue.length>0&&<button onClick={()=>setReview([...queue])}>Review all {queue.length} requests</button>}
 </section><details className="card detail"><summary>Onboard a host with multiple agents</summary>
 <p>Authorize an exact roster for 24 hours. Each agent receives a single-use ticket for its own name. This works even when the ordinary queue is full.</p>
 <form onSubmit={e=>{e.preventDefault();void action(async()=>{const roster=names.trim().split(/[\s,]+/);const result=await post("roster",{host,ip,names:roster});setSecret(result.secret);setNames("")})}}>
 <label>Host name<input value={host} onChange={e=>setHost(e.target.value)} required placeholder="mac-mini"/></label>
 <label>Source IP address<input value={ip} onChange={e=>setIP(e.target.value.trim())} required placeholder="100.64.0.12"/></label><p>Run <code>openagentinbox-client host-info INBOX_URL</code> on the agent host and enter both values.</p>
 <label>Agent names<textarea value={names} onChange={e=>setNames(e.target.value)} required placeholder="architect, openpassword, openagentinbox"/></label>
 <button disabled={busy}>Authorize roster</button></form>
 {secret&&<label>Host grant · copy once to the trusted host operator<input readOnly value={secret} onFocus={e=>e.target.select()}/><button onClick={()=>setSecret("")}>Hide grant</button></label>}
 <a href="/host-guide.md">Host client setup instructions</a>
 {data.roster.map(([h,count,expires,id,ip])=><p key={id}>{h} · {ip} · {count} remaining · expires {new Date(expires).toLocaleString()} <button disabled={busy} onClick={()=>void action(async()=>{await post("roster/cancel",{id});setSecret("");setMsg("Unclaimed tickets canceled.")})}>Cancel remaining tickets</button></p>)}
 </details>{msg&&<p role="status">{msg}</p>}</div>
}
