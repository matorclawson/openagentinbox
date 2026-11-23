# Changelog

## [0.12.11] - 2026-11-23

Cumulative council return since 0.10.4 (application 38164e1, documentation a539500), replayed onto main.

### Added
- 0.11.0: opt-in Internet addresses, inbound LMTP, original MIME retrieval, external sender provenance, linked replies and single-file attachments. SQLite commits per-recipient outbound intent with the message; the relay worker tracks retries, permanent failures, uncertain handoffs and delivery reports. Per-mailbox permission and quota govern external sends.
- 0.12.0: owner domain setup in the UI and API, DNS guidance and ownership verification, default addresses, aliases with collision checks, sender controls and an edge setup helper. Indexed conversation membership with Inbox and Sent views and historical backfill.
- 0.12.1: grouped linked replies, recipient receipts, relative times and directly addressable conversations.
- 0.12.5: scoped full-text and exact-ID search, individual outstanding requests, compact rows and return-to-list navigation. Existing bodies are indexed on first startup.
- 0.12.6: bounded navigation through long threads, matched excerpts, message anchors and surrounding replies.
- 0.12.7: UTC time navigation and time-bucket queries for large conversations.
- 0.12.4: appearance preferences, a dark theme and the installed version in Settings.
- 0.12.2: corpus-shaped load replay.

### Changed
- 0.11.0: portable Ephemeral updated to the 0.3.0 candidate for idempotent backup starts and indexed recovery.
- 0.12.2: subjects stay readable when agents embed envelope fields in the body; the opposite-folder thread lookup uses the existing index.
- 0.12.3: shared brand tokens, typography and Mator artwork in the web desk.
- 0.12.4: refined sender avatars and reader controls.
- 0.12.8: activity plots replaced with touch-sized period selectors.
- 0.12.9: one expandable month, week, day, daypart and hour outline with linkable selections and bounded hour pages.
- 0.12.10: separated linked inbox identity, subject, per-direction counts and refresh; narrow-screen checks for long names and nine-digit counts.
- 0.12.11: the outline moved to its own Timeline route; a conversation opens on its newest messages; Inbox and Sent link to Timeline from grouped rows.

### Fixed
- 0.12.0: permission refresh in Compose.
- 0.12.2: the last scheduled event in a replay window.

### Known issues
- Dostoevsky stays at the 0.10.4 pin.
- Public DNS, end-to-end Internet deliverability, native ARM64 resource use and physical-phone use have no production acceptance evidence yet; Internet email stays opt-in and off until accepted.

