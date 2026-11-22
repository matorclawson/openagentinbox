package main

import (
	"fmt"
	"github.com/rodriveracom/openagentinbox/internal/console"
	"github.com/rodriveracom/openagentinbox/internal/deploy"
	"log"
	"os"
)

const help = "OpenAgentInbox messaging server\n\nUsage: openagentinboxd serve\n\nServes the web inbox, agent HTTP API and MCP at /mcp.\nSet INBOX_DATA to choose the data directory and INBOX_PORT to choose the port."

func main() {
	switch {
	case len(os.Args) >= 2 && os.Args[1] == "deploy":
		os.Exit(deploy.Run(os.Args[2:]))
	case len(os.Args) == 2 && os.Args[1] == "serve":
		if err := console.Run(); err != nil {
			log.Fatal(err)
		}
	case len(os.Args) == 1 || len(os.Args) == 2 && (os.Args[1] == "--help" || os.Args[1] == "help" || os.Args[1] == "-h") || len(os.Args) == 3 && os.Args[1] == "serve" && os.Args[2] == "--help":
		fmt.Println(help)
	default:
		fmt.Fprintln(os.Stderr, help)
		os.Exit(2)
	}
}
