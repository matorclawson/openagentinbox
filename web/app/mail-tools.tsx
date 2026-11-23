import {useEffect,useRef,useState} from "react";
import {Search,List,AlignLeft,X} from "lucide-react";

type Props = {query:string; requests:boolean; sent:boolean; compact:boolean; toggle:()=>void; apply:(query:string,requests:boolean)=>void};
export function MailTools({query,requests,sent,compact,toggle,apply}:Props){
  const [draft,setDraft]=useState(query);
  const input=useRef<HTMLInputElement>(null);
  useEffect(()=>setDraft(query),[query]);
  useEffect(()=>{
    function focus(e:KeyboardEvent){
      if(e.key!=="/" || e.ctrlKey || e.altKey || e.metaKey || (e.target instanceof HTMLElement && (e.target.isContentEditable || e.target.closest("input,textarea,select"))))return;
      e.preventDefault();input.current?.focus();
    }
    window.addEventListener("keydown",focus);return ()=>window.removeEventListener("keydown",focus);
  },[]);
  return <div className="mail-tools">
    <form role="search" onSubmit={e=>{e.preventDefault();apply(draft.trim(),requests)}}>
      <Search size={17}/><input ref={input} aria-label="Search messages" placeholder="Search subject, sender, text or message ID" value={draft} maxLength={120} onChange={e=>setDraft(e.target.value)}/>
      {draft && <button type="button" aria-label="Clear search" onClick={()=>{setDraft("");apply("",requests);input.current?.focus()}}><X size={15}/></button>}
      <button type="submit">Search</button>
    </form>
    <div className="mail-controls"><div className="view-choices" aria-label="Mailbox view">
      <button aria-pressed={!requests} onClick={()=>apply(query,false)}>{query?"All matches":"All conversations"}</button>
      {!sent && <button aria-pressed={requests} onClick={()=>apply(query,true)}>Needs reply</button>}
    </div><button className="density" aria-pressed={compact} onClick={toggle} title="Toggle compact rows">{compact?<List size={15}/>:<AlignLeft size={15}/>}Compact</button></div>
  </div>
}
