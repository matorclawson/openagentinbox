const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const fs = require("fs"),
  child = require("child_process"),
  path = require("path");
(async () => {
  const dir = fs.mkdtempSync("/tmp/oai-browser-"),
    port = 4439,
    url = "http://127.0.0.1:" + port;
  const proc = child.spawn(
    process.env.INBOX_DAEMON || `../dist/${process.platform}-${process.arch==='x64'?'amd64':process.arch}/openagentinboxd`,
    ["serve"],
    {
      env: { ...process.env, INBOX_UI: path.resolve("out"), INBOX_DATA: dir, INBOX_PORT: String(port) },
      stdio: ["ignore", "inherit", "inherit"],
    },
  );
  let browser;
  try {
    for (let i = 0; i < 100; i++) {
      try {
        if ((await fetch(url + "/api/session")).ok) break;
      } catch {}
      await new Promise((r) => setTimeout(r, 100));
    }
    browser = await chromium.launch({
      executablePath: process.env.CHROMIUM_PATH,
      args: [
        "--no-sandbox",
        "--disable-dev-shm-usage",
        "--no-zygote",
        "--single-process",
      ],
    });
    const page = await browser.newPage({
      viewport: { width: 1200, height: 800 },
    });
    page.setDefaultTimeout(10000);
    page.on("pageerror", console.error);
    await page.goto(url);
    await page
      .getByLabel("Setup code")
      .fill(fs.readFileSync(path.join(dir, "setup-code"), "utf8"));
    await page.getByLabel("Username").fill("Rod");
    await page
      .getByLabel("Password", { exact: false })
      .fill("disposable-test-only");
    await page.getByRole("button", { name: "Create account" }).click();
    await page.getByRole("button", { name: "Add your first agent" }).waitFor();
    const discovery = await (
      await fetch(url + "/.well-known/openagentinbox.json")
    ).json();
    const download = await fetch(url + discovery.cli);
    if (!download.ok) throw Error("CLI download failed");
    const cli = path.join(dir, "fresh-agent.mjs");
    fs.writeFileSync(cli, await download.text());
    const exec = require("node:util").promisify(child.execFile);
    const call = async (...args) =>
      JSON.parse(
        (
          await exec(process.execPath, [cli, ...args], {
            env: {
              PATH: process.env.PATH,
              HOME: dir,
              OPENAGENTINBOX_PROFILE: path.join(dir, "codex/agent.json"),
            },
          })
        ).stdout,
      );
    const req = await call("join", url, "research");
    if (req.status !== "pending") throw Error("missing pending state");
    await page.goto(req.approval_url);
    await page.getByRole("button", { name: "Approve enrollment" }).click();
    await page.getByText("Agent enrolled.", { exact: true }).waitFor();
    if ((await call("join", url, "research")).status !== "active")
      throw Error("enrollment not active");
    await page.getByRole("button", { name: "Compose" }).click();
    await page.getByLabel("To", { exact: true }).fill("research");
    await page
      .getByLabel("Subject", { exact: true })
      .fill("Weekly reading list");
    await page
      .getByLabel("Message", { exact: true })
      .fill("Send me the three papers you recommend.");
    await page.getByRole("button", { name: "Send message" }).click();
    await page.getByText("Message sent.", { exact: true }).waitFor();
    const mail = await call("inbox");
    if (mail.rows.length !== 1) throw Error("agent inbox");
    if (
      (await call("read", mail.rows[0].id)).body !==
      "Send me the three papers you recommend."
    )
      throw Error("agent body");
    await call(
      "send",
      "operator",
      "Your reading list",
      "The reading list is ready.",
    );
    await page.getByRole("button", { name: "Inbox", exact: true }).click();
    await page.getByRole("button", { name: /Your reading list/ }).click();
    await page.getByText("The reading list is ready.").waitFor();
    await page.setViewportSize({ width: 390, height: 844 });
    if (
      await page.evaluate(
        () => document.documentElement.scrollWidth > innerWidth,
      )
    )
      throw Error("mobile overflows");
    await page.getByRole("button", { name: "Sign out" }).click();
    await page.getByRole("button", { name: "Sign in", exact: true }).waitFor();
    console.log(
      "PASS: Chromium onboarding, provisioning, compose, agent reply, inbox detail, mobile width and sign-out",
    );
  } finally {
    if (browser) await browser.close();
    proc.kill();
    fs.rmSync(dir, { force: true, recursive: true });
  }
})().catch((e) => {
  console.error(e);
  process.exitCode = 1;
});
