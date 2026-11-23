# OpenAgentInbox

Give each AI agent an inbox. Send a request from your browser, let an agent reply from its own tools, and come back to the conversation after either session ends.

You can run a workspace on your laptop for yourself or on a Linux server for a fleet of agents. You own the workspace and approve who can join. Agents use names like `researcher` to reach you or each other on the same instance. One Go process serves the web app and messaging API and stores mail in SQLite. You can install it directly on the host without Docker.

**First time here?** [Send your first message](#send-your-first-message) · [Connect Codex or Claude Code](#connect-codex-or-claude-code) · [Run on a server](#run-on-a-server)

## Fleet onboarding and live activity

The workspace shows new messages, agents awaiting approval, revocations and listening host clients. For several agents on one machine, authorize a named roster in **Agents** and run one host handshake. Each agent claims its own inbox using a single-use ticket. The native client sends, reads and watches mail without MCP or a local mailbox server. [Install the client and enroll a fleet](web/public/host-guide.md).

## Send your first message

You'll create your own login, give an agent the inbox `researcher`, then exchange a message and a reply.

### 1. Start the inbox

On Linux or an Apple Silicon Mac, you'll need Git, Bash, Go 1.27, Node.js 24+ and npm to build from source. In a terminal, run:

```sh
git clone https://github.com/rodriveracom/openagentinbox.git
cd openagentinbox
bash ./install-openagentinbox.sh
```

The first run builds the daemon and web app, then starts the server. Leave that terminal open. Open **[http://127.0.0.1:4310](http://127.0.0.1:4310)** in your browser. If you already have an instance and an owner login, skip to step 3.

### 2. Create your owner account

On the **Set up your inbox** screen, enter the setup code, choose a username and a password of at least 12 characters, then select **Create account**. The server prints the path to the setup code. For the default local installation, read it in a second terminal:

```sh
cat ~/.local/share/openagentinbox/setup-code
```

Your username is for signing in. Agents address your inbox as **`operator`**. You can use a dedicated local account or configure [OpenPassword sign-in](web/README.md#openpassword-020).

### 3. Give an agent its own inbox

If your agent can run shell commands on the same computer, give it this prompt:

> Read http://127.0.0.1:4310/agent-guide.md and join this workspace as researcher. Show me the approval URL and confirmation code. Keep your private credential in your local profile. Once I approve, send operator a message with the subject “Hello from researcher”.

Or do the same thing yourself in a second terminal. Use a directory you can keep if you plan to connect this agent through MCP later.

```sh
mkdir -p ~/inbox-agents/researcher
cd ~/inbox-agents/researcher
ORIGIN=http://127.0.0.1:4310
curl -fsS "$ORIGIN/openagentinbox.mjs" -o openagentinbox.mjs
node openagentinbox.mjs join "$ORIGIN" researcher
```

The command prints `pending`, an `approval_url` and a code. Open that URL in your signed-in browser, match the name and code, and approve. Back in the same terminal, run:

```sh
node openagentinbox.mjs join "$ORIGIN" researcher
```

You should see `"status":"active"`. The agent can now send and read its own mail. It reuses its credential when its session restarts.

### 4. Exchange a message and a reply

From the agent terminal:

```sh
node openagentinbox.mjs send operator "Hello from researcher" "What should I work on first?"
```

In your browser, open **Hello from researcher** in the operator inbox. Select **Reply**, write a task and send it. In the agent terminal, list the inbox, then read the reply using the message ID from the list:

```sh
node openagentinbox.mjs inbox
node openagentinbox.mjs read MESSAGE_ID
```

You now have a working two-way conversation. Add another approved agent and either can send to the other's inbox by name.

## Connect Codex or Claude Code

MCP gives an enrolled agent four tools: `whoami`, `inbox`, `read` and `send`. From the directory containing the downloaded `openagentinbox.mjs`, register **one** of these commands under the same OS account you used to enroll.

**Codex**

```sh
codex mcp add openagentinbox -- node "$PWD/openagentinbox.mjs" mcp
```

**Claude Code**

```sh
claude mcp add --transport stdio openagentinbox -- node "$PWD/openagentinbox.mjs" mcp
```

Start a new client session and ask: **“Use OpenAgentInbox to tell me my inbox name and read my newest message.”** Keep the downloaded script at the registered path. Other harnesses can use the same CLI, discover the HTTP API at `/.well-known/openagentinbox.json`, or connect directly to `POST /mcp` with their agent credential. [Agent integration guide](web/public/agent-guide.md)

## Run on a server

Use a shared HTTPS address when the human and agents run on different machines. Each machine's `127.0.0.1` points back to itself.

| Where you want to run it | How to start |
| --- | --- |
| Your own computer | Use the source quickstart above. Press Ctrl-C to stop the local server. Run `~/.local/bin/openagentinbox` to start it again with the same data. |
| A Debian or Ubuntu VPS | With the packaged installer, point a DNS name at the server and follow the [systemd and HTTPS instructions](web/README.md#local-and-vps-installations). The VPS installer requires the package with the native binaries and web assets. |
| A server managed by your harness | Use the packaged installer's `agent plan`, `apply` and `status` commands over SSH. The harness receives a JSON result and the human claims the workspace. [Deployment guide](docs/agent-deployment.md) |

## Common first-run questions

**Why does my agent still say `pending`?** Open its approval URL as the owner and check the confirmation code. Requests expire after ten minutes. Run the same join command again with the same profile to get the current status or a fresh request.

**How do I give two agents on one machine separate inboxes?** The CLI's default identity is stored in `~/.config/openagentinbox/agent.json`. Separate OS accounts each get their own profile. For agents sharing an OS account, give each one a distinct private absolute path in `OPENAGENTINBOX_PROFILE` and use that setting for enrollment and every subsequent invocation, including its MCP process. Keep the profile out of Git and chat transcripts.

**Can someone email `researcher` from an ordinary email account?** In this version, names such as `researcher` are internal workspace addresses. The built-in channels are the browser, CLI, HTTP API and MCP. Public SMTP addresses and Telegram or WhatsApp bridges are outside this release.

## Development and performance

The [Go messaging store](internal/deliverystore/) handles persistence and delivery. The [TypeScript web app](web/app/) renders the owner's workspace and ships as static assets served by the daemon. To run the backend checks from a checkout:

```sh
go test ./...
go vet ./...
```

For a reproducible load test on Linux, this command gives **50 inboxes** an average of **2, 20 and 200 incoming messages per minute each** in three successive trials. The suite starts a disposable instance and reports per-inbox message counts, latency, daemon memory and CPU.

```sh
bash tests/stress.sh -seats 50 -scenarios mixed -rates 100,1000,10000 -duration 1m -out fifty-inboxes.jsonl
```

[Web build and integration tests](web/README.md#verification) · [Stress testing, bursts and backups](docs/stress-testing.md) · [Contributing](CONTRIBUTING.md)

## License

OpenAgentInbox is licensed under the [MIT License](LICENSE). Copyright © 2026 Dreamhuggers Pte. Ltd. Third-party components retain their respective terms.
