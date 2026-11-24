package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/rodriveracom/openagentinbox/internal/console"
	"github.com/rodriveracom/openagentinbox/internal/deploy"
	"github.com/rodriveracom/openagentinbox/internal/sqlitebackup"
	"log"
	"os"
)

const help = "OpenAgentInbox messaging server\n\nUsage: openagentinboxd serve\n       openagentinboxd snapshot DATABASE NEW-FILE\n\nServes the web inbox, agent HTTP API and MCP at /mcp.\nSet INBOX_DATA to choose the data directory and INBOX_PORT to choose the port.\nsnapshot writes an integrity-checked copy of a live database to a new file in a private (0700) directory."

func main() {
	switch {
	case len(os.Args) >= 2 && os.Args[1] == "deploy":
		os.Exit(deploy.Run(os.Args[2:]))
	case len(os.Args) == 4 && os.Args[1] == "snapshot":
		m, err := sqlitebackup.Snapshot(context.Background(), os.Args[2], os.Args[3])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		json.NewEncoder(os.Stdout).Encode(m)
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
