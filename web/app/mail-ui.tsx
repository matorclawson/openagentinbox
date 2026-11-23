import { Fragment, useEffect, useRef, useState } from "react";
import { MessagesSquare, CheckCheck, MailOpen, Mail, Send } from "lucide-react";

export type Envelope = {
  id:string; src:string; dst:string; subject:string; at:string; thread?:string;
  count?:number; conversation?:boolean; received?:number; opened?:number; replied?:number;
};
export function useMailClock() {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const tick = () => { if (!document.hidden) setNow(Date.now()); };
    const timer = setInterval(tick, 30000);
    document.addEventListener("visibilitychange", tick);
    return () => { clearInterval(timer); document.removeEventListener("visibilitychange", tick); };
  }, []);
  return now;
}
const dateFormat = new Intl.DateTimeFormat(undefined,{month:"short",day:"numeric",year:"numeric"});
function day(value:string|number, now:number) {
  const date = new Date(value);
  if (!Number.isFinite(+date)) return "Date unavailable";
  if (date.toDateString() === new Date(now).toDateString()) return "Today";
  return dateFormat.format(date);
}
export function MailTime({value,now,label}:{value:string|number;now:number;label:string}) {
  const date = new Date(value);
  if (!Number.isFinite(+date)) return <span>Time unavailable</span>;
  const minutes = Math.floor((now - +date)/60000);
  const text = day(value,now) !== "Today" ? dateFormat.format(date) : minutes < 0 ? date.toLocaleTimeString([], {hour:"numeric",minute:"2-digit"}) : minutes === 0 ? "Just now" : minutes < 60 ? minutes+" min ago" : Math.floor(minutes/60)+" hr ago";
  const exact = label+" "+date.toLocaleString();
  return <time dateTime={date.toISOString()} title={exact} aria-label={exact}>{text}</time>;
}
export function Avatar({name}:{name:string}) {
  const text = name.replace(/^external:/,"");
  const words = text.split(/[-.@_ ]+/).filter(Boolean);
  const initials = words.length > 1 ? words[0][0]+words[1][0] : text.slice(0,2);
  const tint = Array.from(text).reduce((sum,ch)=>(sum*31+ch.charCodeAt(0))>>>0,0)%5;
  return <span aria-hidden="true" className={"person tint"+tint}>{initials.toUpperCase()}</span>;
}
export function Receipt({row}:{row:Envelope}) {
  const external = row.dst.includes("@"), multiple = row.dst.includes(", ");
  const state = external || multiple ? "Sent" : row.replied ? "Replied" : row.opened ? "Opened" : "Delivered";
  const Icon = state==="Replied"?CheckCheck:state==="Opened"?MailOpen:state==="Sent"?Send:Mail;
  return <span className={"mail-status status-"+state.toLowerCase()} title={state+" · latest message"}><Icon size={13}/>{state}</span>;
}
export function MailList({rows,box,sent,timeline,now,follow}:{rows:Envelope[];box:string;sent:boolean;timeline:boolean;now:number;follow:React.MouseEventHandler<HTMLAnchorElement>}) {
  const [expanded,setExpanded] = useState<{id:string;body?:string;error?:string}|null>(null);
  const request = useRef(0);
  async function expand(id:string) {
    const turn = ++request.current;
    if (expanded?.id===id) {setExpanded(null); return;}
    setExpanded({id});
    try {
      const res = await fetch("/api/desk/message/"+encodeURIComponent(id),{cache:"no-store"});
      if (!res.ok) throw Error("Could not open this message.");
      const data = await res.json();
      if (turn===request.current) setExpanded({id,body:data.body});
    } catch (error) {
      if (turn===request.current) setExpanded({id,error:(error as Error).message});
    }
  }
  let previous = "";
  return <div className={timeline?"post-list timeline":"post-list"}>{rows.map(row=>{
    const outgoing = timeline ? row.src===box : sent;
    const who = outgoing ? row.dst : row.src;
    const stamp = (!outgoing && row.received) || row.at;
    const group = day(stamp,now), showDay = group!==previous;
    previous=group;
    const grouped = !timeline && row.conversation;
    const href = grouped ? "/inboxes/"+encodeURIComponent(box)+"/threads/"+encodeURIComponent(row.thread||row.id) : "/messages/"+encodeURIComponent(row.id);
    const content = <>
        <Avatar name={who}/>
        <div className="post-main"><span className="correspondent">{outgoing?"To ":""}{who.replace(/^external:/,"")}</span>
        <strong>{row.subject||"(No subject)"}</strong>
        {grouped && <span className="conversation-cue"><MessagesSquare size={13}/>Conversation{(row.count??0)>1?" · "+row.count+" in "+(sent?"Sent":"Inbox"):""}</span>}</div>
        <div className="post-meta"><MailTime value={stamp} now={now} label={outgoing?"Sent":"Received"}/><Receipt row={row}/></div>
      </>;
    return <Fragment key={row.id}>{showDay && <h2 className="post-day">{group}</h2>}
      {timeline ? <><button className="post-row" aria-expanded={expanded?.id===row.id} onClick={()=>void expand(row.id)}>{content}</button>
      {expanded?.id===row.id && <div className="thread-body"><pre>{expanded.body??expanded.error??"Opening message…"}</pre><a href={href} onClick={follow}>Open message page →</a></div>}</>
      : <a className="post-row" href={href} onClick={follow}>{content}</a>}</Fragment>;
  })}</div>;
}
