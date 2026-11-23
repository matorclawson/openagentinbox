import {useEffect,useRef,useState,type ReactNode} from "react";
import {ChevronDown,ChevronRight} from "lucide-react";

export const HOUR=3600000,DAY=24*HOUR;
export type Level="all"|"month"|"week"|"day"|"band"|"hour";
type Item={start:number;count:number;latest:number};
const levels:Level[]=["all","month","week","day","band","hour"];
const utc=(date:number,options:Intl.DateTimeFormatOptions)=>new Date(date).toLocaleDateString(undefined,{...options,timeZone:"UTC"});
const monthStart=(at:number)=>{const d=new Date(at);return Date.UTC(d.getUTCFullYear(),d.getUTCMonth(),1)};
const dayStart=(at:number)=>Math.floor(at/DAY)*DAY;
const weekStart=(at:number)=>Math.max(monthStart(at),dayStart(at)-(new Date(at).getUTCDay()+6)%7*DAY);
const bandStart=(at:number)=>Math.floor(at/(6*HOUR))*6*HOUR;
const hours=(at:number)=>new Date(at).toISOString().slice(11,16);
const formatDay=(at:number)=>utc(at,{month:"short",day:"numeric"});
function group(items:Item[],key:(at:number)=>number){
 const counts=new Map<number,number>();
 for(const item of items){const start=key(item.start);counts.set(start,(counts.get(start)??0)+item.count)}
 return Array.from(counts,([start,count])=>({start,count,latest:start}));
}
function useGroups(url:string){
 const [data,set]=useState({url:"",items:[] as Item[],next:0,error:false});
 const [attempt,retry]=useState(0);
 useEffect(()=>{
   if(!url)return;
   const abort=new AbortController();
   fetch(url,{signal:abort.signal,cache:"no-store"}).then(async r=>{if(!r.ok)throw Error();return r.json()}).then(v=>{
    if(!abort.signal.aborted)set({url,items:v.items,next:v.next,error:false});
   }).catch(()=>{if(!abort.signal.aborted)set({url,items:[],next:0,error:true})});
   return ()=>abort.abort();
 },[url,attempt]);
 return {...data,items:data.url===url?data.items:[],ready:data.url===url,failed:data.url===url&&data.error,retry:()=>retry(n=>n+1)};
}
type Props={api:string;level:Level|"";at:number;choose:(l:Level,at:number,replace?:boolean)=>void;children:ReactNode;revision:number};
export function ThreadTime({api,level,at,choose,children,revision}:Props){
 const base=api+"&refresh="+revision;
 const [pages,setPages]=useState<number[]>([0]);
 const cursor=pages[pages.length-1];
 const months=useGroups(base+"&outline=months&at="+cursor);
 const depth=levels.indexOf(level||"all");
 const month=monthStart(at),week=weekStart(at),day=dayStart(at),band=bandStart(at);
 const days=useGroups(depth>=1?base+"&outline=days&at="+month:"");
 const hourRows=useGroups(depth>=3?base+"&outline=hours&at="+day:"");
 const action=useRef(choose);action.current=choose;
 useEffect(()=>{
   if(!level && months.ready && months.items.length)action.current("hour",Math.floor(months.items[0].latest/HOUR)*HOUR,true);
 },[level,months.ready,months.items]);
 const chosen=[0,month,week,day,band,at];
 const weeks=group(days.items,weekStart);
 const bands=group(hourRows.items,bandStart);
 const monthEnd=new Date(month);monthEnd.setUTCMonth(monthEnd.getUTCMonth()+1);
 function status(data:ReturnType<typeof useGroups>){return data.failed?<button onClick={data.retry}>Retry loading periods</button>:!data.ready?<p role="status">Loading periods…</p>:null}
 function branch(item:Item,n:number,label:string,nested:()=>ReactNode){
   const open=depth>=n && item.start===chosen[n];
   const age=Math.floor((dayStart(Date.now())-dayStart(item.start))/DAY);
   const note=n===3?(age===0?"Today":age===1?"Yesterday":age+" days ago"):n===4?hours(item.start)+" – "+(new Date(item.start).getUTCHours()===18?"24:00":hours(item.start+6*HOUR)):"";
   return <li key={item.start}>
    <button className="time-branch" data-level={levels[n]} data-start={item.start} aria-expanded={open} onClick={()=>open?choose(levels[n-1],chosen[n-1]):choose(levels[n],item.start)}>
      {open?<ChevronDown size={18}/>:<ChevronRight size={18}/>}<strong>{label}{note && <small>{note}</small>}</strong>
      <span aria-label={item.count+" messages"}>{item.count.toLocaleString()}</span></button>
    {open && nested()}</li>;
 }
 // A deep link may point beyond the current page of months.
 const visible=[...months.items];
 if(depth>=1 && months.ready && days.ready && days.items.length && !visible.some(v=>v.start===month))
  visible.push({start:month,count:days.items.reduce((n,d)=>n+d.count,0),latest:at});
 return <section className="time-outline" aria-label="Conversation timeline"><div className="outline-caption">Timeline · UTC <span>Messages</span></div>
 {status(months)}{months.ready && !months.failed && !visible.length && <p>No activity in this period.</p>}
 <ul>{visible.map(m=>branch(m,1,utc(m.start,{month:"long",year:"numeric"}),()=> <>{status(days)}<ul>
 {weeks.map(w=>branch(w,2,formatDay(w.start)+" – "+formatDay(Math.min(monthEnd.getTime()-DAY,w.start+(6-(new Date(w.start).getUTCDay()+6)%7)*DAY)),()=> <ul>
 {days.items.filter(d=>weekStart(d.start)===week).map(d=>branch(d,3,utc(d.start,{weekday:"short",day:"numeric",month:"short"}),()=><>{status(hourRows)}<ul>
 {bands.map(b=>branch(b,4,["Overnight","Morning","Afternoon","Evening"][new Date(b.start).getUTCHours()/6],()=> <ul>
 {hourRows.items.filter(h=>bandStart(h.start)===band).map(h=>branch(h,5,hours(h.start)+" UTC",()=> <div className="outline-messages">{children}</div>))}
 </ul>))}</ul></>))}
 </ul>))}</ul></>))}</ul>
 {months.ready && (pages.length>1||months.next>0) && <div className="outline-pages">
 {pages.length>1 && <button onClick={()=>{choose("all",0);setPages(p=>p.slice(0,-1))}}>Newer months</button>}
 {months.next>0 && <button onClick={()=>{choose("all",0);setPages(p=>[...p,months.next])}}>Earlier months</button>}</div>}
 </section>;
}
