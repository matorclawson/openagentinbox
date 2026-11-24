import {useEffect,useState} from "react";
import {Monitor,Sun,Moon,Server} from "lucide-react";

const choices=[["system","System",Monitor],["light","Light",Sun],["dark","Dark",Moon]] as const;
export function SettingsPage(){
  const [theme,setTheme]=useState("system");
  const [version,setVersion]=useState("");
  const [attempt,retry]=useState(0);
  const [failed,fail]=useState(false);
  useEffect(()=>{
    const root=document.documentElement;
    const sync=()=>setTheme(root.dataset.appearance || "system");
    sync();
    const observer=new MutationObserver(sync);
    observer.observe(root,{attributes:true,attributeFilter:["data-appearance"]});
    return ()=>observer.disconnect();
  },[]);
  useEffect(()=>{
    const abort=new AbortController();
    fail(false);setVersion("");
    fetch("/api/health",{cache:"no-store",signal:abort.signal})
      .then(async res=>{
        if(!res.ok)throw Error();
        const data=await res.json();
        if(data.product!=="openagentinbox" || typeof data.version!=="string" || !data.version)throw Error();
        if(!abort.signal.aborted)setVersion(data.version);
      }).catch(()=>{if(!abort.signal.aborted)fail(true)});
    return ()=>abort.abort();
  },[attempt]);
  return <section className="preferences"><h1>Settings</h1>
    <div className="card"><fieldset><legend>Appearance</legend><p>Choose a theme for this browser. System follows your device.</p>
      <div className="theme-choices">{choices.map(([value,label,Icon])=><label key={value}>
        <input type="radio" name="appearance" value={value} checked={theme===value}
          onChange={()=>window.dispatchEvent(new CustomEvent("inbox-appearance",{detail:value}))}/>
        <Icon size={18}/>{label}</label>)}</div>
    </fieldset></div>
    <div className="card installation"><h2><Server size={18}/>Installation</h2>
      <div><span>Installed server version</span><output aria-live="polite">{version || (failed?"Unavailable":"Checking…")}</output></div>
      {failed && <button onClick={()=>retry(n=>n+1)}>Check again</button>}
    </div>
  </section>;
}
