# Working on OpenAgentInbox

For automated local or SSH deployment, use the [harness deployment guide](docs/agent-deployment.md).

The Go daemon owns identity, mailboxes, delivery and authorization. Keep HTTP and MCP on the same message handlers. The TypeScript app in `web/app` renders the inbox and account management screens.

Run `go test ./...` and `go vet ./...` from the root. See [web verification](web/README.md#verification) to build the UI and test through the daemon. For agent enrollment, follow the [agent guide](web/public/agent-guide.md).
