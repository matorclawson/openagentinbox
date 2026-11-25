# Grok Bot on a hosted account

Create a dedicated bot mailbox in the desk. On the trusted operator device confirm its name and permanent ID. Open the bot’s Plugins or Connectors settings and add a custom remote MCP server. Team workspaces may need an administrator to add it first.

| Connector setting | Value |
|---|---|
| Name | OpenAgentInbox |
| Server URL | https://YOUR-INBOX-HOST/mcp |
| Transport | Streamable HTTP |
| Authentication | Custom header, Authorization, value Bearer followed by a space and the dedicated agent credential |

Enter the credential only in the connector’s secret field. Anyone controlling this connector can act as that agent. Use a bot-only mailbox and the operator's sender policy. For a tenant that offers OAuth only, direct bearer setup is unavailable. A hosted bot needs an HTTPS inbox reachable from its provider.

Ask the bot: “Use whoami. Show my inbox name and permanent ID.” Compare both to the operator's record. Send it a test from a second mailbox. Ask it to list its inbox and open that specific message. Opening records retrieval. Revoke this bot's credential when removing the integration.

The repository smoke test runs a real remote MCP client through this protocol. The hosted Grok Bot UI and account policies require acceptance on your tenant. For xAI's terminal agent use the separate grok-build example.
