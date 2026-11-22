package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/rodriveracom/openagentinbox/internal/backupflow"
)

func workflowCommand(mode string, args []string) error {
	fs := flag.NewFlagSet(mode, flag.ContinueOnError)
	c := backupflow.Config{Mode: mode}
	fs.StringVar(&c.StateDir, "state-dir", "", "absolute path to private workflow directory")
	fs.StringVar(&c.Adapter, "connect-adapter", "", "absolute path to Connect ciphertext transport executable")
	fs.StringVar(&c.Age, "age", "age", "age executable")
	var receiptFile string
	if mode == "backup-run" {
		fs.StringVar(&c.Source, "db", "", "absolute mailbox SQLite path")
		fs.StringVar(&c.Instance, "instance", "", "stable mailbox instance ID")
		fs.StringVar(&c.Window, "window", "", "stable scheduled window, e.g. 2026-11-22")
		fs.StringVar(&c.Recipient, "recipient", "", "pinned age X25519 public recipient")
	} else {
		fs.StringVar(&c.Drill, "drill-id", "", "stable ID for this scheduled restore exercise")
		fs.StringVar(&receiptFile, "receipt", "", "trusted nightly JSON receipt")
		fs.StringVar(&c.Identity, "identity", "", "absolute path to custodian-supplied age identity")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected argument")
	}
	var r backupflow.Receipt
	if mode == "restore-drill" {
		f, e := os.Open(receiptFile)
		if e != nil {
			return e
		}
		defer f.Close()
		data, e := io.ReadAll(io.LimitReader(f, 16385))
		if e != nil {
			return e
		}
		if len(data) > 16384 || json.Unmarshal(data, &r) != nil {
			return errors.New("invalid nightly receipt")
		}
		c.Instance, c.Window = r.Instance, r.Window
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	out, err := backupflow.Run(ctx, c, r)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}
