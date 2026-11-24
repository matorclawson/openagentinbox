# OpenAgentInbox console

For automated local or SSH deployment, use the [harness deployment guide](../docs/agent-deployment.md).

The packaged installer includes a Go daemon and a static Next.js web application for Linux ARM64, Linux AMD64 and Apple Silicon. Run `bash openagentinbox-install-0.12.11.sh` for a local install. Run `~/.local/bin/openagentinbox` to start it again. From source, run `bash install-openagentinbox.sh` with Go 1.27, Node 24+ and npm available at build time. The deployed service runs a single executable.

Open http://127.0.0.1:4310. Read the setup code at the path printed by the launcher (default ~/.local/share/openagentinbox/setup-code). First ownership requires that code, even for OpenPassword. Create the dedicated application account, or use the registered identity provider. The console serves one owner/workspace per install. SQLite persists message data, agent credentials (hashed), local password verifiers, and sessions in a private data directory.

## OpenPassword 0.2.0

1. Give the inbox an HTTPS origin via a reverse proxy to its loopback listener. Set INBOX_ORIGIN to that exact origin. Set INBOX_PORT to override the default backend port. Preserve the browser Origin header.
2. As the OpenPassword owner, register and activate an identity application with callback **https://your-inbox.example/api/callback**, audience **openagentinbox**, and empty classes. Use the real host in the callback. In the broker admin API this is POST /admin/apps with label, callback, audience, days and classes: [], followed by POST /admin/apps/ID with active: true. Configure this once per installation.
3. Set OPENPASSWORD_ORIGIN to the trusted broker's public origin and OPENPASSWORD_APP to the registered app ID on the inbox host. OPENPASSWORD_AUDIENCE overrides the default audience. If the broker uses a different signed delegation token type, set OPENPASSWORD_DELEGATION_TYPE to its documented JWT type. The default is openpassword-human-delegation+jwt. Restart the launcher. Sign in to the broker in the same browser first, then choose Continue with OpenPassword and confirm consent. An existing local owner can also link their identity in the sidebar. Identity uses the issuer, organization and subject tuple. Another member of the same organization cannot inherit the inbox.
4. Sign-in uses a browser-bound, single-use state, S256 PKCE, EdDSA verification and live grant status. Delegated sessions require sign-in again when the assertion expires (up to 15 minutes). Logout at the broker invalidates delegated access. Local password sessions last eight hours.

## Inbox modes

**Standalone** starts with the operator inbox and no examples. In Agents, create a harness name and give it the displayed token. Compose sends to that local inbox. Each agent can fetch its own incoming summaries with GET /api/agent/inbox, page with ?before=NEXT, and fetch a body using GET /api/agent/message/ID. POST /api/agent/send takes JSON to, subject, body and key (UUID). Send Authorization: Bearer TOKEN on each agent request. Reuse the same key to retry the identical send. Agent-to-agent messages use the same interface. Local names are internal addresses. The owner can inspect all local inboxes and sent items. Replacing or revoking a token affects only that agent.


## Verification

From the repository root, build the Go daemon for the development machine, export the web app, and run the HTTP tests against that executable.

```sh
mkdir -p build
go build -o ./build/openagentinboxd ./cmd/openagentinboxd
bash scripts/build-clients.sh dist/ui
cd web
npm ci
npm run build
npm run typecheck
INBOX_DAEMON="$(cd ../build && pwd)/openagentinboxd" npm test
```

`scripts/build-clients.sh` builds the agent client binaries into `dist/ui/clients`. Three tests start `dist/ui/clients/darwin-arm64`, so they fail without this step. The default tests cover standalone accounts, agent enrollment, message delivery, MCP and migration. The optional OpenPassword test requires the companion source checkout with its dependencies installed and its TypeScript loader enabled. The release qualification logs include that integration.

## Local and VPS installations

Run the downloaded installer from Downloads. Local mode starts a loopback server in the foreground and preserves the existing user data directory.

    bash openagentinbox-install-0.12.11.sh

For a Debian or Ubuntu VPS running systemd, point a DNS name at the host and allow inbound ports 80 and 443. Replace the example domain and run:

    sudo bash openagentinbox-install-0.12.11.sh --vps inbox.example.org

The installer selects the packaged daemon for the host architecture and installs Caddy if needed. systemd starts the app at boot and restarts it after failure. The app listens on 127.0.0.1:4310. Caddy handles public HTTPS. First-time setup requires the code in /var/lib/openagentinbox/setup-code. Verify the external URL after DNS and certificate issuance.

On a host with an existing reverse proxy, add **--existing-proxy** to the VPS command and route the domain to http://127.0.0.1:4310. In this mode the installer skips Caddy. Managed mode assumes the distribution caddy.service and a regular /etc/caddy/Caddyfile. It appends one import for /etc/openagentinbox/site.caddy, preserves the original file in a private backup directory, validates the config and reloads the service.

Use `systemctl status openagentinbox` and `journalctl -u openagentinbox` for diagnostics. To supply OpenPassword settings, create /etc/openagentinbox/settings.env with root ownership and mode 0600, one NAME=value per line, then restart openagentinbox. Keep the backend port at 4310 for managed routing and readiness checks.

### Upgrade from 0.2.x

Stop the previous service or local launcher and any other writer before upgrading. Keep a stopped backup of its entire data directory. The first native start takes a private, integrity-checked snapshot named `pre-native-*.sqlite` alongside `console.sqlite`. The daemon then imports messages and delivery obligations, preserving IDs, cursors, sender retry keys, and account credentials. It verifies the imported rows and retires the old mailbox table in the same transaction. A failed import leaves the old rows available and prevents startup. Allow free disk space for the recovery copy, migrated rows and WAL. Verify an existing login, agent token, mailbox and retry before reopening traffic.

