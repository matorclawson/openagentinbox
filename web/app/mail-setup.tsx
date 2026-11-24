"use client";
import {useEffect,useState} from "react";

type Plan={domain:string,mx:string,ip:string,selector:string,proof:string,active:boolean};
type Setup={domains:Plan[],edge:boolean,relay:boolean,host_domain:string,primary:string};
type RecordCheck={Type:string,Name:string,Value:string,Help:string,OK:boolean,Error?:string};
type Addresses={box:string,names:string[],domain:string,may_send:boolean};
async function request(path:string,body?:object){
 const r=await fetch("/api/desk/email/"+path,{cache:"no-store",method:body?"POST":"GET",headers:body?{"Content-Type":"application/json"}:{},body:body?JSON.stringify(body):undefined});
 const data=await r.json();if(!r.ok)throw Error(data.error??"Request failed");return data;
}
function Copy({value}:{value:string}){
 const [state,set]=useState("Copy");
 return <button type="button" aria-label={"Copy "+value} onClick={async()=>{try{await navigator.clipboard.writeText(value);set("Copied")}catch{set("Select and copy")}}}>{state}</button>;
}
export function MailSetup({boxes,onChange}:{boxes:string[],onChange:()=>Promise<void>}){
 const [setup,setSetup]=useState<Setup|null>(null),[domain,choose]=useState(""),[box,setBox]=useState(boxes[0]??"operator"),[records,setRecords]=useState<RecordCheck[]>([]),[addr,setAddr]=useState<Addresses|null>(null),[alias,setAlias]=useState(""),[err,setErr]=useState(""),[busy,wait]=useState(false),[notice,say]=useState("");
 const plan=setup?.domains.find(p=>p.domain===domain);
 async function act(fn:()=>Promise<void>){wait(true);setErr("");say("");try{await fn()}catch(e){setErr(String((e as Error).message))}finally{wait(false)}}
 useEffect(()=>{let active=true;request("setup").then((s:Setup)=>{if(active){setSetup(s);choose(s.domains[0]?.domain??"")}}).catch(e=>active&&setErr(e.message));return()=>{active=false}},[]);
 useEffect(()=>{let active=true;setAddr(null);request("addresses?box="+encodeURIComponent(box)).then(v=>active&&setAddr(v)).catch(e=>active&&setErr(e.message));return()=>{active=false}},[box,setup]);
 useEffect(()=>{setRecords([])},[domain]);
 return <div className="mail-setup"><div className="section-title"><h1>Mail setup</h1></div>
 <p>Connect a domain you own to give each agent an email address. Your internal inboxes already work.</p>
 {err&&<p role="alert">{err}</p>}{notice&&<p role="status">{notice}</p>}
 <section className="card detail"><h2>1. Choose where your agents receive email</h2>
 <p>If your company uses example.com for everyday mail, try agents.example.com. Changing the MX record for your main domain can redirect everyone’s email.</p>
 {setup?.host_domain&&<p>Already configured on the server: <strong>{setup.host_domain}</strong></p>}
 {setup&&setup.domains.length>0&&<label>Configured domains<select value={domain} onChange={e=>choose(e.target.value)}>{setup.domains.map(p=><option key={p.domain}>{p.domain}</option>)}</select></label>}
 <form onSubmit={e=>{e.preventDefault();const f=new FormData(e.currentTarget);void act(async()=>{const s=await request("domains",Object.fromEntries(f));setSetup(s);choose(String(f.get("domain")).trim().toLowerCase().replace(/\.$/,""));say("Domain settings saved.")})}}>
 <label>Email domain<input name="domain" placeholder="agents.example.com" required/></label>
 <label>Public IP of your mail server<input name="ip" placeholder="From your VPS dashboard" required/></label>
 <details><summary>Custom mail hostname and DKIM selector</summary><label>Mail hostname<input name="mx" placeholder="Defaults to mx.your-domain"/></label><label>Signing key selector<input name="selector" placeholder="mail"/></label></details>
 <button disabled={busy}>Save domain</button></form></section>
 {plan&&<><section className="card detail"><h2>2. Connect your mail server</h2>
 <p>{setup?.edge?"OpenAgentInbox’s local receiving socket is running.":"The mail transport is offline. On a fresh dedicated Debian or Ubuntu VPS with OpenAgentInbox already installed, run the command below as a server administrator."}</p>
 <p>Run this on the VPS that will receive email. It installs Postfix and a DKIM signer, configures the private connection and prints your public signing record. For an existing mail server, use the manual guide.</p>
 <pre>{'sudo bash /opt/openagentinbox/active/ui/setup-mail-edge.sh '+plan.domain+' '+plan.mx+' '+plan.selector}</pre><Copy value={'sudo bash /opt/openagentinbox/active/ui/setup-mail-edge.sh '+plan.domain+' '+plan.mx+' '+plan.selector}/>
 <p><a href="/email-setup.md">Existing mail server instructions</a></p>
 <button disabled={busy} onClick={()=>void act(async()=>setSetup(await request("setup")))}>Refresh connection</button></section>
 <section className="card detail"><h2>3. Add records at your DNS provider</h2><p>Open the site where you manage the domain, find DNS records and choose Add record. The names below are full names. If your provider appends your zone automatically, enter only the part before that zone. Use the default TTL.</p>
 <p>Start with the ownership TXT and server IP. Activate the domain below before pointing incoming mail at it with MX. For MX enter priority 10. For the DKIM value use the public record printed by the server.</p>
 {records.length===0&&<p>No DNS lookup has been made. OpenAgentInbox queries DNS only when you press Check DNS or Activate receiving. Activating looks up the ownership record again.</p>}
 {records.map(r=><section key={r.Name+r.Type} className="dns-record"><strong>{r.Type} · {r.OK?"Found":r.Error?"Lookup unavailable":"Awaiting record"}</strong><label>Name <code>{r.Name}</code></label><Copy value={r.Name}/>{r.Value&&<><label>Value <code>{r.Value}</code></label><Copy value={r.Value}/></>}<p>{r.Help}</p></section>)}
 <button disabled={busy} onClick={()=>void act(async()=>{setRecords((await request("domains/"+domain+"/check",{})).records);say("DNS checked.")})}>Check DNS</button>
 <button disabled={busy||plan.active||!setup?.edge||!records[0]?.OK} onClick={()=>void act(async()=>{setSetup(await request("domains/"+domain+"/activate",{}));await onChange();say("Domain activated.")})}>{plan.active?"Domain active":"Activate receiving"}</button>
 {plan.active&&setup?.primary!==domain&&<button disabled={busy} onClick={()=>void act(async()=>{setSetup(await request("domains/"+domain+"/primary",{}));await onChange();say("Default sender domain updated.")})}>Use as default address domain</button>}
 <p>After activation, publish the MX record. Allow inbound TCP port 25 in your VPS firewall. Send one email from another provider and reply to it to confirm delivery in both directions.</p></section></>}
 <section className="card detail"><h2>Agent addresses and sending permissions</h2>
 <label>Inbox<select value={box} onChange={e=>setBox(e.target.value)}>{boxes.map(n=><option key={n}>{n}</option>)}</select></label>
 {addr&&<><ul>{addr.names.map((n,i)=><li key={n}><code>{n}@{addr.domain||domain||"your-domain"}</code> · {i===0?"Primary":"Alias"}</li>)}</ul>
 {addr.domain&&addr.names.length>0&&<p><a href={"mailto:"+addr.names[0]+"@"+addr.domain+"?subject=Inbox%20test"}>Send a test email from my email app</a></p>}
 <form onSubmit={e=>{e.preventDefault();void act(async()=>{await request("aliases",{box,alias});setAddr(await request("addresses?box="+encodeURIComponent(box)));setAlias("");say("Alias added to this inbox.")})}}><label>Additional name<input required value={alias} onChange={e=>setAlias(e.target.value)} placeholder="customer-support"/></label><button disabled={busy}>Add alias</button></form>
 <label><input type="checkbox" checked={addr.may_send} disabled={busy||!setup?.relay||!setup?.primary} onChange={e=>{const enabled=e.target.checked;void act(async()=>{await request("sender",{box,enabled});await onChange();setAddr(await request("addresses?box="+encodeURIComponent(box)))})}}/> Allow this inbox to send Internet email</label></>}
 </section></div>
}
