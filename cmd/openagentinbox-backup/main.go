package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/rodriveracom/openagentinbox/internal/sqlitebackup"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "backup-run" || os.Args[1] == "restore-drill") {
		if err := workflowCommand(os.Args[1], os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	src := flag.String("db", "", "live SQLite database")
	out := flag.String("out", "", "new snapshot in a private directory")
	outdir := flag.String("out-dir", "", "private snapshot directory; creates a unique name")
	verify := flag.Bool("verify", false, "verify -db rather than copy")
	flag.Parse()
	if *out == "" && *outdir != "" {
		*out = filepath.Join(*outdir, "snapshot-"+time.Now().UTC().Format("20060102T150405.000000000Z")+".db")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	var m sqlitebackup.Manifest
	var e error
	if *verify {
		m, e = sqlitebackup.Verify(ctx, *src)
	} else {
		m, e = sqlitebackup.Create(ctx, *src, *out)
	}
	if e == nil && os.Getenv("OPENAGENTINBOX_BACKUP_PUT_URL") != "" {
		e = sqlitebackup.Upload(ctx, m, os.Getenv("OPENAGENTINBOX_BACKUP_PUT_URL"))
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(m)
}
