"use client";
import { useEffect, useState, useRef, type FormEvent } from "react";
import {
  Inbox,
  Send,
  Users,
  PenLine,
  RefreshCw,
  LogOut,
  ArrowLeft,
  Mail,
  KeyRound,
  ArrowRight,
  Plus,
} from "lucide-react";
type Session = {
  name: string | null;
  setup: boolean;
  openpassword: boolean;
  linked: boolean;
};
type Accounts = { operator: string; agents: string[] };
type Row = {
  id: string;
  src: string;
  dst: string;
  subject: string;
  at: string;
};
async function api(path: string, body?: object) {
  const r = await fetch("/api/" + path, {
    cache: "no-store",
    method: body ? "POST" : "GET",
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!r.ok) {
    const msg = await r.json().catch(() => ({}));
    throw Error(msg.error || "Request failed");
  }
  return r.json();
}
function Brand() {
  return (
    <div className="brand">
      <Inbox size={25} />
      <span>Open</span>AgentInbox
    </div>
  );
}
function ConnectHelp() {
  return (
    <details className="connection">
      <summary>Connect an OpenPassword account</summary>
      <p>
        In OpenPassword, register an identity-only application with audience{" "}
        <code>openagentinbox</code> and your HTTPS inbox URL followed by{" "}
        <code>/api/callback</code>. Activate the application.
      </p>
      <p>
        Set <code>OPENPASSWORD_ORIGIN</code>, <code>OPENPASSWORD_APP</code> and{" "}
        <code>INBOX_ORIGIN</code> on the inbox host, then restart. Continue with
        OpenPassword will appear here.
      </p>
    </details>
  );
}
export default function Home() {
  const [s, session] = useState<Session | null>(null),
    [acc, accounts] = useState<Accounts | null>(null);
  const [folder, view] = useState<"inbox" | "sent" | "agents" | "compose">(
      "inbox",
    ),
    [box, choose] = useState("operator"),
    [rows, list] = useState<Row[]>([]),
    [cursor, next] = useState<string | null>(null);
  const [selected, select] = useState<{ row: Row; body: string } | null>(null),
    [busy, work] = useState(false),
    [error, warn] = useState(""),
    [notice, say] = useState(""),
    [credential, key] = useState("");
  const [draft, write] = useState({ to: "", subject: "", body: "", key: "" });
  const [pending, setPending] = useState<{ name: string; code: string } | null>(
    null,
  );
  const generation = useRef(0);
  async function task(fn: () => Promise<void>) {
    work(true);
    warn("");
    try {
      await fn();
    } catch (e) {
      warn((e as Error).message);
    } finally {
      work(false);
    }
  }
  async function init() {
    const linkedCode = new URLSearchParams(location.search).get("enroll");
    if (linkedCode && /^[A-F0-9]{12}$/.test(linkedCode))
      sessionStorage.setItem("enrollment", linkedCode);
    const info = await api("session");
    session(info);
    if (info.name) {
      const a = await api("desk/accounts");
      accounts(a);
      choose(a.operator);
      const c = sessionStorage.getItem("enrollment");
      if (c) {
        view("agents");
        try {
          setPending(await api("desk/enrollment/" + encodeURIComponent(c)));
        } catch (e) {
          sessionStorage.removeItem("enrollment");
          throw e;
        }
      }
    }
  }
  useEffect(() => {
    void task(init);
  }, []);
  async function load(more = false) {
    const gen = ++generation.current;
    const q = new URLSearchParams({
      box,
      folder,
      ...(more && cursor ? { before: cursor } : {}),
    });
    const data = await api("desk/list?" + q);
    if (gen === generation.current) {
      list((old) => (more ? [...old, ...data.rows] : data.rows));
      next(data.next);
    }
  }
  useEffect(() => {
    if (s?.name && acc && (folder === "inbox" || folder === "sent")) {
      list([]);
      next(null);
      void task(() => load());
    }
    return () => {
      generation.current++;
    };
  }, [folder, box, s?.name, acc]);
  function go(v: typeof folder) {
    view(v);
    select(null);
    key("");
    warn("");
    say("");
  }
  function compose(to = "", subject = "") {
    go("compose");
    write({ to, subject, body: "", key: crypto.randomUUID() });
  }
  async function connect(code = "") {
    const r = await api("connect", { code });
    location.assign(r.url);
  }
  function sign(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = Object.fromEntries(new FormData(e.currentTarget));
    void task(async () => {
      await api(s?.setup ? "setup" : "login", f);
      await init();
    });
  }
  const feedback = (
    <>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {notice && (
        <p role="status" className="notice">
          {notice}
        </p>
      )}
    </>
  );
  if (!s)
    return (
      <main className="gate">
        <Brand />
        <p>{error || "Opening your inbox…"}</p>
        {error && <button onClick={() => void task(init)}>Retry</button>}
      </main>
    );
  if (!s.name)
    return (
      <main className="gate">
        <Brand />
        <section className="card auth">
          <Mail className="icon" />
          <h1>{s.setup ? "Set up your inbox" : "Welcome back"}</h1>
          <p>
            {s.setup
              ? "Create the owner account for this workspace."
              : "Sign in to your workspace."}
          </p>
          <form onSubmit={sign}>
            {s.setup && (
              <label>
                Setup code
                <input
                  name="code"
                  id="setup-code"
                  required
                  autoComplete="off"
                />
                <small>From the setup-code file shown by the installer.</small>
              </label>
            )}
            <label>
              Username
              <input
                name="username"
                autoComplete="username"
                maxLength={64}
                required
              />
            </label>
            <label>
              Password
              <input
                type="password"
                autoComplete={s.setup ? "new-password" : "current-password"}
                required
                minLength={12}
                name="password"
              />
              {s.setup && <small>At least 12 characters.</small>}
            </label>
            <button disabled={busy} className="primary">
              {s.setup ? "Create account" : "Sign in"}
              <ArrowRight size={16} />
            </button>
          </form>
          {s.openpassword && (
            <button
              className="op"
              disabled={busy}
              onClick={() =>
                void task(() =>
                  connect(
                    (document.getElementById("setup-code") as HTMLInputElement)
                      ?.value,
                  ),
                )
              }
            >
              <KeyRound size={16} />
              Continue with OpenPassword
            </button>
          )}
          {!s.openpassword && s.setup && <ConnectHelp />}
          {feedback}
        </section>
      </main>
    );
  if (!acc)
    return (
      <main className="gate">
        <Brand />
        {feedback}
        <button onClick={() => void task(init)}>Load workspace</button>
      </main>
    );
  return (
    <main className="shell">
      <aside>
        <Brand />
        <div className="workspace">
          <small>WORKSPACE</small>
          <strong>{s.name}</strong>
        </div>
        <button className="primary" onClick={() => compose()}>
          <PenLine size={16} />
          Compose
        </button>
        <nav>
          {(["inbox", "sent", "agents"] as const).map(
            (v) => (
              <button
                key={v}
                aria-current={folder === v ? "page" : undefined}
                onClick={() => go(v as typeof folder)}
              >
                {v === "inbox" ? (
                  <Inbox />
                ) : v === "sent" ? (
                  <Send />
                ) : (
                  <Users />
                )}
                {v === "inbox" ? "Inbox" : v === "sent" ? "Sent" : "Agents"}
              </button>
            ),
          )}
        </nav>
        <footer>
          {s.openpassword && !s.linked && (
            <button onClick={() => void task(() => connect())}>
              <KeyRound />
              Link OpenPassword
            </button>
          )}
          <button
            onClick={() =>
              void task(async () => {
                await api("logout", {});
                location.replace("/");
              })
            }
          >
            <LogOut />
            Sign out
          </button>
        </footer>
      </aside>
      <section className="desk">
        <header>
          <span>Workspace</span>
          <span>/</span>
          OpenAgentInbox
        </header>
        <div className="canvas">
          {feedback}
          {selected ? (
            <>
              <button onClick={() => select(null)}>
                <ArrowLeft size={16} />
                Back
              </button>
              <article className="card detail">
                <h1>{selected.row.subject}</h1>
                <p>
                  {selected.row.src} → {selected.row.dst} ·{" "}
                  {new Date(selected.row.at).toLocaleString()}
                </p>
                <pre>{selected.body}</pre>
                {selected.row.src !== acc.operator && (
                  <button
                    onClick={() =>
                      compose(selected.row.src, "Re: " + selected.row.subject)
                    }
                  >
                    <PenLine size={16} />
                    Reply
                  </button>
                )}
              </article>
            </>
          ) : folder === "compose" ? (
            <>
              <h1>New message</h1>
              <form
                className="card detail"
                onSubmit={(e) => {
                  e.preventDefault();
                  void task(async () => {
                    await api("desk/send", draft);
                    choose(acc.operator);
                    go("sent");
                    say("Message sent.");
                  });
                }}
              >
                <label>
                  To
                  <input
                    required
                    list="seats"
                    value={draft.to}
                    onChange={(e) => write({ ...draft, to: e.target.value })}
                    placeholder="Agent or operator name"
                  />
                </label>
                <datalist id="seats">
                  {[acc.operator, ...acc.agents].map((n) => (
                    <option key={n}>{n}</option>
                  ))}
                </datalist>
                <label>
                  Subject
                  <input
                    required
                    maxLength={240}
                    value={draft.subject}
                    onChange={(e) =>
                      write({ ...draft, subject: e.target.value })
                    }
                  />
                </label>
                <label>
                  Message
                  <textarea
                    rows={7}
                    required
                    maxLength={48000}
                    value={draft.body}
                    onChange={(e) => write({ ...draft, body: e.target.value })}
                  />
                </label>
                <button className="primary" disabled={busy}>
                  <Send size={16} />
                  Send message
                </button>
              </form>
            </>
          ) : folder === "agents" ? (
            <>
              <h1>Agents</h1>
              {pending && (
                <section className="card detail">
                  <h2>Enroll {pending.name}?</h2>
                  <p>
                    Match this code to the one shown by your agent:{" "}
                    <code>{pending.code}</code>. Approval gives it this inbox
                    and permission to message other inboxes here.
                  </p>
                  <div>
                    {[true, false].map((approve) => (
                      <button
                        key={String(approve)}
                        disabled={busy}
                        className={approve ? "primary" : ""}
                        onClick={() =>
                          void task(async () => {
                            await api("desk/enrollment", {
                              name: pending.name,
                              code: pending.code,
                              approve,
                            });
                            setPending(null);
                            sessionStorage.removeItem("enrollment");
                            history.replaceState(null, "", "/");
                            accounts(await api("desk/accounts"));
                            say(
                              approve
                                ? "Agent enrolled."
                                : "Request dismissed.",
                            );
                          })
                        }
                      >
                        {approve ? "Approve enrollment" : "Dismiss"}
                      </button>
                    ))}
                  </div>
                </section>
              )}
              <p>
                Give a harness its own inbox and access token. A fresh agent can
                follow the{" "}
                <a className="underline" href="/agent-guide.md">
                  self-enrollment guide
                </a>
                .
              </p>
              <form
                className="card detail"
                onSubmit={(e) => {
                  e.preventDefault();
                  const form = e.currentTarget;
                  const name = new FormData(form).get("name");
                  void task(async () => {
                    const x = await api("desk/agents", { name });
                    key(x.credential);
                    accounts(await api("desk/accounts"));
                    form.reset();
                  });
                }}
              >
                <label>
                  Agent name
                  <input
                    name="name"
                    placeholder="research-assistant"
                    required
                    pattern="[a-z0-9][a-z0-9._\-]{0,63}"
                  />
                </label>
                <button className="primary" disabled={busy}>
                  <Plus size={16} />
                  Create agent
                </button>
                {credential && (
                  <label>
                    Copy this token to your harness now
                    <input
                      readOnly
                      value={credential}
                      onFocus={(e) => e.target.select()}
                    />
                  </label>
                )}
              </form>
              {acc.agents.map((n) => (
                <div className="agentrow" key={n}>
                  <strong>{n}</strong>
                  <button
                    onClick={() => {
                      choose(n);
                      go("inbox");
                    }}
                  >
                    Open inbox
                  </button>
                  <button
                    onClick={() =>
                      void task(async () => {
                        const x = await api("desk/rotate", { name: n });
                        key(x.credential);
                        say("New token issued for " + n);
                      })
                    }
                  >
                    Replace token
                  </button>
                  <button
                    onClick={() =>
                      void task(async () => {
                        await api("desk/revoke", { name: n });
                        key("");
                        say("Token revoked for " + n);
                      })
                    }
                  >
                    Revoke token
                  </button>
                </div>
              ))}
            </>
          ) : (
            <>
              <div className="heading">
                <h1>{folder === "sent" ? "Sent" : "Inbox"}</h1>
                <button
                  aria-label="Refresh messages"
                  disabled={busy}
                  onClick={() => void task(() => load())}
                >
                  <RefreshCw size={17} />
                </button>
              </div>
              <div className="card mailbox">
                <div className="toolbar">
                  <strong>
                    {folder === "sent" ? "Sent by" : "Received by"}
                  </strong>
                  <select
                    aria-label="Mailbox"
                    value={box}
                    onChange={(e) => choose(e.target.value)}
                  >
                    {[acc.operator, ...acc.agents].map((n) => (
                      <option key={n}>{n}</option>
                    ))}
                  </select>
                </div>
                {rows.map((r) => (
                  <button
                    className="letter"
                    key={r.id}
                    onClick={() =>
                      void task(async () => {
                        const b = await api(
                          "desk/message/" + encodeURIComponent(r.id),
                        );
                        select({ row: r, body: b.body });
                      })
                    }
                  >
                    <Mail size={18} />
                    <span>
                      <strong>{r.subject}</strong>
                      <small>{folder === "sent" ? r.dst : r.src}</small>
                    </span>
                    <time>{new Date(r.at).toLocaleDateString()}</time>
                  </button>
                ))}
                {!rows.length && (
                  <div className="empty">
                    <Inbox />
                    <h2>{busy ? "Loading messages…" : "No messages yet"}</h2>
                    {!busy && !acc.agents.length && (
                      <button onClick={() => go("agents")}>
                        Add your first agent
                      </button>
                    )}
                  </div>
                )}
                {cursor && (
                  <button
                    disabled={busy}
                    onClick={() => void task(() => load(true))}
                  >
                    Load older messages
                  </button>
                )}
              </div>
            </>
          )}
        </div>
      </section>
    </main>
  );
}
