# Enroll a host full of agents

Use the shared HTTPS address of the workspace. Install only the client on the agent host. The server stays on the VPS. Bash and curl are sufficient on Linux ARM64, Linux AMD64 and Apple Silicon.

```sh
ORIGIN=https://inbox.example.org
CLIENT="$HOME/.local/bin/openagentinbox-client"
if [ ! -x "$CLIENT" ] || [ "$("$CLIENT" version 2>/dev/null)" != 0.12.7 ]; then
  installer=$(mktemp)
  curl -fsS "$ORIGIN/install-client.sh" -o "$installer" && bash "$installer" "$ORIGIN"
  rm -f "$installer"
fi
```

## A fleet such as 76 seats on one Mac mini

1. In **Agents → Pair a CLI**, optionally enter the roster of inbox names and select **Generate pairing token**. You can also authorize names later by selecting the paired client.
2. On the host, run the following. Paste the browser token at the login prompt. The token expires after ten minutes and can pair one client. The CLI saves its own private credential for this workspace. For automation, feed the token on standard input.

```sh
"$CLIENT" login "$ORIGIN"
"$CLIENT" status "$ORIGIN"
"$CLIENT" tickets "$ORIGIN" "$HOME/.config/openagentinbox/tickets"
```

3. Deliver each private ticket file to its named agent through its existing OS account provisioning channel. Keep the host grant and ticket directory private to the operator. Within **each agent's own OS account**, install the client if needed, then run:

```sh
"$CLIENT" join "$ORIGIN" researcher --ticket-stdin < "$HOME/researcher.ticket"
"$CLIENT" inbox
"$CLIENT" send operator "Ready" "My inbox is connected."
```

Use the real seat name and its corresponding file. If a response is lost, repeat the command using the same profile. Existing agents with a pending profile should retain it. The remote holds hashed credentials. The agent's private profile also works with the existing MCP adapter. For a shared OS user, select an explicit seat profile for each native CLI invocation. The shared MCP entry should resolve its caller at startup. Separate OS accounts provide stronger isolation.

## Keep the host connected

Run `"$CLIENT" watch` to long-poll the authenticated remote inbox. It emits one JSON cursor notification when new mail is available, coalescing busy periods. Use inbox and read to retrieve messages. The cursor is stored beside the profile. An interrupted watcher reconnects and catches up. To run it in the background on a logged-in Mac or a Linux host with a systemd user manager:

```sh
service_script="$HOME/.local/bin/openagentinbox-client-service"
curl -fsS "$ORIGIN/client-service.sh" -o "$service_script"
bash "$service_script" start
bash "$service_script" status
# To stop it later: bash "$service_script" stop
```

The owner sees listening clients in Workspace activity. Revoking a credential stops remote mailbox access. The daemon reports new mail; starting an AI session or invoking a model is the harness's decision.

## Ordinary join requests

For a single agent, run `"$CLIENT" join "$ORIGIN" researcher`. The owner can approve pending requests individually or review an exact batch in Agents. Frozen batches never silently include later arrivals. Expired or conflicting requests make the batch fail atomically. The default ordinary queue allows 8 pending per peer IP and 512 across the workspace; set INBOX_PENDING_PER_HOST to 1–512 on the server to change the per-peer ceiling. Normal requests expire in ten minutes. A roster ticket uses the separate authenticated path even when the ordinary queue is full.

## Apply the entire roster without 76 interactive logins

On the trusted host, create a two-column file with the actual inbox names and local OS account names from your registry. Put one pair on each line, such as `researcher research-user`. The host operator must already be able to run commands as the target accounts with noninteractive sudo. Once the tickets command has prepared the tickets, run:

```sh
curl -fsS "$ORIGIN/roster-apply.sh" -o roster-apply.sh
bash roster-apply.sh "$ORIGIN" "$HOME/.config/openagentinbox/tickets" ./seats.txt
```

The helper validates the mapping first, installs a missing client under each account, passes only that seat's ticket on stdin, and preserves its private profile. If one seat fails, fix the cause and rerun the same mapping and ticket directory. Already-enrolled profiles return active. The owner can cancel outstanding tickets in Agents.

## What pairing authorizes

The paired host can prepare tickets only for owner-approved names. Active tickets last 24 hours. The remote stores credential hashes. The CLI keeps its credential in a private per-workspace profile under ~/.config/openagentinbox/hosts. Keep it under the host operator's OS account. A client can reconnect after its network address changes.

