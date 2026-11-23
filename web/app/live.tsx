"use client";
import {OnboardingStatus,type Receipt} from "./onboarding-status";
import { useEffect, useState } from "react";
import { Activity, ArrowUpRight, Mail, Radio, UserRoundPlus, Users } from "lucide-react";

export type Live = {
  probes?:[string,number,number,number,string][];
  overdue?:[string,string,number,string][];
  listeners?:[string,number,string][];
  cursor: string;
  peerLimit: number;
  watching: Record<string,boolean>;
  pending: [string,string,number,string][];
  agents: [string,number,string][];
  activity: [string,string,number][];
  mail: [string,string,string,string,string][];
  onboarding: Receipt[];
  clients: [string,string,number,number,number][];
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
          cursor=snapshot.cursor;if(!snapshot.unchanged){setData(snapshot);expiry=Math.min(Infinity,...snapshot.pending.map(v=>v[2]),...snapshot.roster.map(v=>v[2]),...snapshot.clients.filter(v=>!v[3]).map(v=>v[2]))}setConnection("Live");delay=800;
        }catch{if(abort.signal.aborted)return;setConnection("Reconnecting");delay=Math.min(15000,delay*2)}
        await new Promise(resolve=>setTimeout(resolve,delay));
      }
    }
    void listen();return ()=>abort.abort();
  },[owner]);
  return {data,connection};
}
type Sample = {count:number;p95_upper_ms:number};
type PerformanceSnapshot = {successful_sends:Sample;successful_body_reads:Sample;estimated_recipient_mailboxes:number};
function MailPerformance(){
 const [snapshot,setSnapshot]=useState<PerformanceSnapshot|null>(null);
 const [status,setStatus]=useState("");
 async function refresh(){
   setStatus("Loading…");
   try{
     const response=await fetch("/api/desk/performance",{cache:"no-store"});
     if(!response.ok)throw Error();
     setSnapshot(await response.json());setStatus("");
   }catch{setStatus("Could not load performance data.")}
 }
 return <details className="stream" onToggle={event=>{if(event.currentTarget.open)void refresh()}}>
   <summary>Mail performance since restart</summary>
   {snapshot&&<><p>Estimated distinct recipient inboxes · {Math.round(snapshot.estimated_recipient_mailboxes)}</p>
   <table><thead><tr><th>Successful operations</th><th>Count</th><th>p95 upper bound</th></tr></thead>
   <tbody>{([["Send",snapshot.successful_sends],["Open message",snapshot.successful_body_reads]] as const).map(([name,v])=><tr key={name}><th scope="row">{name}</th><td>{v.count}</td><td>{v.count?v.p95_upper_ms.toFixed(2)+" ms":"—"}</td></tr>)}</tbody></table></>}
   {status&&<p role="status">{status}</p>}
   <button disabled={status==="Loading…"} onClick={()=>void refresh()}>Refresh</button>
 </details>
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
   <MailPerformance/>
   <section className="stream"><h2>Delivery health</h2>
   {data?.probes?.length?data.probes.map(([host,last,incident,count,id])=><p key={id}><strong>{host}</strong> · {incident?"Delivery check overdue":last?"Delivery verified":"Awaiting first check"} · {count} verified seats{last?" · Last check "+new Date(last).toLocaleTimeString():""}</p>):<p>No paired hosts reporting yet.</p>}
   <details><summary>Seat listener checks</summary>{data?.listeners?.map(([seat,at,mode])=><p key={seat}>{seat} · {!at?"No listener check recorded":Date.now()-at>180000?"Check overdue":mode} · {at?new Date(at).toLocaleTimeString():"—"}</p>)}</details>
   <h2>Overdue requests</h2>{data?.overdue?.length?data.overdue.map(([id,seat,due,state])=><p key={id}><a href={"/messages/"+encodeURIComponent(id)}>{seat} · {state}</a> · Due {new Date(due).toLocaleString()}</p>):<p>No overdue requests.</p>}</section>
   <details className="stream" open><summary>Recent activity</summary><ol>{feed.slice(0,5).map((v,i)=><li key={v.at+"-"+i}>{v.mail?<Mail size={16}/>:<Users size={16}/>}<div>{v.mail?<button onClick={()=>openBox(v.box)}>{v.title}</button>:<b>{v.title}</b>}<small>{v.detail}</small></div><time title={new Date(v.at).toLocaleString()}>{new Date(v.at).toLocaleTimeString([], {hour:"2-digit",minute:"2-digit"})}</time></li>)}</ol>{!feed.length&&<p>{data?"Activity will appear when someone joins or sends mail.":"Connecting to your workspace…"}</p>}</details>
 </section>
}

