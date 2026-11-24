"use client";

import { useEffect, useState } from "react";
import { CheckCircle2, CircleAlert, Clock3 } from "lucide-react";

export type Receipt = [seat:string, host:string, owner:string, verified:number, expires:number, problem:string, hostId?:string];
type Status = "attention" | "waiting" | "verified";
type Filter = Status | "all";
const pageSize = 10;
const day = new Intl.DateTimeFormat("en-GB", {day:"numeric", month:"short", year:"numeric", timeZone:"UTC"});
const clock = new Intl.DateTimeFormat("en-GB", {hour:"2-digit", minute:"2-digit", second:"2-digit", timeZone:"UTC"});
function stamp(t:number){return day.format(t)+" · "+clock.format(t)+" UTC";}
function windowLabel(times:number[]){
  if(!times.length)return "";
  const start=Math.min(...times), end=Math.max(...times);
  if(start===end)return stamp(start);
  if(day.format(start)===day.format(end))return day.format(start)+" · "+clock.format(start)+"–"+clock.format(end)+" UTC";
  return stamp(start)+" – "+stamp(end);
}
function status(row:Receipt,now:number):Status{
  if(row[3])return "verified";
  return row[4]<=now||row[5]?"attention":"waiting";
}
const problemLabels:Record<string,string>={"profile-missing":"Profile missing","profile-conflict":"Profile conflict","identity-failed":"Identity check failed","enrollment-failed":"Enrollment failed","inbox-unavailable":"Inbox unavailable","confirmation-failed":"Confirmation failed","invalid-seat":"Invalid seat"};
const priority={attention:0,waiting:1,verified:2};
function ReceiptGroup({rows,now,disambiguate}:{rows:Receipt[];now:number;disambiguate:boolean}){
  const [open,setOpen]=useState<boolean|null>(null),[filter,setFilter]=useState<Filter|null>(null),[query,search]=useState(""),[page,setPage]=useState(0);
  const [,host,owner,,,,hostId]=rows[0];
  const counts={attention:0,waiting:0,verified:0};
  for(const row of rows)counts[status(row,now)]++;
  const expanded=open??counts.attention>0;
  const active=filter??(counts.attention?"attention":"all");
  const found=rows.filter(r=>(active==="all"||status(r,now)===active)&&r[0].includes(query.trim().toLowerCase()))
    .sort((a,b)=>priority[status(a,now)]-priority[status(b,now)]||a[0].localeCompare(b[0]));
  const index=Math.min(page,Math.max(0,Math.ceil(found.length/pageSize)-1));
  const verifiedTimes=rows.filter(r=>r[3]).map(r=>r[3]);
  const Icon=counts.attention?CircleAlert:counts.waiting?Clock3:CheckCircle2;
  return <article className="receipt-group">
    <div className="receipt-head"><Icon aria-hidden="true" size={24} className={counts.attention?"attention":counts.waiting?"waiting":"verified"}/>
      <div><h3>{counts.verified} of {rows.length} CLI verified</h3>
        <p>{host||"Awaiting host pairing"}{disambiguate&&hostId?" · "+hostId.slice(-8):""} · Authorized by {owner}</p>
        {verifiedTimes.length>0&&<p>Verified {windowLabel(verifiedTimes)}</p>}
      </div>
      <button type="button" aria-expanded={expanded} onClick={()=>setOpen(!expanded)}>{expanded?"Hide receipts":"Browse receipts"}</button>
    </div>
    {(counts.waiting>0||counts.attention>0)&&<p className="receipt-pending">{[counts.attention&&counts.attention+" need attention",counts.waiting&&counts.waiting+" awaiting host"].filter(Boolean).join(" · ")}</p>}
    {expanded&&<div className="receipt-browser">
      <div className="receipt-controls"><label>Find a seat<input type="search" value={query} onChange={e=>{search(e.target.value);setPage(0)}}/></label>
      <label>Status<select value={active} onChange={e=>{setFilter(e.target.value as Filter);setPage(0)}}>
        <option value="all">All ({rows.length})</option><option value="attention">Needs attention ({counts.attention})</option>
        <option value="waiting">Awaiting host ({counts.waiting})</option><option value="verified">CLI verified ({counts.verified})</option>
      </select></label></div>
      {found.length>0?<table aria-label={"Onboarding receipts for "+host}><thead><tr><th scope="col">Seat</th><th scope="col">Status</th><th scope="col">Verified at (UTC)</th></tr></thead>
        <tbody>{found.slice(index*pageSize,(index+1)*pageSize).map(row=><tr key={row[0]}><th scope="row">{row[0]}</th>
        <td>{row[3]?"CLI verified":row[4]<=now?"Authorization expired":problemLabels[row[5]]||row[5]||"Awaiting host"}</td>
        <td>{row[3]?<time dateTime={new Date(row[3]).toISOString()}>{day.format(row[3])}<br/>{clock.format(row[3])}</time>:"—"}</td></tr>)}</tbody></table>:<p>No matching seats</p>}
      {found.length>pageSize&&<div className="receipt-pages"><span>{index*pageSize+1}–{Math.min(found.length,(index+1)*pageSize)} of {found.length}</span>
      <button disabled={index===0} onClick={()=>setPage(index-1)}>Previous</button><button disabled={(index+1)*pageSize>=found.length} onClick={()=>setPage(index+1)}>Next</button></div>}
    </div>}
  </article>
}
export function OnboardingStatus({rows}:{rows:Receipt[]}){
  const [now,setNow]=useState(Date.now);
  useEffect(()=>{
    let timer:ReturnType<typeof setTimeout>;
    function update(){
      const at=Date.now();setNow(at);
      const deadline=Math.min(...rows.filter(r=>!r[3]&&r[4]>at).map(r=>r[4]));
      if(Number.isFinite(deadline))timer=setTimeout(update,Math.min(2147483647,deadline-at+1));
    }
    update();return ()=>clearTimeout(timer);
  },[rows]);
  const groups=new Map<string,Receipt[]>(),hosts=new Map<string,Set<string>>();
  for(const row of rows){
    const id=row[6]??row[1], key=JSON.stringify([id,row[2]]);
    if(!groups.has(key))groups.set(key,[]);
    groups.get(key)!.push(row);
    if(!hosts.has(row[1]))hosts.set(row[1],new Set());
    hosts.get(row[1])!.add(id);
  }
  return <section aria-label="Onboarding status" className="card detail receipts"><h2>Onboarding status</h2>
    {[...groups].map(([key,group])=><ReceiptGroup key={key} rows={group} now={now} disambiguate={hosts.get(group[0][1])!.size>1}/>)}
  </section>
}
