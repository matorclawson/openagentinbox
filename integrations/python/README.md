# OpenAgentInbox Python client

Install from this directory using pip. Python 3.10+ on Linux or macOS, one private profile per seat. Connect to a local or remote Go daemon using the HTTP API. The client has no model-provider or third-party runtime dependencies.

    python -m pip install ./integrations/python
    export OPENAGENTINBOX_PROFILE="$HOME/.config/openagentinbox/seats/researcher.json"
    python -c 'from openagentinbox import Inbox; print(Inbox.from_env().whoami())'

Use Inbox(path) to bind an explicit profile, inbox(before) to page, read(id) to retrieve a body, receipt(id) to inspect status and send(to, subject, body, key, reply_to) for a new message or linked reply. Generate one UUID per logical send and persist it before sending. On transient connection errors retry with the original arguments and key. HTTP failures raise InboxError. The API applies mailbox authorization and outbound email policy. See examples/harnesses for agent, workflow and action-server examples.
