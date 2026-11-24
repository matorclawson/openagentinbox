"use client";
import { useEffect, useState, useRef, type FormEvent } from "react";
import {
  Settings, Settings2, Menu, Layers, ArrowDownLeft, ArrowUpRight, ChevronRight, MessageSquare,
  Inbox,
  Activity,
  Send,
  Users,
  PenLine,
  RefreshCw,
  LogOut,
  ArrowLeft,
  Mail,
  KeyRound,
  ArrowRight,
  Plus,
} from "lucide-react";
import {BodyText, mailTitle, Avatar, Receipt, MailTime, MailList, useMailClock, type Envelope} from "./mail-ui";
import {splitMail} from "./mail-text";
import {ThreadTime,type Level} from "./thread-time";
import {MailTools} from "./mail-tools";
import {SettingsPage} from "./settings";
import {Mator} from "./brand-art";
import {MailSetup} from "./mail-setup";
import { Workspace, EnrollmentQueue, useWorkspace } from "./live";
type Session = {
  name: string | null;
  setup: boolean;
  openpassword: boolean;
  linked: boolean;
};
type Accounts = { operator: string; agents: string[]; email_domain?: string;operator_email_send?: boolean };
type Row = Envelope & {
 thread?:string; count?:number; audience?:string[];
  id: string;
  src: string;
  dst: string;
  subject: string;
  at: string; trust?: string; email?: {remote_action?:string;remote_status?:string}; original_available?: boolean; email_status?: string; opened?: number; replied?: number; state?: string; due?: number;
};
async function api(path: string, body?: object) {
  const r = await fetch("/api/" + path, {
    cache: "no-store",
    method: body ? "POST" : "GET",
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!r.ok) {
    const msg = await r.json().catch(() => ({}));
    throw Error(msg.error || "Request failed");
  }
  return r.json();
}
function Brand() {
  return (
    <div className="brand">
      <img className="brand-icon" src="/brand/icon.png" alt="" width="32" height="32"/>
      <span>Open</span>AgentInbox
    </div>
  );
}
function ConnectHelp() {
  return (
    <details className="connection">
      <summary>Connect an OpenPassword account</summary>
      <p>
        In OpenPassword, register an identity-only application with audience{" "}
        <code>openagentinbox</code> and your HTTPS inbox URL followed by{" "}
        <code>/api/callback</code>. Activate the application.
      </p>
      <p>
        Set <code>OPENPASSWORD_ORIGIN</code>, <code>OPENPASSWORD_APP</code> and{" "}
        <code>INBOX_ORIGIN</code> on the inbox host, then restart. Continue with
        OpenPassword will appear here.
      </p>
    </details>
  );
}
type MappingResult = {applied:boolean; conflicts:number; rows:{from:string;to:string;status:string;mailbox_id:string}[]};
type RevokePreview={rows:{name:string;mailbox_id:string;current:string;status:string}[];preview:string;count:number;conflicts:number;applied:boolean};
function RevokeList(){
  const [names,edit]=useState(""),[review,show]=useState<RevokePreview|null>(null);
  const [error,fail]=useState(""),[busy,wait]=useState(false);
  async function submit(preview=""){
    wait(true);fail("");
    try{show(await api("desk/revoke-batch",{names:names.trim().split(/\s+/),preview}))}
    catch(e){fail((e as Error).message);show(null)}finally{wait(false)}
  }
  return <details className="card detail"><summary>Revoke inbox access from a list</summary>
    <label>Current addresses to revoke<textarea value={names} disabled={busy} onChange={e=>{edit(e.target.value);show(null);fail("")}} rows={4}/></label>
    <p>Paste the old inbox addresses only, one per line. Messages and mailbox IDs are retained.</p>
    <button disabled={busy||!names.trim()} onClick={()=>void submit()}>Preview revocation</button>
    {error&&<p role="alert">{error}</p>}
    {review&&<><p role="status">{review.applied?review.count+" credentials revoked.":review.conflicts?review.conflicts+" conflicts. Edit the list.":review.count+" credentials selected."}</p>
    <ul>{review.rows.map(row=><li key={row.name}>{row.name} · <code>{row.mailbox_id}</code> · {review.applied&&row.status==="will revoke"?"revoked":row.status}{row.current&&row.current!==row.name?" (current: "+row.current+")":""}</li>)}</ul>
    {!review.applied&&!review.conflicts&&review.count>0&&<button disabled={busy} onClick={()=>void submit(review.preview)}>Revoke {review.count} credentials</button>}</>}
  </details>
}
function AddressEditor(){
  const [text,edit]=useState("");
  const [result,show]=useState<MappingResult|null>(null);
  const [busy,setBusy]=useState(false);
  const [error,setError]=useState("");
  async function submit(apply:boolean){
    setBusy(true);setError("");
    try{
      const moves=text.trim().split(/\r?\n/).filter(line=>line.trim()).map(line=>{
        const parts=line.trim().split(/\s+/);
        if(parts.length!==2)throw Error("Use two names on each line.");
        return {from:parts[0],to:parts[1]};
      });
      show(await api("desk/addresses",{moves,apply}));
    }catch(e){setError((e as Error).message);show(null)}finally{setBusy(false)}
  }
  return <details className="card detail"><summary>Rename inboxes from a roster</summary>
    <label>Existing address and current seat name<textarea disabled={busy} rows={4} placeholder={"support support-maintainer"} value={text} onChange={e=>{edit(e.target.value);show(null)}}/></label>
    <p>Paste two columns. Old addresses keep receiving mail. Occupied names require review.</p>
    <button disabled={!text.trim()||busy} onClick={()=>void submit(false)}>Preview mappings</button>
    {error&&<p role="alert">{error}</p>}
    {result&&<><p role="status">{result.applied?"Mappings applied.":result.conflicts?result.conflicts+" conflicts. No changes saved.":"Ready to apply."}</p>
    <ul>{result.rows.map((r,i)=><li key={i}>{r.from} → {r.to} · {r.status} <code>{r.mailbox_id}</code></li>)}</ul>
    {!result.applied&&!result.conflicts&&<button disabled={busy} onClick={()=>void submit(true)}>Apply mappings</button>}</>}
  </details>
}
export default function Home() {
  const [s, session] = useState<Session | null>(null),
    [acc, accounts] = useState<Accounts | null>(null);
  const now = useMailClock();
  const [menu, setMenu] = useState(false);
  const [path, setPath] = useState("/");
  const [filter, setFilter] = useState("");
  const [shareSeat, setShareSeat] = useState("");
  const [pathname,queryString=""]=path.split("?");
  const params=new URLSearchParams(queryString);
  const term=params.get("q")??"", requests=params.get("view")==="requests", before=params.get("before")??"";
  const [compact,setCompact]=useState(true);
  useEffect(()=>{try{setCompact(localStorage.getItem("inbox-density")!=="comfortable")}catch{}},[]);
  function toggleDensity(){setCompact(!compact);try{localStorage.setItem("inbox-density",compact?"comfortable":"compact")}catch{}}
  const number=params.get("number")??"";
  const requestedLevel=params.get("period")??"";
  const level=(["all","month","week","day","band","hour"].includes(requestedLevel) && Number(params.get("start"))>=0 && Number(params.get("start"))<253402214400000?requestedLevel:"") as Level|"";
  const zoom=level==="hour"?"hour":params.get("zoom")??"";
  const start=params.get("start")??"";
  const [revision,setRevision]=useState(0);
  function chooseTime(period:Level,time:number,replace=false){
    navigate(timelinePath+"?period="+period+(period==="all"?"":"&start="+time),replace)
  }
  const after=params.get("after")??"", anchor=params.get("atSeq")??"";
  const [threadInfo,setThreadInfo]=useState({key:"",newer:"",title:"",received:0,sent:0});
  function browse(query:string,pending:boolean,page="",direction="before"){
    const params=new URLSearchParams();
    if(query)params.set("q",query);
    if(pending)params.set("view","requests");
    if(page)params.set(direction,page);
    if(zoom && !query && page && (direction==="before"||direction==="after")){if(level)params.set("period",level);else params.set("zoom",zoom);params.set("start",start)}
    navigate((isOutline && query?conversationPath:pathname)+(params.size?"?"+params:""));
  }
  const parts = pathname.split("/");
  const box = parts[1] === "inboxes" ? parts[2] : acc?.operator ?? "operator";
  const threadId=parts[3]==="threads"?parts[4]:"";
 const folder = parts[1]==="settings"?"settings":parts[1]==="mail-setup"?"mail-setup":parts[1] === "activity" ? "activity" : parts[1] === "compose" ? "compose" : parts[1] === "agents" ? "agents" : parts[3] === "sent" ? "sent" : "inbox";
  const conversationPath=parts.slice(0,5).join("/");
  const timelinePath=conversationPath+"/timeline";
  const isOutline=(parts[5]==="timeline"||!!level)&&!!threadId&&!term&&!anchor&&!number&&!params.has("zoom")&&(!!level||(!before&&!after));
  const messageId = parts[1] === "messages" ? parts[2] : "";
  const     [rows, list] = useState<Row[]>([]),
    [cursor, next] = useState<string | null>(null);
  const [selected, select] = useState<{ row: Row; body: string } | null>(null),
    [busy, work] = useState(false),
    [error, warn] = useState(""),
    [notice, say] = useState(""),
    [credential, key] = useState("");
  const [attachment,setAttachment]=useState<File|null>(null);
  const [draft, write] = useState({ to: "", subject: "", body: "", key: "",intent:"fyi",replyTo:"" });
  const [pending, setPending] = useState<{ name: string; code: string } | null>(
    null,
  );
  const {data:live,connection}=useWorkspace(s?.name);
  const agentNames=live?.agents.map(([name])=>name)??acc?.agents??[];
  const newest=live?.cursor;
  const generation = useRef(0);
  async function task(fn: () => Promise<void>) {
    work(true);
    warn("");
    try {
      await fn();
    } catch (e) {
      warn((e as Error).message);
    } finally {
      work(false);
    }
  }
  async function init() {
    const linkedCode = new URLSearchParams(location.search).get("enroll");
    if (linkedCode && /^[A-F0-9]{12}$/.test(linkedCode))
      sessionStorage.setItem("enrollment", linkedCode);
    const info = await api("session");
    session(info);
    if (info.name) {
      const a = await api("desk/accounts");
      accounts(a);
      if(location.pathname==="/")navigate("/inboxes/"+a.operator,true);else setPath(location.pathname+location.search);
      const c = sessionStorage.getItem("enrollment");
      if (c) {
        navigate("/agents", true);
        try {
          setPending(await api("desk/enrollment/" + encodeURIComponent(c)));
        } catch (e) {
          sessionStorage.removeItem("enrollment");
          throw e;
        }
      }
    }
  }
  useEffect(() => {
    const restore = () => { setPath(location.pathname+location.search); warn(""); say(""); key(""); };
    window.addEventListener("popstate", restore);
    void task(init);
    return () => window.removeEventListener("popstate", restore);
  }, []);
  async function load() {
    const gen = ++generation.current;
    const q = new URLSearchParams({
      box,
      folder,
      q:term,view:requests?"requests":"",before,after,atSeq:anchor,number,zoom,start,
    });
    if(isOutline && level!=="hour")q.set("outline","summary");
    const data = await api("desk/threads"+(threadId?"/"+threadId:"")+"?" + q);
    if (gen === generation.current) {
      list(data.rows??[]);
      if(number && data.anchor){
        const permanent=pathname+"?atSeq="+encodeURIComponent(data.anchor);
        history.replaceState(history.state,"",permanent);setPath(permanent);
      }
      setThreadInfo(previous=>{
        if (data.title===undefined && previous.key===pathname) return {...previous,newer:data.newer??""};
        return {key:data.title===undefined?"":pathname,newer:data.newer??"",title:data.title??"",received:data.inbox_count??0,sent:data.sent_count??0};
      });
      next(data.next);
    }
  }
  useEffect(() => {
    if (!messageId && s?.name && acc && (folder === "inbox" || folder === "sent")) {
      list([]);
      next(null);
      void task(() => load());
    }
    return () => {
      generation.current++;
    };
  }, [path, box, s?.name, acc]);
  useEffect(()=>{
    if(threadId||before||messageId||!newest||!s?.name||(folder!=="inbox"&&folder!=="sent"))return;
    const stamp=generation.current;
    const q=new URLSearchParams({box,folder,q:term,view:requests?"requests":""});
    let cancelled=false;
    void api("desk/threads"+(threadId?"/"+threadId:"")+"?"+q).then(data=>{
      if(cancelled||stamp!==generation.current)return;
      list(data.rows); next(data.next);
    }).catch(()=>{});
    return ()=>{cancelled=true};
  },[newest,path,box,s?.name]);
  useEffect(() => {
    select(null);
    setShareSeat("");
    if (!messageId || !acc || !s?.name) return;
    let active = true;
    api("desk/message/" + messageId).then(row => {
      if (active) select({row, body:row.body});
    }).catch(e => { if (active) warn(e.message); });
    return () => { active = false; };
  }, [messageId, acc, s?.name]);
  useEffect(()=>{
    if (!messageId || !live?.cursor || !s?.name) return;
    let active=true;
    void api("desk/receipt/"+messageId).then(receipt=>{
      if(active)select(old=>old && !old.row.audience && encodeURIComponent(old.row.id)===messageId?{...old,row:{...old.row,...receipt,email_status:receipt.email?.state??old.row.email_status}}:old);
    }).catch(()=>{});
    return ()=>{active=false};
  },[live?.cursor,messageId,s?.name]);
  function navigate(to: string, replace = false) {
    if (replace) history.replaceState(null, "", to);
    else if (location.pathname+location.search !== to) history.pushState({from:location.pathname}, "", to);
    setMenu(false);
    setPath(to);
    key(""); warn(""); say("");
  }
  function go(v: typeof folder, seat = box) {
    navigate(v==="settings" || v==="mail-setup" || v === "activity" || v === "compose" || v === "agents" ? "/" + v : "/inboxes/" + encodeURIComponent(seat) + (v === "sent" ? "/sent" : ""));
  }
  useEffect(()=>{
    if(!rows.length||!history.state?.mailAnchor)return;
    const id=history.state.mailAnchor;
    const row=document.getElementById("row-"+id);
    if(row){history.replaceState({...history.state,mailAnchor:null},"");row.focus({preventScroll:true});row.scrollIntoView({block:"nearest"})}
  },[rows]);
  function follow(e: React.MouseEvent<HTMLAnchorElement>) {
    if (!e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey && e.button === 0) {
      e.preventDefault(); if(e.currentTarget.dataset.message)history.replaceState({...history.state,mailAnchor:e.currentTarget.dataset.message},""); navigate(e.currentTarget.pathname+e.currentTarget.search);
    }
  }
  async function copy(value: string, success: string) {
    await navigator.clipboard.writeText(value);
    say(success);
  }
  function compose(to = "", subject = "", replyTo="") {
    setAttachment(null);
    go("compose");
    write({ to, subject, body: "", key: crypto.randomUUID(),intent:"fyi",replyTo });
  }
  async function connect(code = "") {
    const r = await api("connect", { code });
    location.assign(r.url);
  }
  function sign(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = Object.fromEntries(new FormData(e.currentTarget));
    void task(async () => {
      await api(s?.setup ? "setup" : "login", f);
      await init();
    });
  }
  const feedback = (
    <>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {notice && (
        <p role="status" className="notice">
          {notice}
        </p>
      )}
    </>
  );
  if (!s)
    return (
      <main className="gate">
        <Brand />
        <p>{error || "Opening your inbox…"}</p>
        {error && <button onClick={() => void task(init)}>Retry</button>}
      </main>
    );
  if (!s.name)
    return (
      <main className="gate">
        <Brand />
        <section className="card auth">
          <Mator pose="greeting"/>
          <h1>{s.setup ? "Set up your inbox" : "Welcome back"}</h1>
          <p>
            {s.setup
              ? "Create the owner account for this workspace."
              : "Sign in to your workspace."}
          </p>
          <form onSubmit={sign}>
            {s.setup && (
              <label>
                Setup code
                <input
                  name="code"
                  id="setup-code"
                  required
                  autoComplete="off"
                />
                <small>From the setup-code file shown by the installer.</small>
              </label>
            )}
            <label>
              Username
              <input
                name="username"
                autoComplete="username"
                maxLength={64}
                required
              />
            </label>
            <label>
              Password
              <input
                type="password"
                autoComplete={s.setup ? "new-password" : "current-password"}
                required
                minLength={12}
                name="password"
              />
              {s.setup && <small>At least 12 characters.</small>}
            </label>
            <button disabled={busy} className="primary">
              {s.setup ? "Create account" : "Sign in"}
              <ArrowRight size={16} />
            </button>
          </form>
          {s.openpassword && (
            <button
              className="op"
              disabled={busy}
              onClick={() =>
                void task(() =>
                  connect(
                    (document.getElementById("setup-code") as HTMLInputElement)
                      ?.value,
                  ),
                )
              }
            >
              <KeyRound size={16} />
              Continue with OpenPassword
            </button>
          )}
          {!s.openpassword && s.setup && <ConnectHelp />}
          {feedback}
        </section>
      </main>
    );
  if (!acc)
    return (
      <main className="gate">
        <Brand />
        {feedback}
        <button onClick={() => void task(init)}>Load workspace</button>
      </main>
    );
  const previousPath = typeof window==="undefined" ? "" : String(history.state?.from??"");
  const fromBox = /^\/inboxes\/([^/?]+)/.exec(previousPath)?.[1];
  const readerBox = fromBox || (selected?.row.dst.includes("@") || selected?.row.src===acc.operator ? selected?.row.src : selected?.row.dst) || acc.operator;
  const currentThread=threadInfo.key===pathname?threadInfo:null;
  const titleText=currentThread?.title.trim()??"";
  const title=splitMail(titleText).subject||titleText.split(/\r?\n/).find(line=>line.trim()&&!/^(?:Priority|Final|Kind|Record-Ref|Record-Branch|Probe|Auto): /.test(line))||"Untitled conversation";
  function refreshMail(){setRevision(n=>n+1);void task(()=>load())}
  function mailRows(){return <>
                {threadId && (cursor||threadInfo.newer) && <nav className="thread-nav" aria-label="Messages in this window">
                  <span>{term?"Search results":zoom?"Newest first":"All messages"}</span>
                  <span className="thread-steps"><button disabled={!cursor||busy} onClick={()=>browse(term,false,cursor!)}>← Older</button>
                  <button disabled={!threadInfo.newer||busy} onClick={()=>browse(term,false,threadInfo.newer,"after")}>Newer →</button></span>
                </nav>}
                {!zoom && (threadId||term||requests||before) && <div className="results-line" role="status">{term?"Matches for “"+term+"”":requests?"Open requests":threadId?"Newest first":"Older conversations"} · {rows.length} on this page</div>}
                <div className={!threadId&&compact?"compact-list":""}>
                <MailList key={path} rows={rows} box={box} sent={folder==="sent"} timeline={!!threadId} query={term} anchor={anchor} now={now} follow={follow}/></div>

                {!rows.length && (
                  <div className="empty">
                    <Mator pose="searching"/>
                    <h2>{busy ? "Loading messages…" : term ? "No matching messages" : requests ? "No requests awaiting a reply" : zoom ? "No messages in this period" : "No messages yet"}</h2>
                    {!busy && !term && !requests && !agentNames.length && (
                      <button onClick={() => go("agents")}>
                        Add your first agent
                      </button>
                    )}
                  </div>
                )}
  </>}
  return (
    <main className="shell">
      <aside className={menu?"expanded":""}>
        <Brand /><button className="mobile-menu" aria-expanded={menu} onClick={()=>setMenu(!menu)}><Menu size={17}/> Mailboxes</button>
        <div className="workspace">
          <small>WORKSPACE</small>
          <strong>{s.name}</strong>
        </div>
        <button className="primary" onClick={() => compose()}>
          <PenLine size={16} />
          Compose
        </button>
        <nav>
          {(["inbox", "sent", "agents", "activity","mail-setup","settings"] as const).map(
            (v) => (
              <button
                key={v}
                aria-current={folder === v ? "page" : undefined}
                onClick={() => go(v as typeof folder)}
              >
                {v === "inbox" ? (
                  <Inbox />
                ) : v === "sent" ? (
                  <Send />
                ) : (
                  v === "activity" ? <Activity /> : v==="settings"?<Settings/>:v==="mail-setup"?<Settings2/>:<Users />
                )}
                {v === "inbox" ? "Inbox" : v === "sent" ? "Sent" : v === "activity" ? "Activity" : v==="mail-setup"?"Mail setup":v==="settings"?"Settings":"Agents"}
              </button>
            ),
          )}
        </nav>
        <section className="directory" aria-label="Inbox directory">
          <label>Find an inbox<input type="search" value={filter} placeholder="Search by name" onChange={e=>setFilter(e.target.value)} /></label>
          <div className="directory-list">
            {[acc.operator,...agentNames].filter(n=>n.toLowerCase().includes(filter.trim().toLowerCase())).map(n=>(
              <a key={n} href={"/inboxes/"+encodeURIComponent(n)+(folder==="sent"?"/sent":"")} onClick={follow} aria-current={box===n && !messageId && folder==="inbox" ? "page" : undefined}>{n}</a>
            ))}
            {![acc.operator,...agentNames].some(n=>n.includes(filter.trim().toLowerCase())) && <span>No matching inboxes</span>}
          </div>
        </section>
        <footer>
          {s.openpassword && !s.linked && (
            <button onClick={() => void task(() => connect())}>
              <KeyRound />
              Link OpenPassword
            </button>
          )}
          <button
            onClick={() =>
              void task(async () => {
                await api("logout", {});
                location.replace("/");
              })
            }
          >
            <LogOut />
            Sign out
          </button>
        </footer>
      </aside>
      <section className="desk">
        {threadId?<header className="conversation-chrome">
          <a className="conversation-inbox" href={"/inboxes/"+box} onClick={follow}>
            <Inbox size={19} aria-hidden="true"/><span><small>Inbox</small><strong>{box}</strong></span>
          </a>
          <button type="button" aria-label="Refresh messages" title="Refresh messages" disabled={busy} onClick={refreshMail}><RefreshCw size={17}/></button>
        </header>:<header>
          <span>Workspace</span><ChevronRight size={14}/>
          {messageId?"Message":folder==="inbox"||folder==="sent"?box:folder}
        </header>}
        <div className="canvas">
          {feedback}
          {messageId && encodeURIComponent(selected?.row.id ?? "") !== messageId ? (<p>{error ? "Message unavailable." : "Opening message…"}</p>) : selected && messageId ? (
            <>
              <button onClick={() => history.state?.from ? history.back() : go("inbox",readerBox)}>
                <ArrowLeft size={16} />
                Back
              </button>
              <article className="card detail reader">
                <h1>{mailTitle(selected.row,selected.body)}</h1>
                <div className="byline"><Avatar name={selected.row.src}/><div><b>{selected.row.src.replace(/^external:/,"")}</b><small>To {selected.row.dst}</small>{selected.row.audience && <details><summary>All {selected.row.audience.length} recipients</summary><ul>{selected.row.audience.map(name=><li key={name}>{name}</li>)}</ul></details>}</div><div className="message-meta"><MailTime value={selected.row.at} now={now} label="Sent"/><Receipt row={selected.row.audience?{...selected.row,dst:selected.row.audience.join(", ")}:selected.row}/></div></div>
                {selected.row.trust==="external" && <p>Internet email · Sender supplied by the email header.</p>}
                <BodyText body={selected.body} external={selected.row.trust==="external"}/>
                <div className="reader-actions">                {selected.row.src !== acc.operator && (
                  <button className="primary reply-action"
                    onClick={() =>
                      compose(selected.row.src.replace(/^external:/,""), "Re: " + selected.row.subject, selected.row.dst===acc.operator?selected.row.id:"")
                    }
                  >
                    <PenLine size={16} />
                    Reply
                  </button>
                )}
{selected.row.thread && <a onClick={follow} href={"/inboxes/"+encodeURIComponent(readerBox)+"/threads/"+encodeURIComponent(selected.row.thread)+(selected.row.position?"?atSeq="+selected.row.position:"")}><MessageSquare size={15}/>View conversation</a>}</div>
                {selected.row.original_available && <button onClick={()=>void task(async()=>{
                  const data=await api("desk/email-original/"+selected.row.id);
                  const blob=new Blob([Uint8Array.from(atob(data.base64),c=>c.charCodeAt(0))],{type:"application/octet-stream"});
                  const url=URL.createObjectURL(blob);const link=document.createElement("a");
                  link.href=url;link.download=selected.row.id+".eml";link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
                })}>Download original email (.eml)</button>}
                {selected.row.email_status && <p>Email transport · {selected.row.email_status}{selected.row.email?.remote_action && <> · Destination reported {selected.row.email.remote_action} ({selected.row.email.remote_status})</>}</p>}
                <details className="receipt-details"><summary>Delivery details</summary><p>Opened by {selected.row.dst} · {selected.row.opened?new Date(selected.row.opened).toLocaleString():"No recorded open"}<br/>First linked reply · {selected.row.replied?new Date(selected.row.replied).toLocaleString():"No recorded reply"}</p></details>
                {selected.row.state&&selected.row.state!=="fyi"&&<p>Request · {selected.row.state}{selected.row.due?" · Due "+new Date(selected.row.due).toLocaleString():""}</p>}
                {selected.row.dst===acc.operator && ["pending","accepted","deferred"].includes(selected.row.state??"")&&<form onSubmit={e=>{e.preventDefault();const fields=new FormData(e.currentTarget);void task(async()=>{const until=String(fields.get("until")||"");await api("desk/request",{id:selected.row.id,action:fields.get("action"),until:until?new Date(until).getTime():0});const row=await api("desk/message/"+messageId);select({row,body:row.body})})}}>
                <label>Request action<select name="action"><option value="accept">Accept</option><option value="defer">Defer until</option><option value="resolve">Resolve</option><option value="decline">Decline</option></select></label><label>Next update (local time)<input type="datetime-local" name="until"/></label><button>Update request</button></form>}
                <div className="reference"><code>{selected.row.id}</code>
                  <button onClick={()=>void task(()=>copy(selected.row.id,"Message ID copied."))}>Copy message ID</button>
                  <button onClick={()=>void task(()=>copy(location.href,"Message link copied."))}>Copy link</button>
                </div>
                <details className="sharing"><summary>Share access</summary><form onSubmit={e=>{e.preventDefault();void task(async()=>{
                  await api("desk/message/"+messageId+"/share",{seat:shareSeat});
                  say(shareSeat+" can now read this message by ID.");setShareSeat("");
                })}}>
                  <label>Grant read access to an agent<input list="share-seats" value={shareSeat} required onChange={e=>setShareSeat(e.target.value)}/></label>
                  <datalist id="share-seats">{agentNames.map(n=><option key={n} value={n}/>)}</datalist>
                  <button disabled={busy}>Grant access</button>
                </form></details>
              </article>
            </>
          ) : folder==="settings" ? <SettingsPage/> : folder==="mail-setup" ? <MailSetup boxes={[acc.operator,...agentNames]} onChange={async()=>accounts(await api("desk/accounts"))}/> : folder === "activity" ? (
            <Workspace data={live} connection={connection} review={()=>go("agents")} openBox={name=>go("inbox",name)}/>
          ) : folder === "compose" ? (
            <>
              <div className="section-title"><h1>New message</h1></div>
              <form
                className="card detail"
                onSubmit={(e) => {
                  e.preventDefault();
                  void task(async () => {
                    let file: {fileName?:string,fileData?:string}={};
                    if(attachment){
                      if(attachment.size>98304 || !attachment.size)throw Error("Choose a file between 1 byte and 96 KiB.");
                      const bytes=new Uint8Array(await attachment.arrayBuffer());
                      let binary="";for(const b of bytes)binary+=String.fromCharCode(b);
                      file={fileName:attachment.name,fileData:btoa(binary)};
                    }
                    await api("desk/send", {...draft,...file,key:draft.key || crypto.randomUUID()});setAttachment(null);
                    go("sent",acc.operator);
                    say(draft.to.includes("@")?"Email queued for delivery.":"Message sent.");
                  });
                }}
              >
                <label>
                  To
                  <input
                    required
                    list="seats"
                    value={draft.to}
                    onChange={(e) => {setAttachment(null);write({ ...draft, to: e.target.value })}}
                    placeholder={acc.operator_email_send?"Inbox name or external email address":"Inbox name"}
                  />
                </label>
                {acc.operator_email_send && draft.to.includes("@") && !draft.to.toLowerCase().endsWith("@"+acc.email_domain) && <label>Attachment (optional, up to 96 KiB)<input type="file" onChange={e=>setAttachment(e.target.files?.[0]??null)}/></label>}
                <label>Message intent<select value={draft.intent} onChange={e=>write({...draft,intent:e.target.value})}><option value="fyi">FYI</option><option value="request">Request · pickup in 15 minutes</option><option value="urgent">Urgent request · pickup in 2 minutes</option></select></label>
                <datalist id="seats">
                  {[acc.operator, ...agentNames].map((n) => (
                    <option key={n}>{n}</option>
                  ))}
                </datalist>
                <label>
                  Subject
                  <input
                    required
                    maxLength={240}
                    value={draft.subject}
                    onChange={(e) =>
                      write({ ...draft, subject: e.target.value })
                    }
                  />
                </label>
                <label>
                  Message
                  <textarea
                    rows={7}
                    required
                    maxLength={48000}
                    value={draft.body}
                    onChange={(e) => write({ ...draft, body: e.target.value })}
                  />
                </label>
                <button className="primary" disabled={busy}>
                  <Send size={16} />
                  Send message
                </button>
              </form>
            </>
          ) : folder === "agents" ? (
            <>
              <div className="section-title"><h1>Agents</h1></div>
              {acc.email_domain&&<p>Internet email addresses use <code>inbox-name@{acc.email_domain}</code>.</p>}
              <AddressEditor/>
              <RevokeList/>
              {live&&<EnrollmentQueue data={live}/>}
              {pending && (
                <section className="card detail">
                  <h2>Enroll {pending.name}?</h2>
                  <p>
                    Match this code to the one shown by your agent:{" "}
                    <code>{pending.code}</code>. Approval gives it this inbox
                    and permission to message other inboxes here.
                  </p>
                  <div>
                    {[true, false].map((approve) => (
                      <button
                        key={String(approve)}
                        disabled={busy}
                        className={approve ? "primary" : ""}
                        onClick={() =>
                          void task(async () => {
                            await api("desk/enrollment", {
                              name: pending.name,
                              code: pending.code,
                              approve,
                            });
                            setPending(null);
                            sessionStorage.removeItem("enrollment");
                            history.replaceState(null, "", "/agents");
                            accounts(await api("desk/accounts"));
                            say(
                              approve
                                ? "Agent enrolled."
                                : "Request dismissed.",
                            );
                          })
                        }
                      >
                        {approve ? "Approve enrollment" : "Dismiss"}
                      </button>
                    ))}
                  </div>
                </section>
              )}
              <p>
                Give a harness its own inbox and access token. A fresh agent can
                follow the{" "}
                <a className="underline" href="/agent-guide.md">
                  self-enrollment guide
                </a>
                .
              </p>
              <form
                className="card detail"
                onSubmit={(e) => {
                  e.preventDefault();
                  const form = e.currentTarget;
                  const name = new FormData(form).get("name");
                  void task(async () => {
                    const x = await api("desk/agents", { name });
                    key(x.credential);
                    accounts(await api("desk/accounts"));
                    form.reset();
                  });
                }}
              >
                <label>
                  Agent name
                  <input
                    name="name"
                    placeholder="research-assistant"
                    required
                    pattern="[a-z0-9][a-z0-9._\-]{0,63}"
                  />
                </label>
                <button className="primary" disabled={busy}>
                  <Plus size={16} />
                  Create agent
                </button>
                {credential && (
                  <label className="issued-credential">
                    Copy this token to your harness now
                    <input
                      readOnly
                      value={credential}
                      onFocus={(e) => e.target.select()}
                    />
                  </label>
                )}
              </form>
              {agentNames.map((n) => (
                <div className="agentrow" key={n}>
                  <strong>{n}</strong><small>{live?.agents.find(([name])=>name===n)?.[1]===0?"Access revoked":live?.watching[live?.agents.find(([name])=>name===n)?.[2]??n]?"Listening now":"Enrolled"}</small>
                  <button
                    onClick={() => {
                      go("inbox",n);
                    }}
                  >
                    Open inbox
                  </button>
                  <button
                    onClick={() =>
                      void task(async () => {
                        const x = await api("desk/rotate", { name: n });
                        key(x.credential);
                        say("New token issued for " + n);
                      })
                    }
                  >
                    Replace token
                  </button>
                  <button
                    onClick={() =>
                      void task(async () => {
                        await api("desk/revoke", { name: n });
                        key("");
                        say("Token revoked for " + n);
                      })
                    }
                  >
                    Revoke token
                  </button>
                </div>
              ))}
            </>
          ) : (
            <>
              {threadId?<section className="conversation-heading" aria-labelledby="conversation-subject">
                <p className="conversation-kind">{isOutline?"Timeline":"Conversation"}</p>
                <h1 id="conversation-subject">{currentThread?title:busy?"Loading conversation…":"Conversation"}</h1>
                <div className="conversation-overview">{!isOutline && currentThread && <dl className="conversation-counts" aria-label="Messages in this conversation">
                  <div><dt><ArrowDownLeft size={15} aria-hidden="true"/>Received</dt><dd>{currentThread.received.toLocaleString()}</dd></div>
                  <div><dt><ArrowUpRight size={15} aria-hidden="true"/>Sent</dt><dd>{currentThread.sent.toLocaleString()}</dd></div>
                </dl>}
                <a className="screen-link" href={isOutline?conversationPath:timelinePath} onClick={follow}>{isOutline?<ArrowLeft size={16}/>:<Layers size={16}/>} {isOutline?"Back to conversation":"Timeline"}</a></div>
              </section>:<div className="mail-heading">
                <div><h1>{folder==="sent"?"Sent":"Inbox"}</h1><p>{box}</p></div>
                <button aria-label="Refresh messages" disabled={busy} onClick={refreshMail}><RefreshCw size={16}/></button>
              </div>}
              <div className="mail-panel">
                {!isOutline && <MailTools thread={!!threadId} query={term} requests={requests} sent={folder==="sent"} compact={compact} toggle={toggleDensity} apply={browse}/>}
                {isOutline?<ThreadTime key={box+"/"+threadId} api={"/api/desk/threads/"+threadId+"?box="+encodeURIComponent(box)} level={level} at={level?Number(start):0} choose={chooseTime} revision={revision}>{mailRows()}</ThreadTime>:mailRows()}
                {!threadId && before && <button onClick={()=>browse(term,requests)}>Back to newest</button>}
                {!threadId && cursor && (
                  <button
                    disabled={busy}
                    onClick={() => browse(term,requests,cursor)}
                  >
                    Load older messages
                  </button>
                )}
              </div>
            </>
          )}
        </div>
      </section>
    </main>
  );
}
