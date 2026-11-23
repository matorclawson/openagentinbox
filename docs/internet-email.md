# Give an agent an Internet inbox

An enrolled mailbox such as `support` can receive at `support@agents.example.org`. Its existing seat credential reads both Internet and internal mail. Current names and retained aliases route to the permanent mailbox. Revocation stops new address lookups. The owner receives mail at `operator@agents.example.org`. Use a dedicated mail subdomain for the first deployment.

## Architecture and trust

The Go daemon commits the mailbox entry and its MIME original in one SQLite transaction before returning LMTP success. A retry of the same envelope and exact edge bytes for the same box returns its original ID. The public MTA must prepend its own Received trace on each new external submission. A fresh submission with changed bytes has a new identity. An Internet Message-ID alone cannot authorize access or establish uniqueness.

The outbound send API commits a message and a per-recipient outbox entry together. A single worker hands it to the explicitly configured local SMTP relay. Body, attachments, address and Message-ID are frozen for retries. External traffic uses one recipient per operation. Internal traffic continues to use the existing Go commit and notification path. There is no workflow database write per email. Ephemeral 0.3.0 provides idempotent starts for new backup workflows and indexed discovery of interrupted windows.

## Connect an SMTP edge

These are staging examples for a dedicated Linux Postfix host. Review your existing domain classes, chroot paths, ports, maps and mail policy before applying them. The installer leaves the edge under the mail administrator's control. Enabling a domain requires both the edge and the app. An installation using only internal mail needs neither an MX nor an SMTP listener.

1. Choose the mail domain, MX hostname and outbound route. Set up public MX and A/AAAA records and verify inbound TCP 25. Choose an authenticated upstream smarthost if the VPS cannot send on port 25. Install the edge's TLS certificate, configure outbound DKIM signing, the domain's SPF and DMARC policy, and inbound spam filtering at the edge. Confirm reverse DNS and alignment with a controlled external mailbox.
2. Create a service-owned, group-traversable directory inside the Postfix queue root. On a standard Debian layout:

   ```sh
   sudo install -d -o openagentinbox -g postfix -m 2750 /var/spool/postfix/oai
   ```

   The daemon creates two mode-0660 Unix sockets. The setgid directory supplies the Postfix group. The Postfix user needs traversal and socket access only. Grant the systemd service a writable exception for this directory using a drop-in:

   ```ini
   [Service]
   ReadWritePaths=/var/spool/postfix/oai
   ```

3. Add these lines to the installation's settings environment and restart the daemon after reloading systemd. Use original mailbox names for the sender allowlist, including after a rename. The relay must be a literal loopback IP and port. Only trusted processes should have access to this relay.

   ```ini
   INBOX_EMAIL_DOMAIN=agents.example.org
   INBOX_EMAIL_SOCKET=/var/spool/postfix/oai/lmtp
   INBOX_EMAIL_RELAY=127.0.0.1:25
   INBOX_EMAIL_SENDERS="support operator"
   ```

   Leave out the last two settings for inbound-only operation. The domain and local relay identity must remain stable while the outbox drains. Check that the relay presents the correct DKIM and transport policy for the domain and authorizes only intended sources.

4. For a dedicated edge, route this **virtual mailbox domain** to the daemon. The lookup socket implements the Postfix socketmap protocol. It returns current recipients, retained aliases, and scoped bounce routes. Use a Postfix build with socketmap support (`postconf -m`). Example for an unchrooted lookup process and an LMTP process using the queue directory:

   ```ini
   virtual_mailbox_domains = agents.example.org
   virtual_mailbox_maps = proxy:socketmap:unix:/var/spool/postfix/oai/lmtp.map:inboxes
   virtual_transport = lmtp:unix:oai/lmtp
   lmtp_destination_recipient_limit = 1
   smtpd_reject_unlisted_recipient = yes
   smtpd_relay_restrictions = permit_mynetworks, reject_unauth_destination
   mynetworks = 127.0.0.0/8 [::1]/128
   message_size_limit = 1000000
   ```

   Check the socket path as seen by both Postfix processes, including proxymap if chrooted. Preserve other hosted domains and merge any existing map lists. This domain must be absent from mydestination. Keep recipient extensions from rewriting the issued `bounce+TOKEN` address. Configure rate and disk reserve limits on the public edge. Validate with `postfix check` and `postmap -q support@agents.example.org socketmap:unix:/var/spool/postfix/oai/lmtp.map:inboxes` (expect `1`). Test a nonexistent seat, a revoked seat, and a forbidden third-party relay at RCPT from **outside** mynetworks. A lookup outage must produce a temporary failure.

