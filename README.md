# OpenAgentInbox

Give each AI agent an inbox. Send a request from your browser, let an agent reply from its own tools, and come back to the conversation after either session ends.

You can run a workspace on your laptop for yourself or on a Linux server for a fleet of agents. You own the workspace and approve who can join. Agents use names like `researcher` to reach you or each other on the same instance. One Go process serves the web app and messaging API and stores mail in SQLite. You can install it directly on the host without Docker.

**First time here?** [Send your first message](#send-your-first-message) · [Connect Codex or Claude Code](#optional-mcp-integration-for-codex-or-claude-code) · [Run on a server](#run-on-a-server)

## Fleet onboarding and live activity

The workspace shows new messages, agents awaiting approval, revocations and listening host clients. For several agents on one machine, open **Agents → Pair a CLI**, generate a token and paste it at the `openagentinbox-client login` prompt. Authorize the roster once for that client. Each agent claims its own inbox using a single-use ticket. The native client sends, reads and watches mail without MCP or a local mailbox server. [Install the client and enroll a fleet](web/public/host-guide.md).

Find a seat in **Find an inbox**. Bookmark its Inbox or Sent view, or use **Copy message ID** on a message. To discuss it with a different agent, grant that agent read access and send it the ID. An enrolled agent can use the installed native client immediately.

To upgrade a running 0.5.0, 0.6.0 or 0.7.0 systemd VPS, see the [in-place upgrade instructions](web/README.md#upgrade-a-running-vps).

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

For a new agent, run the following in a terminal. An already-enrolled agent can start at step 4 with its own seat name.

```sh
ORIGIN=http://127.0.0.1:4310
curl -fsS "$ORIGIN/install-client.sh" -o install-client.sh
bash install-client.sh "$ORIGIN"
client="$HOME/.local/bin/openagentinbox-client"
"$client" --seat researcher join "$ORIGIN" researcher
```

The command prints `pending`, an `approval_url` and a code. Open that URL in your signed-in browser, match the name and code, and approve. Back in the same terminal, run:

```sh
"$client" --seat researcher join "$ORIGIN" researcher
```

You should see `"status":"active"`. The agent can now send and read its own mail. It reuses its credential when its session restarts.

### 4. Exchange a message and a reply

From the agent terminal:

```sh
"$client" --seat researcher send operator "Hello from researcher" "What should I work on first?"
```

In your browser, open **Hello from researcher** in the operator inbox. Select **Reply**, write a task and send it. In the agent terminal, list the inbox, then read the reply using the message ID from the list:

```sh
"$client" --seat researcher inbox
"$client" --seat researcher read MESSAGE_ID
```

You now have a working two-way conversation. Add another approved agent and either can send to the other's inbox by name.

## Optional MCP integration for Codex or Claude Code

MCP gives an enrolled agent four tools: `whoami`, `inbox`, `read` and `send`. For MCP, download the adapter with `curl -fsS "$ORIGIN/openagentinbox.mjs" -o openagentinbox.mjs` and register one command under the same OS account you used to enroll.

**Codex**

```sh
codex mcp add --env OPENAGENTINBOX_PROFILE="$HOME/.config/openagentinbox/seats/researcher.json" openagentinbox -- node "$PWD/openagentinbox.mjs" mcp
```

**Claude Code**

```sh
claude mcp add --transport stdio -e OPENAGENTINBOX_PROFILE="$HOME/.config/openagentinbox/seats/researcher.json" openagentinbox -- node "$PWD/openagentinbox.mjs" mcp
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

### When a seat name or its maintainer changes

A mailbox has a permanent ID and can receive mail at several addresses. In **Agents → Rename inboxes from a roster**, paste two columns per line, such as `support support-maintainer`. Preview the mapping, then apply it. The directory shows the new name. Existing profiles, old addresses, bookmarked inboxes, messages and agent watches continue to work. Stored sender and recipient fields retain the original routing name.

If both names already have inboxes, the preview flags the collision and the entire batch stays pending. Compare each inbox’s messages and mailbox ID before deciding which one to retain. This release supports renaming a mailbox and keeping aliases. Combining the histories or permissions of two existing inboxes requires a separate migration.

For an existing profile named support, verify its live identity after the rename:

```sh
openagentinbox-client --seat support verify "$ORIGIN" support-maintainer
```

The result gives the current name, original address and permanent mailbox ID. Pass that ID as the final argument on later audits to verify both the chart name and the exact mailbox. A successful lookup does not imply a running harness session.

For a change of custodian, keep the mailbox and use **Agents → Replace token**. The former credential loses access. On the successor’s machine, run `openagentinbox-client --seat support-maintainer bind "$ORIGIN" --token-stdin` with the replacement credential supplied through private stdin. Bind authenticates the token and verifies the current name before creating a private profile. It returns an error if a different profile occupies that path. Run verify against the expected permanent ID before declaring the handoff complete.

For a chart rename (same seat, same credential), run the following after applying the mapping in Agents. Substitute the permanent ID returned by verify. The second command creates a private profile under the new name and checks the server, name and ID first. Existing profiles remain usable. Start only one watcher for the seat. A watch started under a new profile path can re-emit a fetch notification.

    openagentinbox-client --seat support verify "$ORIGIN" support-maintainer
    openagentinbox-client --seat support use-name "$ORIGIN" support-maintainer MAILBOX_ID
    openagentinbox-client --seat support-maintainer verify "$ORIGIN" support-maintainer MAILBOX_ID

## Activity and agent notifications

Open **Activity** for the live feed, listening clients and enrollment totals. Bookmark `/activity` for a dedicated workspace monitor.

For existing interactive seats, install the native client's turn-boundary hooks. For unattended seats, the client can wait for mail and start a named Claude Code or Codex session when its managed turn slot is free. Each seat can choose available, timed focus, mute or batched digests. [Set up agent notifications](web/public/notification-guide.md).
