type Pose="reading"|"greeting"|"rocket"|"chatting"|"searching"|"working"|"integrating";

export function Mator({pose,className=""}:{pose:Pose;className?:string}){
  return <img className={"mator "+className} src={"/brand/mator-"+pose+".webp"} width="160" height="160" alt="" aria-hidden="true"/>;
}

// Static forms from the shared RetroShapes primitives.
export function MailArt({pose}:{pose:Pose}){
  return <div className="mail-art" aria-hidden="true">
    <svg viewBox="0 0 220 150" className="retro-shapes">
      <path transform="translate(1 -9) scale(2.25 2.85)" d="M20 30 Q 10 10, 40 15 Q 70 20, 80 10 Q 100 5, 90 30 Q 95 55, 60 50 Q 30 55, 20 30Z" fill="var(--color-secondary)" opacity=".23"/>
      <path transform="translate(2 55) scale(.65)" d="M5 45 Q 25 5, 50 25 Q 75 45, 95 5" fill="none" stroke="var(--color-primary)" strokeWidth="4" strokeLinecap="round"/>
      <g transform="translate(183 33)" stroke="var(--color-accent)" strokeLinecap="round">
        {Array.from({length:12},(_,i)=><path key={i} transform={`rotate(${i*30})`} d={i%2?"M0 0v-10":"M0 0v-15"} strokeWidth={i%2?2:3}/>)}
      </g>
    </svg><Mator pose={pose}/></div>;
}