## Agent and human use

```sh
openagentinbox-client --seat support whoami
openagentinbox-client --seat support inbox
openagentinbox-client --seat support read MESSAGE_ID
openagentinbox-client --seat support send person@example.net 'Revised dates' 'Tuesday works.'
openagentinbox-client --seat support reply MESSAGE_ID person@example.net 'Re: Revised dates' 'Confirmed.'
openagentinbox-client --seat support email-file person@example.net 'Your report' 'See attached.' report.pdf
openagentinbox-client --seat support receipt SENT_MESSAGE_ID
openagentinbox-client --seat support email-original MESSAGE_ID | jq -r .base64 | base64 -d > original.eml
```

The send command prints a retry UUID. Reuse it for the same submission following a timeout. The desk offers Compose, original email download and a transport status. The agent HTTP endpoints and MCP send, read and receipt tools use the same permissions. MIME originals, including attachments, are fetched on demand through the CLI or HTTP. Agents should treat external bodies, From names, links and attachments as untrusted data. A local-looking From header never grants seat permissions. Replies to incoming email use the From address and link a supported original Message-ID. Review alternate Reply-To addresses in the original if required.

## Queue recovery and operations

* `queued` is durable in SQLite. The worker retries temporary errors with backoff for up to 12 attempts. `failed` covers conclusive SMTP rejections and exhausted retries. `relayed` means that the local MTA accepted responsibility. Remote DSNs are reported separately as `remote_action` and `remote_status` when they match the unique envelope return path, null reverse path and original recipient. Delivery reports are evidence from the destination. A remote open is unknown. Bounces are also ordinary inbox mail.
* `uncertain` requires checking the edge queue or logs for the stable Message-ID `<OPENAGENTINBOX-ID@domain>`. A lost final DATA reply or an expired in-flight attempt can produce this state. Owner API `GET /api/desk/email/uncertain?after=ID` pages up to 100. With the owner's existing session and same-origin HTTP POST, submit `{"id":"…","attempt":1,"decision":"confirmed","evidence":"edge-log-reference"}` to `/api/desk/email/reconcile`. Use `retry` only after establishing the edge did not accept the message. Use `cancel` to record a terminal failure. The database rejects stale decisions.
* Four concurrent LMTP sessions, eight recipient lookup sessions, one relay worker, 1 MiB per inbound message, one outgoing attachment up to 96 KiB, and 120 new external sends per sending mailbox per rolling hour. The API request also fits within the existing 240,000-byte JSON limit. The inbox returns bounded text previews. HTML-only and unsupported MIME can be retrieved as original bytes. The worker parks on an in-process wake hint with a 30-second recovery scan.
* Back up the live database with the application's online backup. It includes MIME originals, outbox rows and operator decisions. Monitor database/WAL bytes, backup restore time, edge queue age, disk free, oldest due outbox age, permanent refusals and uncertain attempts. SMTP spool and logs belong to the MTA. Capacity-plan original content per recipient. No automatic purge is enabled. Restoring an old snapshot requires reconciling queued sends against edge logs before enabling the relay again.
* SMTPUTF8 addresses, IMAP, POP, public SMTP password submission, automatic complaint suppression and comprehensive MIME client rendering are outside this candidate. Agents submit through their authenticated Go API, CLI or MCP. Production acceptance requires a real public edge; see the Architect handoff in the release bundle.
