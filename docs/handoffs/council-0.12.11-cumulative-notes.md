# OpenAgentInbox changelog

## 0.12.11 cumulative handoff · 23 November 2026

The comparator is the tagged 0.10.4 source at b0b8d2a. The packaged application is 38164e1. This handoff updates documentation and preserves the 0.12.11 installer. The entries below identify when each change entered the council branch. Production acceptance is recorded separately by the operator.

| Release | What changed | Application commit |
|---|---|---|
| 0.11.0 | Added opt-in Internet addresses, inbound LMTP, original MIME retrieval, external sender provenance, linked replies and single-file attachments in the desk and native client. SQLite commits per-recipient outbound intent with the message. The relay worker tracks retries, permanent failures, uncertain handoffs and correlated delivery reports. Per-mailbox permission and quota govern external sends. Updated portable Ephemeral to the 0.3.0 candidate for idempotent backup starts and indexed recovery. | be24746 |
| 0.12.0 | Added owner domain setup in the UI and API, DNS guidance and ownership verification, default addresses, aliases with collision checks, sender controls and a dedicated edge setup helper. Added indexed conversation membership, Inbox and Sent views with historical backfill. Fixed permission refresh in Compose. | cf75f85, 60f8cfd |
| 0.12.1 | Grouped linked replies in the desk, surfaced recipient receipts and relative times, and made conversations directly addressable. | 80fd9c0 |
| 0.12.2 | Made subjects readable when agents embed envelope fields in the body. Added corpus-shaped load replay, improved the opposite-folder thread lookup using the existing index and corrected the last scheduled event in a replay window. | de57de2, 15d8706 |
| 0.12.3 | Applied shared brand tokens, typography and supplied Mator artwork to the web desk. | 7c9ae81 |
| 0.12.4 | Refined sender avatars and reader controls. Added appearance preferences, a dark theme and installed version in Settings. | 6f95ddd |
| 0.12.5 | Added scoped full-text and exact-ID search, individual outstanding requests, compact rows and return-to-list navigation. Existing bodies are indexed on first startup. | 92651ce |
| 0.12.6 | Added bounded navigation through long threads, matched excerpts, message anchors and surrounding replies. | 55d1c55 |
| 0.12.7 | Added UTC time navigation and time-bucket queries for large conversations. | 1bafdc2 |
| 0.12.8 | Replaced activity plots with touch-sized period selectors. | 6421c53 |
| 0.12.9 | Unified time navigation into one expandable month, week, day, daypart and hour outline with linkable selections and bounded hour pages. | e567951 |
| 0.12.10 | Separated the linked inbox identity, subject, per-direction counts and refresh control. Added narrow-screen checks for long names and nine-digit counts. | dbaa71a |
| 0.12.11 | Moved the outline to its own Timeline route. A conversation opens on its newest messages. Inbox and Sent offer direct Timeline links on grouped rows. Added reload, return and browser-history checks. | 38164e1 |

### Evidence and measurement boundaries

- **Internet mail.** On 0.12.0, all 300 scheduled messages persisted in one minute split across inbound, outbound and internal paths. This used loopback protocol fixtures on AMD64. The measurement excludes public delivery, Postfix, filtering and signing. The same release indexed 100,000 historical messages and became ready in 1.77 seconds on that host.
- **Internal traffic.** On 0.12.2, the one-minute 100, 1,000 and 10,000 sends/minute runs persisted every offer. Peak daemon RSS was 45.7, 56.6 and 56.6 MiB respectively. A separate trace-shaped 10,000/minute run peaked at 57.0 MiB. All ran on shared AMD64 with two Go processors. Current-release throughput and CAX11 capacity still require measurement.
- **Reader.** On 0.12.11, a synthetic 10,000-message thread traversed 250 disjoint pages with at most 40 envelopes each. Local HTTP median was 2.1 ms and p95 was 3.1 ms. This measures reading pages on loopback. Browser coverage included restricted access, time links, phone widths and back navigation.
- **Upgrade.** This handoff adds a direct 0.10.4 → 0.12.11 → 0.10.4 → 0.12.11 packaged-daemon rehearsal on disposable Linux AMD64 data. Owner session, 123 identities and credentials, message IDs, retry keys, cursor continuity and unrelated-reader denial survived. Production systemd, SMTP spool recovery, native ARM64 load and physical-phone testing remain staging work.
- **Integration scope.** The Dostoevsky pin remains the version delivered in 0.10.4. First-open, linked-reply and deadline tracking predate this range. A body fetch records retrieval. The public SMTP edge owns transport, TLS, signing and filtering. Token savings and inbox placement were not measured.

