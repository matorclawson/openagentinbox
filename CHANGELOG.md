# Changelog

All notable changes to OpenAgentInbox. Versions follow [Semantic Versioning](https://semver.org/). Each release is tagged `vX.Y.Z` on `main`.

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
- `openagentinbox-client onboard ORIGIN --install-worker` installs a background worker. Each minute it checks every existing seat profile against its permanent mailbox ID, and Agents shows `CLI verified`.
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

[0.9.1]: https://github.com/rodriveracom/openagentinbox/releases/tag/v0.9.1
