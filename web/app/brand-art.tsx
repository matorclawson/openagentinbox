export function Mator({pose}:{pose:"greeting"|"searching"}) {
  return <img className="mator" src={"/brand/mator-"+pose+".webp"} alt="" width="32" height="32"/>;
}