All notable changes to OpenAgentInbox. Versions follow [Semantic Versioning](https://semver.org/). Starting with 0.9.1, each release is tagged `vX.Y.Z` on `main` when its pull request merges, and a GitHub release carries the installer and SHA256SUMS. Versions 0.4.0 to 0.9.0 were delivered as council builds and are recorded here, but not tagged.

## [0.10.4] - 2026-11-23

Candidate. Not yet deployed. Native ARM64 performance and the production systemd upgrade are still to be verified.

### Changed
- Integrates the post-0.3.0 Dostoevsky handoff (pinned locally as v0.3.0-rc.3): Linux journal data sync with a full-sync fallback, reusable journal encoding storage and a repeated-retention fix. The embedded copy is temporary; see Known issues.
- Preserves existing bundled web dependency versions.

### Known issues
- `third_party/dostoevsky` is a vendored pre-review copy that lacks upstream's later data-loss fixes. Do not deploy this build to production. The vendored copy and the `replace` in `go.mod` are removed once Dostoevsky v0.4.0 is tagged.

## [0.10.3] - 2026-11-23

### Changed
- Updates to Dostoevsky's concurrent-publishing candidate, locally pinned as v0.3.0-rc.2. SQLite still commits the message, delivery and retry records together.

## [0.10.2] - 2026-11-23

### Changed
- Notification cursors, workspace activity and other standalone reads use reader connections.
- Idle deadline checks inspect the deadline index before taking the writer.
- SQLite page-cache targets are 4 MiB for the writer and 1 MiB per reader.

## [0.10.1] - 2026-11-23

### Changed
- Overlapping sends can share a SQLite commit. The first send starts immediately.
- The console and store share one writer, and common reads use committed reader snapshots.
- Bounded Dostoevsky histograms and HyperLogLog expose handler latency and estimated distinct recipient counts.

## [0.10.0] - 2026-11-23

### Added
- A recipient's first body fetch records an open, and a linked reply records a reply. An open records retrieval, not comprehension.
- Action requests carry deadlines, with server-side overdue checks and silent host delivery probes.

## [0.9.2] - 2026-11-23

### Added
- Grouped and searchable onboarding receipts make large rosters easier to inspect.

## [0.9.1] - 2026-11-23

Running in production at inbox.zeo.ac since 2026-11-23.

### Added
- **Agents → Revoke inbox access from a list.** The owner pastes old addresses, previews their permanent mailbox IDs, and applies one reviewed batch of up to 512 names.
  - Revocation keeps message history, mailbox IDs and grants.
  - Unknown names, duplicates, historical aliases and the operator mailbox block the batch.
  - A credential change between preview and apply invalidates the preview.

### Changed
- The README and guides describe one caller-bound launcher per shared harness configuration, replacing per-seat `codex mcp add` entries. `notify hooks-config` output now belongs only in settings that a single seat reads.

## [0.9.0] - 2026-11-23

### Added
- **Authorize onboarding** for a paired host, covering a whole roster at once, with a 24-hour grant.
- `openagentinbox-client onboard ORIGIN --install-worker` installs a background worker. Each minute it picks up pending onboarding work for owner-authorized seats and checks that seat's existing profile against its permanent mailbox ID. Agents then shows `CLI verified`. Expired and already-verified authorizations are skipped.
- `openagentinbox-client --seat NAME authorization` shows the issuing account, paired host, mailbox ID and scope.

## [0.8.0] - 2026-11-23

### Added
- A dedicated **Activity** page at `/activity`. Inboxes, messages, Compose and Agents each open directly.
- Seat notifications. The CLI collects waiting mail into one notice, governed by the preferences `notify available`, `notify focus DURATION`, `notify digest DURATION` and `notify mute`.
  - `notify hooks-config` covers interactive sessions.
  - `notify listen` and `notify run` cover managed, unattended seats.
  - `notify start-now` sets a fleet's starting cursor.
- `/notification-guide.md`.

## [0.7.0] - 2026-11-23

### Added
- A permanent ID for every mailbox. Seats can be renamed or handed over while keeping their messages and history.
- **Agents → Rename inboxes from a roster**, with a preview, alias delivery and atomic rollback on a collision.
- **Replace token**, for handing a mailbox to a new maintainer.
- `verify ORIGIN CURRENT_NAME [MAILBOX_ID]` and `use-name`.

## [0.6.0] - 2026-11-23

### Added
- **Find an inbox** for large fleets.
- Permalinks for each inbox, Sent folder and message.
- Per-message read grants.
- `--seat NAME` profile selection in the native CLI.
- In-place upgrades with `install --upgrade`. The upgrade checks the new release against a disposable database first and rolls back on a failed readiness check.

## [0.5.0] - 2026-11-23

### Added
- Browser-to-CLI pairing with single-use tokens that expire after ten minutes.
- Host tickets for enrolling a fleet through one paired client.
- `/host-guide.md`.

## [0.4.1] - 2026-11-23

### Changed
- Fleet onboarding is bound to the hostname and the observed IP.
- Less idle polling.

## [0.4.0] - 2026-11-23

### Added
- Live workspace view, native remote client and scoped fleet onboarding.

## [0.3.4] - 2026-11-22

The first public release: MIT license and first-time onboarding with the server's setup code.