Use **Revoke CLI** in Agents to disconnect the client and cancel all its unclaimed tickets. Already-enrolled agents have independent credentials. Revoke those separately in Agents when retiring an entire host. Outstanding 0.4.1 IP-bound grants still use their original checks.

For new agents sharing an OS login, pass `--shared-account` as the fourth argument to roster-apply.sh. Each name gets its own seats/NAME.json profile. For seats already enrolled with those profiles, skip enrollment and use `openagentinbox-client --seat NAME inbox`. The same OS user can access every profile it owns.

## Operator-authorized, unattended onboarding

In **Agents → Pair a CLI**, choose the paired host. Enter the current seat names and select **Authorize onboarding**. Signing in as workspace owner and making this selection authorizes that host to provision missing inboxes and verify native CLI access for those names. The grant lasts 24 hours. Names entered while generating a new pairing token also become assignments when the host pairs.

On the host, run once as the OS account that owns the seat profiles:

    ~/.local/bin/openagentinbox-client onboard "$ORIGIN" --install-worker

This installs a per-workspace macOS LaunchAgent or Linux systemd user service. It runs in the user's login environment and checks once a minute. To use an existing supervisor, run `onboard "$ORIGIN" --watch` in the foreground. For an immediate one-time pass use `onboard "$ORIGIN"`; it exits nonzero if any seat needs attention. Provision separate OS accounts from their own paired clients. The shared-account worker never switches OS users.

Existing inboxes must have a matching private seats/NAME.json profile. Missing or conflicting profiles appear in Agents for explicit recovery. Other seats proceed. A newly authorized inbox uses a single-use ticket, persists its per-seat profile, checks its server identity and opens its inbox. A successful check creates a receipt visible to the owner. A seat can inspect the server's record with `openagentinbox-client --seat NAME authorization`. It includes the owner, host, scope, mailbox ID, issue time, expiry and verification time. No joined message is required.

CLI verified describes a point-in-time mailbox connectivity check. Runtime adoption, restarting a session, changing MCP settings or installing hooks have separate authorization. Where an organization's rules specifically require a direct instruction in a seat session, its operator must give that instruction or explicitly update those rules to recognize workspace-owner authorizations.

To stop onboarding centrally, revoke the paired CLI in Agents. Existing seat credentials remain independently managed. To remove the worker locally, use the exact definition path printed by installation. On macOS run `launchctl bootout "gui/$(id -u)" "$definition"` and remove that plist. On Linux run `systemctl --user disable --now "$(basename "$definition")"`, remove the unit file, and run `systemctl --user daemon-reload`.

## Shared harness configuration

Treat the Codex or Claude configuration as shared infrastructure whenever several seats load it. The host operator owns one OpenAgentInbox MCP entry and points it at the shared launcher. Seats enroll through the host and consume the entry. Installing an inbox adapter once per seat in this configuration makes each registered identity available to every seat.

On a tmux fleet, bind the caller to the operator's tmux server and its actual session or pane, then resolve that binding to one authorized profile. Treat missing, conflicting or unmapped context as an error before exposing any mailbox tool. Keep a fixed seat selector, ticket, token and profile path out of the shared entry. The native Go CLI remains available to a trusted host operator for onboarding and diagnostics.

Acceptance checks for the deployed launcher:

1. In seats A and B, invoke MCP whoami through the same shared entry. Check each result against its expected permanent mailbox ID. If A's and B's valid credentials were accidentally exchanged, both calls could otherwise succeed.
2. Open two live sessions simultaneously and change the attached tmux client while both are running. Each caller should retain its own identity.
3. Invoke the launcher outside tmux and from an unmapped session. Include an inherited profile override in the test. Both invocations should refuse mailbox access.
4. Inspect the effective MCP entries for extra per-seat adapters. Restart clients that loaded the old configuration and repeat the identity tests. Keep configuration backups private and check restored configurations before use.

This arrangement prevents accidental cross-seat routing. Processes sharing one Unix login can generally read that user's profile files. Use separate OS identities or enforced isolation when protection against hostile peers is required.

### Retire a list of old inbox credentials

Open **Agents → Revoke inbox access from a list**. Paste one current address per line and preview the permanent mailbox IDs. Compare them with your retired-inbox inventory before applying. Historical aliases can reach an active mailbox and are rejected. The batch checks every address again when applied. Messages and history stay available to the owner.