async function post(path:string,body:object){
 const r=await fetch("/api/desk/"+path,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(body)});
 const v=await r.json();if(!r.ok)throw Error(v.error||"Request failed");return v;
}
export function EnrollmentQueue({data}:{data:Live}){
 const [review,setReview]=useState<Live["pending"]|null>(null),[msg,setMsg]=useState(""),[busy,setBusy]=useState(false);
 async function action(fn:()=>Promise<void>){setBusy(true);setMsg("");try{await fn()}catch(e){setMsg((e as Error).message)}finally{setBusy(false)}}
 const queue=review??data.pending;
 return <div className="queue">{data.onboarding?.length>0&&<OnboardingStatus rows={data.onboarding}/>}<section className="card detail">
 <h2>Enrollment queue <small>{queue.length}</small></h2>
 <p>Match the names and codes with the requests from your agents.</p>
 {queue.length>0&&<ul className="requests">{queue.map(([n,c,expires,peer])=><li key={c}><strong>{n}</strong><small>{peer}</small><code>{c}</code>{!review&&<button onClick={()=>setReview([[n,c,expires,peer]])}>Review</button>}</li>)}</ul>}
 {!queue.length&&<p>No requests waiting.</p>}
 {review?<div className="actions">{[true,false].map(approve=><button key={String(approve)} disabled={busy} onClick={()=>void action(async()=>{await post("batch",{approve,requests:review.map(([name,code])=>({name,code}))});setReview(null);setMsg(approve?"Access granted.":"Requests dismissed.")})}>{approve?"Approve":"Dismiss"} {review.length}</button>)}<button onClick={()=>setReview(null)}>Cancel</button></div>:queue.length>0&&<button onClick={()=>setReview([...queue])}>Review all {queue.length} requests</button>}
 </section><ClientPairing data={data}/>
 {data.roster.map(([label,count,expires,id])=><p key={id}>Earlier roster · {label} · {count} remaining · expires {new Date(expires).toLocaleString()} <button disabled={busy} onClick={()=>void action(async()=>{await post("roster/cancel",{id});setMsg("Unclaimed tickets canceled.")})}>Cancel remaining tickets</button></p>)}
 {msg&&<p role="status">{msg}</p>}</div>
}

function ClientPairing({data}:{data:Live}){
 const [pair,setPair]=useState<{id:string;code:string;expires:number}|null>(null);
 const [names,setNames]=useState(""),[target,setTarget]=useState(""),[message,setMessage]=useState(""),[busy,setBusy]=useState(false);
 const paired=data.clients.filter(v=>v[3]>0);
 useEffect(()=>{
   if(!pair)return;
   if(data.clients.some(v=>v[0]===pair.id&&v[3])){setPair(null);setMessage("CLI paired.");return}
   const timer=setTimeout(()=>{setPair(null);setMessage("Pairing token expired.")},Math.max(0,pair.expires-Date.now()));
   return ()=>clearTimeout(timer);
 },[pair,data.clients]);
 async function act(fn:()=>Promise<void>){setBusy(true);setMessage("");try{await fn()}catch(e){setMessage((e as Error).message)}finally{setBusy(false)}}
 return <section className="card detail">
 <h2>Pair a CLI</h2><p>Connect a client to this workspace. You choose which agent names it can provision.</p>
 <form onSubmit={e=>{e.preventDefault();void act(async()=>{
  const roster=names.trim()?names.trim().split(/[\s,]+/):[];
  if(target){await post("onboarding",{id:target,names:roster});setMessage("Onboarding authorized. The host worker can now enroll new seats and verify existing profiles.")}
  else{setPair(await post("pair",{names:roster}))}
  setNames("");
 })}}>
 {paired.length>0&&<label>Client<select aria-label="Paired client" value={target} onChange={e=>setTarget(e.target.value)}><option value="">Pair a new CLI</option>{paired.map(([id,label])=><option key={id} value={id}>{label} · {id.slice(-6)}</option>)}</select></label>}
 <label>Agent names {target?"":"(optional)"}<textarea required={Boolean(target)} value={names} onChange={e=>setNames(e.target.value)} placeholder="architect, openpassword, researcher"/></label>
 <button className="primary" disabled={busy||(!target&&Boolean(pair))}>{target?"Authorize onboarding":"Generate pairing token"}</button>
 </form>
 {pair&&<div className="pair-code"><strong>Paste this token at the CLI prompt</strong>
 <code>openagentinbox-client login {window.location.origin}</code>
 <label>Pairing token<input readOnly value={pair.code} onFocus={e=>e.target.select()}/></label>
 <button onClick={()=>void act(async()=>{await navigator.clipboard.writeText(pair.code);setMessage("Token copied.")})}>Copy token</button>
 <small>Single use · expires at {new Date(pair.expires).toLocaleTimeString()}</small></div>}
 {target&&<p>Authorize this paired host to enroll missing inboxes and verify existing seat profiles. Start its worker once under the account that owns those profiles: <code>openagentinbox-client onboard {window.location.origin} --install-worker</code></p>}
 {data.clients.map(([id,label,expires,at,remaining])=><div className="agentrow" key={id}><strong>{at?label:"Awaiting CLI login"}</strong><small>{at?"Paired · "+id.slice(-6)+" · "+remaining+" unclaimed":"Expires "+new Date(expires).toLocaleTimeString()}</small><button disabled={busy} onClick={()=>void act(async()=>{await post("pair/revoke",{id});if(pair?.id===id)setPair(null);if(target===id)setTarget("");setMessage(at?"CLI revoked.":"Pairing canceled.")})}>{at?"Revoke CLI":"Cancel pairing"}</button></div>)}
 {message&&<p role="status">{message}</p>}<a href="/host-guide.md">Install the CLI and onboard a fleet</a>
 </section>
}
