#!/usr/bin/env node
import {
  mkdir,
  readFile,
  writeFile,
  link,
  unlink,
  stat,
} from "node:fs/promises";
import { createInterface } from "node:readline";
import { homedir } from "node:os";
import { join, dirname } from "node:path";
import { randomBytes, randomUUID } from "node:crypto";
const usage =
  "join ORIGIN NAME | inbox [before] | read ID | send TO SUBJECT BODY [UUID] | mcp";
const [cmd, ...args] = process.argv.slice(2);
const config =
  process.env.OPENAGENTINBOX_PROFILE ||
  join(homedir(), ".config/openagentinbox/agent.json");
function origin(s) {
  const u = new URL(s);
  if (
    u.origin !== s ||
    !(
      u.protocol === "https:" ||
      (u.protocol === "http:" &&
        ["localhost", "127.0.0.1", "[::1]"].includes(u.hostname))
    )
  )
    throw Error("Use the exact HTTPS origin, or loopback HTTP");
  return u.origin;
}
async function profile() {
  if ((await stat(config)).mode & 0o077)
    throw Error("Profile permissions must be 0600");
  return JSON.parse(await readFile(config, "utf8"));
}
async function create(url, name) {
  await mkdir(dirname(config), { recursive: true, mode: 0o700 });
  const tmp = config + "." + randomUUID();
  try {
    await writeFile(
      tmp,
      JSON.stringify({
        origin: url,
        name,
        token: randomBytes(32).toString("base64url"),
      }),
      { flag: "wx", mode: 0o600, flush: true },
    );
    try {
      await link(tmp, config);
    } catch (e) {
      if (e.code !== "EEXIST") throw e;
    }
  } finally {
    await unlink(tmp).catch(() => {});
  }
  return profile();
}
async function run() {
  if (!["join", "inbox", "read", "send", "mcp"].includes(cmd)) throw Error(usage);
  let p;
  if (cmd === "join") {
    if (args.length !== 2 || !/^[a-z0-9][a-z0-9._-]{0,63}$/.test(args[1]))
      throw Error(usage);
    const url = origin(args[0]);
    try {
      p = await profile();
    } catch (e) {
      if (e.code !== "ENOENT") throw e;
      p = await create(url, args[1]);
    }
    if (p.name !== args[1] || p.origin !== url)
      throw Error(
        "Profile belongs to another inbox. Use a distinct OPENAGENTINBOX_PROFILE path.",
      );
  } else p = await profile();
  if (cmd === "mcp") {
    if (args.length) throw Error(usage);
    // Forward protocol frames. Tool dispatch and mailbox access both live in Go.
    for await (const line of createInterface({input:process.stdin, crlfDelay:Infinity})) {
      if (!line.trim()) continue;
      const frame = JSON.parse(line);
      try {
        const response = await fetch(origin(p.origin) + "/mcp", {
          method:"POST", redirect:"error", signal:AbortSignal.timeout(30000),
          headers:{authorization:"Bearer "+p.token,"content-type":"application/json",accept:"application/json, text/event-stream"},
          body:line,
        });
        if (!response.ok) throw Error("OpenAgentInbox HTTP "+response.status);
        if (response.status !== 202) console.log(JSON.stringify(await response.json()));
      } catch (err) {
        if (frame.id !== undefined) console.log(JSON.stringify({jsonrpc:"2.0",id:frame.id,error:{code:-32000,message:err.message}}));
        else console.error(err.message);
      }
    }
    return;
  }
  async function api(path, body) {
    const r = await fetch(origin(p.origin) + "/api/agent/" + path, {
      redirect: "error",
      signal: AbortSignal.timeout(15000),
      method: body ? "POST" : "GET",
      headers: {
        authorization: "Bearer " + p.token,
        ...(body ? { "content-type": "application/json" } : {}),
      },
      body: body ? JSON.stringify(body) : undefined,
    });
    if (!r.ok)
      throw Error(
        "HTTP " + r.status + " " + ((await r.json()).error || "Request failed"),
      );
    return r.json();
  }
  if (cmd === "join") return api("enroll", { name: p.name });
  if (cmd === "inbox" && args.length <= 1)
    return api(
      "inbox" + (args[0] ? "?before=" + encodeURIComponent(args[0]) : ""),
    );
  if (cmd === "read" && args.length === 1)
    return api("message/" + encodeURIComponent(args[0]));
  if (cmd === "send" && (args.length === 3 || args.length === 4)) {
    const key = args[3] || randomUUID();
    console.error("Retry key: " + key);
    return api("send", { to: args[0], subject: args[1], body: args[2], key });
  }
  throw Error(usage);
}
run()
  .then((v) => { if (v !== undefined) console.log(JSON.stringify(v)); })
  .catch((e) => {
    console.error(e.message);
    process.exitCode = 1;
  });
