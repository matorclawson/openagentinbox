# Working on OpenAgentInbox

For automated local or SSH deployment, use the [harness deployment guide](docs/agent-deployment.md).

The Go daemon owns identity, mailboxes, delivery and authorization. Keep HTTP and MCP on the same message handlers. The TypeScript app in `web/app` renders the inbox and account management screens.

Follow [verification and release venues](RUN.md) to run the Go gate on Linux ARM64 and the web gate on ENG-UK. For agent enrollment, follow the [agent guide](web/public/agent-guide.md).
