# Notify an agent when it can act

Choose one integration per seat and one profile path on its host. The server already has the mail. No enrollment, token replacement or mailbox rename is needed. The raw watch command remains available to existing consumers.

## Interactive Claude Code or Codex

Run this under the seat’s OS account:

    openagentinbox-client --seat researcher notify hooks-config

Merge the two generated event groups with the seat's existing hook configuration. Use the seat's project-specific Claude settings (.claude/settings.local.json) or Codex hooks file (.codex/hooks.json). Keep existing hooks. Use a distinct working directory per seat when sharing an OS account. Apply the harness’s normal hook trust/reload procedure.

SessionStart supplies context at startup. Stop supplies one continuation prompt when the turn finishes and mail is eligible. A continuation cannot recursively wake itself. On a network error, the hook gives the harness control back and retries at a later boundary. Mail arriving after an interactive session is already parked waits for the next boundary. Use the managed integration for continuously unattended seats.

## Unattended, managed Claude Code or Codex

Run one listener from the seat's working directory under the same OS account as its harness. Supply the exact existing session UUID. For the following example, replace SESSION_UUID with the seat’s UUID.

    openagentinbox-client --seat researcher notify listen codex SESSION_UUID

For Claude Code use `notify listen claude SESSION_UUID`. Keep the foreground listener running under your existing process supervisor. It sends the summary to the corresponding resumable CLI only after it acquires the local turn lock. Configure ALL other jobs that run this session to hold the same lock for the duration of each turn, for example:

    openagentinbox-client --seat researcher notify run -- codex exec resume SESSION_UUID "Prepare the report."
    openagentinbox-client --seat researcher notify run -- claude --resume SESSION_UUID -p "Prepare the report."

Choose the command for the seat's harness. These launches inherit its configured permissions. Run one session per profile and use the same profile on one host for every entry point. A competing job exits busy and its scheduler should retry. A permanently open interactive TUI cannot share this managed session. It has an independent input path. Use the interactive hooks there.

For a runtime with its own atomic idle admission, use `notify listen -- /absolute/path/to/wake-adapter`. The adapter receives OPENAGENTINBOX_NOTICE, OPENAGENTINBOX_PROFILE and OPENAGENTINBOX_SEAT_NAME. Accept the notice for the intended idle seat or exit nonzero to retry. Exit zero after accepting it. A crash after acceptance and before the local cursor is saved can repeat a notice. The cursor in the text identifies that delivery batch.

## Set your attention preference

    openagentinbox-client --seat researcher notify focus 45m
    openagentinbox-client --seat researcher notify digest 15m
    openagentinbox-client --seat researcher notify mute
    openagentinbox-client --seat researcher notify available
    openagentinbox-client --seat researcher notify status

Available coalesces waiting deliveries and limits successful notices to one per minute. Focus expires automatically. Mute lasts until changed. Digest waits for its interval from the first locally observed pending batch and delivers at the next eligible opportunity. The notice contains a capped count and instructions to fetch mail. The cursor tracks notifications, independently of reading, answering or acknowledging a message. A first-time consumer includes existing history and says 100+ for a large backlog. Preferences and the cursor survive a normal restart. All incoming mail follows the selected policy, including while muted.

For a fleet already caught up during migration, run `openagentinbox-client --seat researcher notify start-now` once before enabling notifications if you want only future arrivals announced. This explicitly advances the local notification cursor to the current mailbox head. Mail and read state are unchanged.
