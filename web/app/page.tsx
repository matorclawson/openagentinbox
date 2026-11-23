"use client";
import { useEffect, useState, useRef, type FormEvent } from "react";
import {
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
import { Workspace, EnrollmentQueue, useWorkspace } from "./live";
type Session = {
  name: string | null;
  setup: boolean;
  openpassword: boolean;
  linked: boolean;
};
type Accounts = { operator: string; agents: string[]; email_domain?: string;operator_email_send?: boolean };
type Row = {
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
      <Inbox size={25} />
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
  const [path, setPath] = useState("/");
  const [filter, setFilter] = useState("");
  const [shareSeat, setShareSeat] = useState("");
  const parts = path.split("/");
  const box = parts[1] === "inboxes" ? parts[2] : acc?.operator ?? "operator";
  const folder = parts[1] === "activity" ? "activity" : parts[1] === "compose" ? "compose" : parts[1] === "agents" ? "agents" : parts[3] === "sent" ? "sent" : "inbox";
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
  const newest=live?.mail[0]?.[4];
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
      navigate(location.pathname === "/" ? "/inboxes/" + a.operator : location.pathname, true);
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
    const restore = () => { setPath(location.pathname); warn(""); say(""); key(""); };
    window.addEventListener("popstate", restore);
    void task(init);
    return () => window.removeEventListener("popstate", restore);
  }, []);
  async function load(more = false) {
    const gen = ++generation.current;
    const q = new URLSearchParams({
      box,
      folder,
      ...(more && cursor ? { before: cursor } : {}),
    });
    const data = await api("desk/list?" + q);
    if (gen === generation.current) {
      list((old) => (more ? [...old, ...data.rows] : data.rows));
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
  }, [folder, box, messageId, s?.name, acc]);
  useEffect(()=>{
    if(messageId||!newest||!s?.name||(folder!=="inbox"&&folder!=="sent"))return;
    const stamp=generation.current;
    const q=new URLSearchParams({box,folder});
    let cancelled=false;
    void api("desk/list?"+q).then(data=>{
      if(cancelled||stamp!==generation.current)return;
      list(old=>{
        const seen=new Set(data.rows.map((r:Row)=>r.id));
        // Keep older pages only if they overlap the freshly fetched first page.
        if(old.some(r=>seen.has(r.id)))return [...data.rows,...old.filter(r=>!seen.has(r.id))];
        return data.rows;
      });
      if(!cursor||!rows.some(r=>data.rows.some((v:Row)=>v.id===r.id)))next(data.next);
    }).catch(()=>{});
    return ()=>{cancelled=true};
  },[newest,folder,box,messageId,s?.name]);
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
      if(active)select(old=>old && encodeURIComponent(old.row.id)===messageId?{...old,row:{...old.row,...receipt,email_status:receipt.email?.state??old.row.email_status}}:old);
    }).catch(()=>{});
    return ()=>{active=false};
  },[live?.cursor,messageId,s?.name]);
  function navigate(to: string, replace = false) {
    if (replace) history.replaceState(null, "", to);
    else if (location.pathname !== to) history.pushState({from:location.pathname}, "", to);
    setPath(to);
    key(""); warn(""); say("");
  }
  function go(v: typeof folder, seat = box) {
    navigate(v === "activity" || v === "compose" || v === "agents" ? "/" + v : "/inboxes/" + encodeURIComponent(seat) + (v === "sent" ? "/sent" : ""));
  }
  function follow(e: React.MouseEvent<HTMLAnchorElement>) {
    if (!e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey && e.button === 0) {
      e.preventDefault(); navigate(e.currentTarget.pathname);
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
          <Mail className="icon" />
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
  return (
    <main className="shell">
      <aside>
        <Brand />
        <div className="workspace">
          <small>WORKSPACE</small>
          <strong>{s.name}</strong>
        </div>
        <button className="primary" onClick={() => compose()}>
          <PenLine size={16} />
          Compose
        </button>
        <nav>
          {(["inbox", "sent", "agents", "activity"] as const).map(
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
                  v === "activity" ? <Activity /> : <Users />
                )}
                {v === "inbox" ? "Inbox" : v === "sent" ? "Sent" : v === "activity" ? "Activity" : "Agents"}
              </button>
            ),
          )}
        </nav>
        <section className="directory" aria-label="Inbox directory">
          <label>Find an inbox<input type="search" value={filter} placeholder="Search by name" onChange={e=>setFilter(e.target.value)} /></label>
          <div className="directory-list">
            {[acc.operator,...agentNames].filter(n=>n.toLowerCase().includes(filter.trim().toLowerCase())).map(n=>(
              <a key={n} href={"/inboxes/"+encodeURIComponent(n)} onClick={follow} aria-current={box===n && !messageId && folder==="inbox" ? "page" : undefined}>{n}</a>
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
        <header>
          <span>Workspace</span>
          <span>/</span>
          OpenAgentInbox
        </header>
        <div className="canvas">
          {feedback}
          {messageId && encodeURIComponent(selected?.row.id ?? "") !== messageId ? (<p>{error ? "Message unavailable." : "Opening message…"}</p>) : selected && messageId ? (
            <>
              <button onClick={() => history.state?.from ? history.back() : go("inbox",selected.row.dst.split(", ")[0])}>
                <ArrowLeft size={16} />
                Back
              </button>
              <article className="card detail">
                <h1>{selected.row.subject}</h1>
                <p>
                  {selected.row.src} → {selected.row.dst} ·{" "}
                  {new Date(selected.row.at).toLocaleString()}
                </p>
                {selected.row.trust==="external" && <p>Internet email · Sender supplied by the email header.</p>}
                <pre>{selected.body}</pre>
                {selected.row.original_available && <button onClick={()=>void task(async()=>{
                  const data=await api("desk/email-original/"+selected.row.id);
                  const blob=new Blob([Uint8Array.from(atob(data.base64),c=>c.charCodeAt(0))],{type:"application/octet-stream"});
                  const url=URL.createObjectURL(blob);const link=document.createElement("a");
                  link.href=url;link.download=selected.row.id+".eml";link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
                })}>Download original email (.eml)</button>}
                {selected.row.email_status && <p>Email transport · {selected.row.email_status}{selected.row.email?.remote_action && <> · Destination reported {selected.row.email.remote_action} ({selected.row.email.remote_status})</>}</p>}
                <p>Opened by recipient · {selected.row.opened?new Date(selected.row.opened).toLocaleString():"No recorded open"}<br/>First linked reply · {selected.row.replied?new Date(selected.row.replied).toLocaleString():"No recorded reply"}</p>
                {selected.row.state&&selected.row.state!=="fyi"&&<p>Request · {selected.row.state}{selected.row.due?" · Due "+new Date(selected.row.due).toLocaleString():""}</p>}
                {selected.row.dst===acc.operator && ["pending","accepted","deferred"].includes(selected.row.state??"")&&<form onSubmit={e=>{e.preventDefault();const fields=new FormData(e.currentTarget);void task(async()=>{const until=String(fields.get("until")||"");await api("desk/request",{id:selected.row.id,action:fields.get("action"),until:until?new Date(until).getTime():0});const row=await api("desk/message/"+messageId);select({row,body:row.body})})}}>
                <label>Request action<select name="action"><option value="accept">Accept</option><option value="defer">Defer until</option><option value="resolve">Resolve</option><option value="decline">Decline</option></select></label><label>Next update (local time)<input type="datetime-local" name="until"/></label><button>Update request</button></form>}
                <div className="reference"><code>{selected.row.id}</code>
                  <button onClick={()=>void task(()=>copy(selected.row.id,"Message ID copied."))}>Copy message ID</button>
                  <button onClick={()=>void task(()=>copy(location.href,"Message link copied."))}>Copy link</button>
                </div>
                <form onSubmit={e=>{e.preventDefault();void task(async()=>{
                  await api("desk/message/"+messageId+"/share",{seat:shareSeat});
                  say(shareSeat+" can now read this message by ID.");setShareSeat("");
                })}}>
                  <label>Grant read access to an agent<input list="share-seats" value={shareSeat} required onChange={e=>setShareSeat(e.target.value)}/></label>
                  <datalist id="share-seats">{agentNames.map(n=><option key={n} value={n}/>)}</datalist>
                  <button disabled={busy}>Grant access</button>
                </form>
                {selected.row.src !== acc.operator && (
                  <button
                    onClick={() =>
                      compose(selected.row.src.replace(/^external:/,""), "Re: " + selected.row.subject, selected.row.dst===acc.operator?selected.row.id:"")
                    }
                  >
                    <PenLine size={16} />
                    Reply
                  </button>
                )}
              </article>
            </>
          ) : folder === "activity" ? (
            <Workspace data={live} connection={connection} review={()=>go("agents")} openBox={name=>go("inbox",name)}/>
          ) : folder === "compose" ? (
            <>
              <h1>New message</h1>
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
              <h1>Agents</h1>
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
                  <label>
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
              <div className="heading">
                <h1>{folder === "sent" ? "Sent" : "Inbox"}</h1>
                <button
                  aria-label="Refresh messages"
                  disabled={busy}
                  onClick={() => void task(() => load())}
                >
                  <RefreshCw size={17} />
                </button>
              </div>
              <div className="card mailbox">
                <div className="toolbar">
                  <strong>
                    {folder === "sent" ? "Sent by" : "Received by"}
                  </strong>
                  <strong>{box}</strong>
                </div>
                {rows.map((r) => (
                  <a className="letter" key={r.id} href={"/messages/"+encodeURIComponent(r.id)} onClick={follow}>
                    <Mail size={18} />
                    <span>
                      <strong>{r.subject}</strong>
                      <small>{folder === "sent" ? r.dst : r.src}</small>
                    </span>
                    <time>{new Date(r.at).toLocaleDateString()}</time>
                  </a>
                ))}
                {!rows.length && (
                  <div className="empty">
                    <Inbox />
                    <h2>{busy ? "Loading messages…" : "No messages yet"}</h2>
                    {!busy && !agentNames.length && (
                      <button onClick={() => go("agents")}>
                        Add your first agent
                      </button>
                    )}
                  </div>
                )}
                {cursor && (
                  <button
                    disabled={busy}
                    onClick={() => void task(() => load(true))}
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
