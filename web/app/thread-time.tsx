import {useEffect, useRef, useState} from "react";
import {ArrowUpLeft, CalendarDays, ChevronDown, ChevronLeft, ChevronRight, Clock, X} from "lucide-react";

export const HOUR=3600000, DAY=24*HOUR;
const spans={hour:HOUR,day:DAY,month:30*DAY};
export type Scale=keyof typeof spans;
const date=new Intl.DateTimeFormat(undefined,{timeZone:"UTC",day:"numeric",month:"short"});
const weekday=new Intl.DateTimeFormat(undefined,{timeZone:"UTC",weekday:"long"});
const time=new Intl.DateTimeFormat(undefined,{timeZone:"UTC",hour:"2-digit",minute:"2-digit",hourCycle:"h23"});
const dayStart=(v:number)=>Math.floor(v/DAY)*DAY;
const bands=["Overnight","Morning","Afternoon","Evening"];
function messages(n:number){return n.toLocaleString()+(n===1?" message":" messages")}

export function ThreadTime({scale,start,counts,choose}:{scale:Scale|"";start:number;counts:number[];choose:(scale:Scale|"",start:number)=>void}){
 const now=Date.now(), span=scale?spans[scale]:0, end=start+span;
 const valid=span>0 && Number.isFinite(start) && start>=0 && end<253402300800000;
 const focus=valid?Math.min(end-1,now):now;
 const dialog=useRef<HTMLDialogElement>(null);
 const trigger=useRef<HTMLButtonElement>(null);
 const heading=useRef<HTMLHeadingElement>(null);
 const [band,setBand]=useState<number|null>(null);
 const focusAfterChange=useRef(false);
 useEffect(()=>{
   dialog.current?.close();setBand(null);
   if(focusAfterChange.current){heading.current?.focus({preventScroll:true});focusAfterChange.current=false}
 },[scale,start]);
 function select(next:Scale|"",at:number){
   dialog.current?.close();
   focusAfterChange.current=true;
   choose(next,at);
 }
 function goWider(){if(scale==="hour")select("day",dayStart(focus));else if(scale==="day")select("month",dayStart(focus)-29*DAY);else select("",0)}
 const total=counts.reduce((sum,n)=>sum+n,0);
 const ready=counts.length===(scale==="month"?30:scale==="day"?24:12);
 const month=scale==="month";
 const recent=()=>select("hour",Date.now()-HOUR);
 const overview=()=>select("month",dayStart(now)-29*DAY);
 const label=valid?(month?date.format(start)+" – "+date.format(end-1):scale==="day"?weekday.format(start)+", "+date.format(start):time.format(start)+" – "+time.format(end)):"All messages";
 const relative=(v:number)=>{const n=(dayStart(now)-dayStart(v))/DAY;return n===0?"Today":n===1?"Yesterday":n+" days ago"};
 return <section className="period-browser" aria-label="Conversation timeline">
   {!valid?<div className="period-shortcuts"><button onClick={recent}><Clock size={16}/>Last hour</button><button onClick={overview}><CalendarDays size={16}/>Browse dates</button></div>:<>
    <div className="period-path"><button onClick={goWider}><ArrowUpLeft size={15}/>{month?"All time":scale==="day"?"30 days":"Day"}</button>
      <span>{scale==="hour"?date.format(start)+(dayStart(start)!==dayStart(end)?" – "+date.format(end):""):"UTC"}</span></div>
    <div className="period-head"><div><h2 tabIndex={-1} ref={heading}>{label}{scale==="hour" && <small> UTC</small>}</h2>
      <p>{scale==="day"?relative(start)+" · ":""}{ready?messages(total):"Loading…"}</p></div>
      <div className="period-arrows"><button aria-label="Previous time period" disabled={start<span} onClick={()=>select(scale,start-span)}><ChevronLeft size={20}/></button>
      <button aria-label="Next time period" disabled={end>=now} onClick={()=>select(scale,end)}><ChevronRight size={20}/></button></div>
    </div>
    {scale!=="hour" && <button ref={trigger} className="period-open" disabled={!ready} aria-haspopup="dialog" onClick={()=>{setBand(null);dialog.current?.showModal()}}>
      {month?<CalendarDays size={17}/>:<Clock size={17}/>} {month?"Choose a day":"Choose an hour"} <ChevronDown size={17}/></button>}
    <dialog ref={dialog} className="period-sheet" aria-labelledby="period-picker-title" onCancel={()=>trigger.current?.focus()} onClick={e=>{if(e.target===dialog.current){const r=e.currentTarget.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)e.currentTarget.close()}}}>
      <header><div><p>{month?label:date.format(start)} · UTC</p><h2 id="period-picker-title">{month?"Choose a day":"Choose an hour"}</h2></div>
      <button aria-label="Close picker" autoFocus onClick={()=>dialog.current?.close()}><X size={20}/></button></header>
      <div className="period-sheet-body">{month?<><label className="specific-date">Go to date<input type="date" aria-label="Go to date (UTC)" max={new Date(now).toISOString().slice(0,10)} onChange={e=>{const ms=e.currentTarget.valueAsNumber;if(Number.isFinite(ms))select("day",ms)}}/></label>
       {Array.from({length:counts.length},(_,j)=>counts.length-1-j).map(i=>{const at=start+i*DAY;return <button className="period-row" key={at} onClick={()=>select("day",at)}>
        <span><strong>{weekday.format(at)}, {date.format(at)}</strong><small>{relative(at)}</small></span><span>{messages(counts[i])}</span><ChevronRight size={17}/></button>})}</>:
       bands.map((name,i)=><div key={name}><button aria-expanded={band===i} className="period-row band-row" onClick={()=>setBand(band===i?null:i)}>
         <span><strong>{name}</strong><small>{time.format(start+i*6*HOUR)} – {i===3?"24:00":time.format(start+(i+1)*6*HOUR)}</small></span>
         <span>{messages(counts.slice(i*6,i*6+6).reduce((sum,n)=>sum+n,0))}</span><ChevronDown size={17}/></button>
         {band===i && <div className="hour-options">{counts.slice(i*6,i*6+6).map((n,j)=>{const at=start+(i*6+j)*HOUR;return <button className="period-row" key={at} onClick={()=>select("hour",at)}>
           <strong>{time.format(at)} UTC</strong><span>{messages(n)}</span><ChevronRight size={17}/></button>})}</div>}</div>)}</div>
    </dialog>
   </>}
 </section>
}
