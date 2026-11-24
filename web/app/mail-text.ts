// Historical mail carries envelope fields as a contiguous prefix. Only strip
// recognized fields at the beginning. Preserve all other text verbatim.
export function splitMail(text:string) {
  const prefix=/^(?:(?:Subject|Priority|Final|Kind|Record-Ref|Record-Branch|Probe|Auto): [^\n]*(?:\n|$))+/;
  const headers=text.match(prefix)?.[0]??"";
  const subject=/^Subject: ([^\r\n]+)/m.exec(headers)?.[1]?.trim()??"";
  const priority=/^Priority: (p[012])\r?$/m.exec(headers)?.[1];
  const body=text.slice(headers.length).replace(/^\s*\n/,"");
  const first=body.trimStart().split(/\r?\n/,1)[0]??"";
  const chars=Array.from(first);
  const lead=chars.length>100?chars.slice(0,100).join("")+"…":first;
  return {headers,subject,priority,body,lead};
}