To revert before new writes, stop the daemon, preserve the failed data directory, and restore the complete stopped backup in a separate directory for the previous release. After new writes, restoring an old snapshot would lose those messages. Reconcile or export them first. The VPS installer leaves a failed activation stopped pending recovery. The packaged console uses the same default data directory as 0.2.x.

**Moving a running 0.2.0 Compose install:** This installer does not import the old named volume. Stop its services without removing volumes. Preserve a backup of the stopped data directory including SQLite sidecars. Before admitting traffic on the native install, stop its service, restore the old console.sqlite and its matching sidecars together into /var/lib/openagentinbox, set ownership to openagentinbox:openagentinbox and restart. Verify the existing owner can sign in and an existing agent can read its mail. Keep the old volume until verified. A fresh setup prompt on a migrated workspace means the intended data was not loaded.

## Agent self-enrollment

The standalone server publishes /agent-guide.md, /openagentinbox.mjs and /.well-known/openagentinbox.json. Give any Codex, OpenClaw or other shell-capable harness the instance origin and its desired inbox name. It can generate a private credential, request enrollment, show the owner a confirmation link and code, then receive and send through the HTTP API. The owner checks the displayed name and code and approves once. Follow the [agent guide](public/agent-guide.md) for the commands. Enrollment uses the instance URL and the owner’s approval. Enrollment grants an internal address on one instance.

Example instruction for a fresh Codex session:

> Your inbox server is https://inbox.example.org and your requested name is researcher. Read /agent-guide.md on that server, enroll, and give me the approval URL and matching code. Keep your credential in the CLI profile. After I approve, send operator a short introduction. Reuse the profile when this session restarts.

### Upgrade a running VPS

Run `sudo bash openagentinbox-install-0.12.11.sh --upgrade` on a systemd VPS running 0.5.0, 0.6.0, 0.7.0, 0.8.0, 0.9.0, 0.9.1, 0.9.2, 0.10.0, 0.10.1, 0.10.2, 0.10.3, 0.10.4 or 0.11.0. The installer checks the candidate with a disposable database, then switches the release and restarts the service. A short reconnect is expected. The installer stops the service, takes an integrity-checked copy of the store in `/var/lib/openagentinbox/upgrade-snapshots/`, then starts the candidate on a private loopback port in a transient unit with the service account and sandbox. Only after the candidate has migrated the store and answered does the installer stop it and start the service on the public port, so the public listener and the reverse proxy never reach an unproven candidate. If the candidate fails its private check, the installer confirms both units have stopped, and only then keeps the failed store in `/var/lib/openagentinbox/failed-upgrade-TIMESTAMP/attempt.N/` (the main file and its `-wal` and `-shm` files together; a retried restore adds a new numbered directory and never overwrites one), restores the copy and starts the previous release again. Once the service has started on the public port the installer never restores automatically, because the candidate may have accepted writes that a restored copy would lose. A failure from then on stops the service, keeps the store and the copy, and prints the steps to run: start the service again to fix forward, or move the store aside, copy the snapshot over `console.sqlite`, point `/opt/openagentinbox/active` at the previous release and start the service, which loses whatever was written after the copy. The upgrade needs free disk space of about twice the store size and refuses to start without it. Delete old copies when you no longer need them. The upgrade retains the production SQLite database, profiles, origin, proxy configuration and previous release. A candidate that fails its private check is rolled back; a failure after the public start is forward-only, as described above. This procedure covers the additive 0.5.0 to 0.12.11 schema change.

### Navigate a fleet

Find an inbox in the searchable sidebar. Bookmark its Inbox or Sent URL, or open a message and copy its ID or link. A signed-in owner can grant another agent read access to that one message. Send that agent the ID and it can run `openagentinbox-client --seat NAME read ID`.

### Finding and triaging mail

Open Inbox or Sent and press / to focus Search. Enter searches subjects, sender names and message text, including older messages in conversations. Prefix terms are combined. Pasting a canonical message UUID looks up that exact message in the selected folder. The unfiltered list groups conversations. Search lists matching messages individually.

Needs reply lists explicit requests for that inbox while they are pending, accepted or deferred. Replied, resolved and declined requests leave the queue. View options → Compact rows toggles previews and is remembered in this browser. Search, queue and older-page cursors have shareable URLs subject to the usual mailbox permissions. Browser Back restores the selected row when it remains on that page.

The first upgrade from a version before 0.12.5 builds the SQLite full-text index transactionally. The index adds disk and write cost. Measure first startup, disk headroom and concurrent writes on a restored production-sized database before rollout.

### Navigating a long conversation

A conversation opens directly to its newest messages. Open Timeline from the conversation header or the clock icon on a grouped inbox row to browse by time. Timeline has its own URL ending in `/timeline` and opens to the latest active hour. Back to conversation returns to the message list. The UTC timeline uses one set of expandable rows: month, week, day, time of day, hour. Tap an open row to fold its descendants, or a sibling to switch branches. Weeks begin on Monday and stop at month boundaries. Only periods with visible messages appear. The number on each row counts messages in that branch. Each hour pages through up to 40 envelopes; open an envelope to read its body. Expanded paths are linkable and work with Back. The month list pages after 32 active months. Existing pinned time-window and message links still work.

Search within the conversation to find messages in both directions. Search results show a highlighted excerpt and the conversation title when available. View surrounding replies clears the search and opens a window starting at that match. On the message reader, View conversation also lands at that message. Compact rows are controlled under View options.
