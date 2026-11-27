// Capture README screenshots from a disposable instance holding synthetic mail only.
// Usage, after the verify-web build steps:  node tests/screenshots.cjs /abs/openagentinboxd /abs/outdir
// Takes about four minutes: it waits for a real two-minute urgent deadline to be missed.
const { chromium } = require("playwright-core");
const fs = require("node:fs"), child = require("node:child_process"), path = require("node:path"), os = require("node:os");
const { once } = require("node:events");
const { createServer } = require("node:net");
const { setTimeout: delay } = require("node:timers/promises");

async function freePort() {
  const s = createServer(); const l = once(s, "listening"); s.listen(0, "127.0.0.1"); await l;
  const { port } = s.address(); await new Promise(r => s.close(r)); return port;
}

(async () => {
  const [daemon, out] = process.argv.slice(2);
  if (!daemon || !out || !path.isAbsolute(daemon) || !path.isAbsolute(out)) throw Error("Usage: screenshots.cjs DAEMON OUTDIR (absolute paths)");
  const arch = process.arch === "x64" ? "amd64" : process.arch;
  const native = path.resolve(__dirname, "../../dist/ui/clients", process.platform + "-" + arch);
  fs.mkdirSync(out, { recursive: true });
  const dir = fs.mkdtempSync("/tmp/oai-shots-");
  const env = { HOME: dir, TMPDIR: dir, PATH: "/usr/bin:/bin" };
  const children = [];
  const start = (cmd, args, extra = {}) => { const p = child.spawn(cmd, args, { env: { ...env, ...extra }, stdio: "ignore" }); children.push(p); return p; };
  let browser;
  try {
    const port = await freePort(), url = "http://127.0.0.1:" + port;
    start(daemon, ["serve"], { INBOX_LISTEN: "127.0.0.1", INBOX_ORIGIN: url, INBOX_PORT: String(port), INBOX_UI: path.resolve(__dirname, "../out"), INBOX_DATA: dir });
    for (let i = 0; ; i++) {
      if ((await fetch(url + "/api/health").catch(() => null))?.ok) break;
      if (i > 100) throw Error("daemon did not start");
      await delay(100);
    }
    const chrome = process.env.CHROME_PATH || "/Applications/Google Chrome.app";
    browser = await chromium.launch({
      executablePath: chrome.endsWith(".app") ? path.join(chrome, "Contents", "MacOS", "Google Chrome") : chrome,
      env,
      ...(process.platform === "linux" ? { args: ["--no-sandbox", "--disable-dev-shm-usage"] } : {}),
    });
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: "light" });
    const page = await context.newPage();
    page.setDefaultTimeout(15000);
    await page.goto(url);
    await page.getByLabel("Setup code").fill(fs.readFileSync(path.join(dir, "setup-code"), "utf8"));
    await page.getByLabel("Username").fill("demo");
    await page.getByLabel("Password", { exact: false }).fill("disposable-demo-only");
    await page.getByRole("button", { name: "Create account" }).click();
    await page.getByRole("button", { name: "Add your first agent" }).waitFor();

    const desk = (route, body) => page.evaluate(async ([route, body]) => {
      const r = await fetch("/api/desk/" + route, body ? { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) } : undefined);
      const v = await r.json(); if (!r.ok) throw Error(route + ": " + v.error); return v;
    }, [route, body]);
    const run = (args, input, extra = {}) => {
      const r = child.spawnSync(native, args, { env: { ...env, ...extra }, input, encoding: "utf8", timeout: 30000 });
      if (r.status !== 0) throw Error("client " + args.join(" ") + ": " + r.stderr);
      return r.stdout;
    };
    const seats = ["researcher", "editor", "reviewer"];
    const pair = await desk("pair", { names: [] });
    run(["login", url], pair.code + "\n");
    await desk("onboarding", { id: pair.id, names: seats });
    run(["onboard", url]);
    start(native, ["onboard", url, "--watch"]);
    start(native, ["--seat", "researcher", "watch"]);
    start(native, ["--seat", "editor", "watch"]);

    // Releases up to 0.12.14 reject request and urgent after --seat, so select the profile by environment.
    const as = (seat, ...args) => JSON.parse(run(args, undefined, { OPENAGENTINBOX_PROFILE: path.join(dir, ".config/openagentinbox/seats", seat + ".json") }));
    // Left unread on purpose: its two-minute pickup deadline passes and is escalated.
    as("editor", "urgent", "reviewer", "Sign off the release notes", "Publishing is waiting on your review.");
    const ask = as("editor", "request", "researcher", "Check the figures in section 3", "Please confirm the totals against the source tables before Friday.");
    as("researcher", "read", ask.id);
    const answer = as("researcher", "reply", ask.id, "editor", "Figures checked", "All totals agree except table 2, where the sum is off by one row. I attached the corrected value in the draft.");
    as("editor", "read", answer.id);
    const brief = await desk("send", { to: "researcher", subject: "Summarise this week's reading", body: "Three bullet points on what changed and what we should try next.", intent: "request", key: require("node:crypto").randomUUID() });
    as("researcher", "read", brief.id);
    const report = as("researcher", "reply", brief.id, "operator", "This week's reading", "1. Long polling beats busy polling for idle agents.\n2. Receipts make hand-offs auditable.\n3. Next: try a digest interval for the reviewer.");
    as("editor", "send", "operator", "Draft is ready for you", "Section 3 is corrected. The reviewer has not picked up the sign-off request yet.");

    for (let i = 0; ; i++) {
      if ((await desk("live")).overdue?.length) break;
      if (i > 100) throw Error("urgent deadline was never recorded as missed");
      await delay(2500);
    }
    await delay(5000);

    // Replace anything that names the build machine, then fail if any of it is still visible.
    const secrets = [os.hostname(), os.hostname().split(".")[0]].filter(v => v.length > 2);
    const shot = async (target, name) => {
      await delay(600);
      const left = await target.evaluate(([hosts, origin]) => {
        const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
        const swap = s => hosts.reduce((v, h) => v.split(h).join("demo-host"), s.split(origin).join("https://inbox.example.org"));
        for (let n; (n = walk.nextNode());) { const v = swap(n.nodeValue); if (v !== n.nodeValue) n.nodeValue = v; }
        const seen = document.body.innerText;
        return [...hosts, origin].filter(v => seen.includes(v));
      }, [secrets, url]);
      if (left.length) throw Error(name + ": build machine details still visible");
      await target.screenshot({ path: path.join(out, name + ".png") });
      console.log("wrote " + name + ".png");
    };
    for (const scheme of ["light", "dark"]) {
      await page.emulateMedia({ colorScheme: scheme });
      await page.goto(url + "/inboxes/operator");
      await page.locator(`a[data-message="${report.id}"]`).click();
      await page.getByRole("heading", { name: "Summarise this week's reading", exact: true }).waitFor();
      await page.getByRole("button", { name: /This week's reading/ }).click();
      await shot(page, "conversation-" + scheme);
      await page.goto(url + "/activity");
      await page.getByRole("heading", { name: "Workspace activity" }).waitFor();
      await page.getByRole("heading", { name: "Overdue requests" }).waitFor();
      await shot(page, "activity-" + scheme);
    }
    await page.emulateMedia({ colorScheme: "light" });
    await page.goto(url + "/messages/" + ask.id);
    await page.getByText("Delivery details").click();
    await shot(page, "receipt-light");
    await page.goto(url + "/inboxes/operator");
    await page.locator("a[data-message]").first().waitFor();
    await shot(page, "inbox-light");
    await page.goto(url + "/agents");
    await page.getByRole("heading", { name: "Agents", exact: true }).waitFor();
    await shot(page, "agents-light");
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(url + "/inboxes/operator");
    await page.locator("a[data-message]").first().waitFor();
    await shot(page, "phone-inbox-light");
  } finally {
    await browser?.close();
    for (const p of children.reverse()) p.kill("SIGTERM");
    await delay(500);
    fs.rmSync(dir, { recursive: true, force: true });
  }
})().catch(e => { console.error(e); process.exit(1); });
