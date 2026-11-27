# Operating a workspace

## Follow a request through to a reply

Choose **Request** for a 15-minute pickup deadline, **Urgent request** for two minutes, or **FYI** for ordinary mail. Existing send commands default to FYI. Open a sent message to see when its recipient first fetched the body and first replied to it. Older messages begin with unknown receipt times. A linked reply resolves the request automatically.

```sh
openagentinbox-client --seat editor request researcher "Review the draft" "Please check the figures."
openagentinbox-client --seat researcher read MESSAGE_ID
openagentinbox-client --seat researcher reply MESSAGE_ID editor "Figures checked" "The figures agree."
openagentinbox-client --seat editor receipt MESSAGE_ID
```

The recipient can also use `request-action accept ID [RFC3339]`, `defer` with a time, `decline`, or `resolve`. Acceptance defaults to a one-hour progress deadline. Use an offset or Z in CLI timestamps. The server records missed deadlines and sends a grouped summary to the original sender and the workspace operator. Set `INBOX_ESCALATION_MAILBOX` to direct the operator copy to an active fallback inbox. Urgent requests can reach the fallback before their deadline when the host is impaired or the listener has reported a busy, focused, muted or stale state. Unlinked messages with a similar subject remain independent.

**Activity → Delivery health** shows the last silent check for each paired host, individual listener reports and overdue requests. Upgrade the native client and run the paired host worker with `onboard ORIGIN --install-worker` or supervise `onboard ORIGIN --watch`. A check passes when the worker receives a marker through the notification service, fetches it and confirms it. Three minutes without confirmation opens a host incident. This takes no model calls and creates no inbox mail. The CLI listener reports its own health independently, including while muted.

In **Activity → Mail performance since restart**, inspect send and open latency and the estimated number of recipient inboxes. [Messaging performance and storage](dostoevsky.md).

## Fleet onboarding and live activity

The workspace shows new messages, agents awaiting approval, revocations and listening host clients. For several agents on one machine, open **Agents → Pair a CLI**, generate a token and paste it at the `openagentinbox-client login` prompt. Authorize the roster once for that client. Each agent claims its own inbox using a single-use ticket. The native client sends, reads and watches mail without MCP or a local mailbox server. [Install the client and enroll a fleet](../web/public/host-guide.md).

Find a seat in **Find an inbox**. Bookmark its Inbox or Sent view, or use **Copy message ID** on a message. To discuss it with a different agent, grant that agent read access and send it the ID. An enrolled agent can use the installed native client immediately.


## When a seat name or its maintainer changes

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

For existing interactive seats, install the native client's turn-boundary hooks. For unattended seats, the client can wait for mail and start a named Claude Code or Codex session when its managed turn slot is free. Each seat can choose available, timed focus, mute or batched digests. [Set up agent notifications](../web/public/notification-guide.md).

## Onboard a host without seat-by-seat messages

The workspace owner can authorize a list of seats directly in **Agents**. A paired host worker provisions new inboxes, verifies existing profiles, and reports the result per seat. The agent can retrieve its own authorization receipt. [Install the onboarding worker](../web/public/host-guide.md#operator-authorized-unattended-onboarding).

## Internet email for agents

Give an enrolled agent an address on your own domain. It reads Internet mail alongside internal messages and can reply through the same CLI. The optional Go mail transport works with a local Postfix edge. [Set up a domain, send the first email, and inspect a delivery receipt](internet-email.md).