## Historical record · OpenAgentInbox changelog · 0.9.1 through 0.10.4

The Architect reported 0.9.1 live on 23 November 2026. The cumulative staging candidate is 0.10.4 at commit b0b8d2a12ccc97142f6d6db33a1374487265cea0. This handoff adds documentation and benchmark receipts to the existing release package. The verified installer and tagged application source match the earlier 0.10.4 delivery.

| Release | Change visible to operators and agents | Source commit |
|---|---|---|
| 0.9.1 | Production baseline. The Architect verified permanent IDs for two simultaneous seats, refusal of unmapped and detached callers, and a seat on a separate tmux socket. The shared launcher belongs to the host installation. | a2671c7 |
| 0.9.2 | Grouped and searchable onboarding receipts make large rosters easier to inspect. | 28dc78c |
| 0.10.0 | A recipient's first body fetch records an open. Linked replies record a reply. Action requests carry deadlines, with server-side overdue checks and silent host delivery probes. An open records retrieval, not comprehension. | a9580a5 |
| 0.10.1 | Overlapping sends can share a SQLite commit. The first send starts immediately. The console and store share a writer, and common reads use committed reader snapshots. Bounded Dostoevsky histograms and HyperLogLog expose handler latency and estimated distinct recipient counts. | 3d648bf, 9cdc466 |
| 0.10.2 | Notification cursors, workspace activity and other standalone reads use reader connections. Idle deadline checks inspect the deadline index before taking the writer. SQLite page-cache targets are 4 MiB for the writer and 1 MiB per reader. Included the Dostoevsky performance candidate. | a467aab, d44f940, 46257e9 |
| 0.10.3 | Updated to Dostoevsky's concurrent-publishing candidate, locally pinned as v0.3.0-rc.2. SQLite still commits the message, delivery and retry records together. | 9086039 |
| 0.10.4 | Integrated the post-0.3.0 handoff, locally pinned as v0.3.0-rc.3. It includes Linux journal data sync with a full-sync fallback, reusable journal encoding storage and a repeated-retention fix. Preserved existing web dependency versions. | 8e9560e, b0b8d2a |

## What 0.10.4 demonstrated

- A disposable 0.9.1 → 0.10.4 → 0.9.1 → 0.10.4 rehearsal preserved 123 distinct synthetic mailbox identities and credentials, the owner session, cursors, four messages and their retry keys. One message was written after the upgrade. An unrelated credential was denied access throughout.
- At 100 messages/min for one minute, both 0.10.3 and 0.10.4 persisted every offer. In the first 5,000-offer burst the driver skipped 42 and 209 offers respectively before submission. Across the three additional 0.10.4 bursts it skipped 249, 256 and zero. Every submitted send persisted in these runs.
- The AMD64 daemons' 6,025,681-byte executable code sections differed by one byte in the deployment version check. OpenAgentInbox uses SQLite for mail. Dostoevsky's journal publishing results measure a separate path. These mailbox measurements establish neither a release speedup nor the cause of the observed timing variation.
- Go tests and vet passed. The embedded Dostoevsky module passed its race-enabled tests and vet. The web tests passed with the optional external OpenPassword integration skipped. Linux AMD64 installation was exercised. Native ARM64 performance and the production systemd upgrade remain to be verified.

See BENCHMARK.md for the original comparison, measurements/recheck/ for the follow-up trials, and ARCHITECT-PROMPT.md for staging acceptance.
