package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func xmlArg(v string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(v))
	return "<string>" + b.String() + "</string>"
}
func unitArg(v string) string {
	return strconv.Quote(strings.NewReplacer("%", "%%", "$", "$$").Replace(v))
}

func workerDefinition(platform, home, exe, origin string) (string, string, string, error) {
	id := "openagentinbox-onboard-" + fileID(origin)[:16]
	switch platform {
	case "darwin":
		args := ""
		for _, v := range []string{exe, "onboard", origin, "--watch"} {
			args += xmlArg(v)
		}
		body := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key>` + xmlArg(id) +
			`<key>ProgramArguments</key><array>` + args + `</array><key>RunAtLoad</key><true/><key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict><key>ThrottleInterval</key><integer>60</integer></dict></plist>`
		return id, filepath.Join(home, "Library/LaunchAgents", id+".plist"), body, nil
	case "linux":
		body := "[Unit]\nDescription=OpenAgentInbox paired host onboarding\n[Service]\nExecStart=" + unitArg(exe) + " onboard " + unitArg(origin) + " --watch\nRestart=on-failure\nRestartSec=60\nUMask=0077\n[Install]\nWantedBy=default.target\n"
		return id + ".service", filepath.Join(home, ".config/systemd/user", id+".service"), body, nil
	default:
		return "", "", "", errors.New("use onboard ORIGIN --watch with this host’s process supervisor")
	}
}

func installWorker(ctx context.Context, home, origin string) error {
	if !validOrigin(origin) {
		return errors.New("invalid workspace origin")
	}
	if os.Geteuid() == 0 {
		return errors.New("run under the paired host user, without sudo")
	}
	host, err := readProfile(hostPath(home, origin))
	if err != nil {
		return err
	}
	if host.Origin != origin {
		return errors.New("paired profile belongs to a different workspace")
	}
	if _, err = request(ctx, host, "pair/status", nil); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	id, path, body, err := workerDefinition(runtime.GOOS, home, exe, origin)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// Never replace another service or publish a partially written definition.
	f, err := os.CreateTemp(filepath.Dir(path), ".onboarding-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(body)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	if err = os.Link(f.Name(), path); os.IsExist(err) {
		old, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if string(old) != body {
			return fmt.Errorf("existing service definition differs: %s", path)
		}
	} else if err != nil {
		return err
	}
	run := func(name string, args ...string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	fmt.Println("User service:", path)
	if runtime.GOOS == "linux" {
		if err = run("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		return run("systemctl", "--user", "enable", "--now", id)
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	if exec.CommandContext(ctx, "launchctl", "print", domain+"/"+id).Run() == nil {
		return nil
	}
	return run("launchctl", "bootstrap", domain, path)
}
