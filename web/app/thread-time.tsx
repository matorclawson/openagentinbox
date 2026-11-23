import {CalendarDays, ChevronLeft, ChevronRight} from "lucide-react";

export const HOUR=3600000, DAY=24*HOUR;
const spans={hour:HOUR,day:DAY,month:30*DAY};
export type Scale=keyof typeof spans;
const dayLabel=new Intl.DateTimeFormat(undefined,{timeZone:"UTC",day:"numeric",month:"short",year:"numeric"});
const timeLabel=new Intl.DateTimeFormat(undefined,{timeZone:"UTC",hour:"2-digit",minute:"2-digit",hourCycle:"h23"});
const labelDay=(v:number)=>dayLabel.format(v);
const utcDay=(v:number)=>Math.floor(v/DAY)*DAY;
export function ThreadTime({scale,start,counts,choose}:{scale:Scale|"";start:number;counts:number[];choose:(scale:Scale|"",start:number)=>void}){
 const now=Date.now(),span=scale?spans[scale]:0,end=start+span;
 const focus=scale?Math.min(end-1,now):now;
 const day=utcDay(focus);
 const size=scale==="month"?DAY:scale==="day"?HOUR:HOUR/12;
 const valid=!!scale && Number.isFinite(start) && start>=0 && end<253402300800000;
 const range=valid?(scale==="hour"?labelDay(start)+" · "+timeLabel.format(start)+"–"+timeLabel.format(end):scale==="day"?labelDay(start):labelDay(start)+" – "+labelDay(end-1)):"";
 const max=Math.max(1,...counts);
 const daysAgo=(v:number)=>Math.round((utcDay(now)-utcDay(v))/DAY);
 const relativeDay=(v:number)=>daysAgo(v)===0?"Today":daysAgo(v)===1?"Yesterday":daysAgo(v)+" days ago";
 return <section className="time-explorer" aria-label="Conversation timeline">
   <div className="time-tools"><div className="zoom-levels" aria-label="Time scale">
    {([["","All time"],["hour","Hour"],["day","Day"],["month","30 days"]] as const).map(([key,label])=><button key={label} aria-pressed={key===scale} onClick={()=>choose(key,key==="month"?day-29*DAY:key==="day"?day:focus-HOUR+1)}>{label}</button>)}
   </div><button className="recent-hour" onClick={()=>choose("hour",Date.now()-HOUR)}>Last hour</button>
   <label className="pick-date"><CalendarDays size={15}/><input type="date" aria-label="Go to date (UTC)" max={new Date(now).toISOString().slice(0,10)} value={valid?new Date(focus).toISOString().slice(0,10):""} onChange={e=>{const ms=e.currentTarget.valueAsNumber;if(Number.isFinite(ms))choose("day",ms)}}/></label></div>
   {valid && <><div className="time-range"><button aria-label="Previous time period" disabled={start<span} onClick={()=>choose(scale,start-span)}><ChevronLeft size={16}/></button>
    <strong>{range} UTC{scale==="day"?" · "+relativeDay(start):""}</strong><button aria-label="Next time period" disabled={end>now} onClick={()=>choose(scale,end)}><ChevronRight size={16}/></button></div>
    {scale==="month" && <div className="weekdays" aria-hidden="true">{["Mon","Tue","Wed","Thu","Fri","Sat","Sun"].map(d=><span key={d}>{d}</span>)}</div>}
    <div className={"time-bars scale-"+scale} aria-label="Messages sent per interval">{counts.map((n,i)=>{
     const at=start+i*size,label=(scale==="month"?labelDay(at)+" · "+relativeDay(at):timeLabel.format(at)+" UTC")+" · "+n+" messages";
     const bar=<span style={{height:n?Math.max(8,n/max*100)+"%":"2px"}}/>;
     return scale==="hour"?<div key={at} title={label} aria-label={label}>{bar}</div>:<button key={at} style={scale==="month" && i===0?{gridColumnStart:1+(new Date(at).getUTCDay()+6)%7}:undefined} title={label} aria-label={label} onClick={()=>choose(scale==="month"?"day":"hour",at)}><b>{scale==="month"?(i===0||new Date(at).getUTCDate()===1?new Intl.DateTimeFormat(undefined,{timeZone:"UTC",month:"short",day:"numeric"}).format(at):new Date(at).getUTCDate()):timeLabel.format(at)}</b>{bar}</button>
    })}</div>
    <div className="time-caption"><span>{counts.reduce((a,b)=>a+b,0).toLocaleString()} messages</span><span>{scale==="month"?"Select a day":scale==="day"?"Select an hour":"5-minute intervals"}</span></div></>}
 </section>;
}
