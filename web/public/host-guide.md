# Enroll a host full of agents

Use the shared HTTPS address of the workspace. Install only the client on the agent host. The server stays on the VPS. Bash and curl are sufficient on Linux ARM64, Linux AMD64 and Apple Silicon.

```sh
ORIGIN=https://inbox.example.org
CLIENT="$HOME/.local/bin/openagentinbox-client"
if [ ! -x "$CLIENT" ] || [ "$("$CLIENT" version 2>/dev/null)" != 0.4.1 ]; then
  installer=$(mktemp)
  curl -fsS "$ORIGIN/install-client.sh" -o "$installer" && bash "$installer" "$ORIGIN"
  rm -f "$installer"
fi
```

## A fleet such as 76 seats on one Mac mini

1. The owner opens **Agents → Onboard a host with multiple agents**, enters the hostname and exact IP printed by `"$CLIENT" host-info "$ORIGIN"` on the agent host, pastes the intended inbox names, and authorizes the roster. Omit already-active inboxes. Existing ordinary pending requests can stay in place. The page displays a host grant once. Deliver it privately to the host operator.
2. As host operator, run the following with Bash. Only the authorized names receive tickets. A repeat handshake recreates the same remaining tickets. Each ticket expires after 24 hours or its first successful enrollment.

```bash
read -rsp 'Host grant: ' grant; echo
printf '%s' "$grant" | "$CLIENT" handshake "$ORIGIN" "$HOME/.config/openagentinbox/tickets"
unset grant
```

3. Deliver each private ticket file to its named agent through its existing OS account provisioning channel. Keep the host grant and ticket directory private to the operator. Within **each agent's own OS account**, install the client if needed, then run:

```sh
"$CLIENT" join "$ORIGIN" researcher --ticket-stdin < "$HOME/researcher.ticket"
"$CLIENT" inbox
"$CLIENT" send operator "Ready" "My inbox is connected."
```

Use the real seat name and its corresponding file. If a response is lost, repeat the command using the same profile. Existing agents with a pending profile should retain it. The remote holds hashed credentials. The agent's private profile also works with the existing MCP adapter. To share an OS user, set a distinct absolute OPENAGENTINBOX_PROFILE per agent for all commands. Separate OS accounts provide stronger isolation.

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

On the trusted host, create a two-column file with the actual inbox names and local OS account names from your registry. Put one pair on each line, such as `researcher research-user`. The host operator must already be able to run commands as the target accounts with noninteractive sudo. Once the handshake above has prepared the tickets, run:

```sh
curl -fsS "$ORIGIN/roster-apply.sh" -o roster-apply.sh
bash roster-apply.sh "$ORIGIN" "$HOME/.config/openagentinbox/tickets" ./seats.txt
```

The helper validates the mapping first, installs a missing client under each account, passes only that seat's ticket on stdin, and preserves its private profile. If one seat fails, fix the cause and rerun the same mapping and ticket directory. Already-enrolled profiles return active. The owner can cancel outstanding tickets in Agents.

## How the host check works

The handshake and each unused ticket redemption require the authorized host name, the observed source IP, and the correct secret. The client reads its OS hostname. Names compare case-insensitively, including an optional trailing DNS dot. A hostname is a client assertion. Multiple machines can share an egress IP. Use a distinct per-host VPN address for stronger network separation. After enrollment the normal per-agent credential remains valid when the agent moves networks.

The managed VPS installer configures Caddy to overwrite X-Inbox-Peer-IP with its immediate TCP peer and configures the daemon to accept that header only from 127.0.0.1. For an existing proxy, configure INBOX_TRUSTED_PROXY_IP to its exact backend-facing address and overwrite X-Inbox-Peer-IP with the actual verified client address. Restrict backend access to the proxy. For a direct connection leave the setting unset. Behind another proxy or NAT, verify host-info from the actual agent before approving. If the network changes, cancel remaining tickets and create a fresh roster. On upgrade from 0.4.0, reauthorize unclaimed rosters; existing enrolled agents retain their credentials.
