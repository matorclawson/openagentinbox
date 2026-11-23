export function Orbit({size=28}:{size?:number}) {
  return <svg width={size} height={size} viewBox="0 0 40 40" fill="none" aria-hidden="true">
    <g stroke="currentColor" strokeWidth="1.8"><ellipse cx="20" cy="20" rx="17" ry="6.5" transform="rotate(-32 20 20)"/><ellipse cx="20" cy="20" rx="7" ry="16" transform="rotate(-28 20 20)"/></g>
    <circle cx="20" cy="20" r="4.2" fill="currentColor"/><circle cx="33" cy="11" r="2.4" fill="currentColor"/>
  </svg>;
}
