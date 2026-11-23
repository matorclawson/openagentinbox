// Package deploy implements the unattended installer contract.
package deploy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

const Version = "0.10.2"

type report map[string]any
type options struct {
	action, target, host, domain, root, installDir, data string
	port                                                 int
	existing                                             bool
}

var domainRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
var hostRE = regexp.MustCompile(`^([a-zA-Z0-9_][a-zA-Z0-9_.-]*@)?[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

func setting(k, v string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return v
}
func Run(args []string) int {
	r, e := run(args)
	if r == nil {
		r = report{}
	}
	r["schema"] = "openagentinbox.deploy/v1"
	if e != nil {
		r["status"] = "error"
		r["error"] = e.Error()
	}
	json.NewEncoder(os.Stdout).Encode(r)
	if e != nil {
		return 1
	}
	if r["status"] == "not_ready" {
		return 2
	}
	return 0
}
func run(args []string) (report, error) {
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help") {
		return report{"status": "ok", "actions": []string{"plan", "apply", "status"}, "targets": []string{"local", "vps", "ssh"}, "flags": []string{"--target", "--host", "--domain", "--existing-proxy", "--port"}, "exit_codes": map[string]int{"success": 0, "error": 1, "not_ready": 2}}, nil
	}
	if len(args) == 0 {
		return nil, errors.New("use agent help, plan, apply or status")
	}
	o := options{action: args[0], root: os.Getenv("INBOX_INSTALL_SOURCE")}
	if o.action != "apply" && o.action != "status" && o.action != "plan" {
		return nil, errors.New("unknown deployment action")
	}
	f := flag.NewFlagSet("agent", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.target, "target", "local", "local, vps or ssh")
	f.StringVar(&o.host, "host", "", "SSH alias")
	f.StringVar(&o.domain, "domain", "", "public DNS name")
	f.BoolVar(&o.existing, "existing-proxy", false, "use existing proxy")
	f.IntVar(&o.port, "port", 4310, "local loopback port")
	if e := f.Parse(args[1:]); e != nil {
		return nil, e
	}
	if f.NArg() != 0 {
		return nil, errors.New("unexpected positional argument")
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return nil, e
	}
	o.installDir = setting("INBOX_INSTALL_DIR", filepath.Join(home, ".local/opt/openagentinbox"))
	o.data = setting("INBOX_DATA", filepath.Join(home, ".local/share/openagentinbox"))
	switch o.target {
	case "local":
		if o.host != "" || o.domain != "" || o.existing || o.port < 1 || o.port > 65535 || !filepath.IsAbs(o.data) || !filepath.IsAbs(o.installDir) {
			return nil, errors.New("local requires absolute install and data paths and a valid loopback port")
		}
		return o.local()
	case "ssh", "vps":
		if len(o.domain) > 253 || !domainRE.MatchString(o.domain) || o.port != 4310 {
			return nil, errors.New("provide a DNS name using --domain; VPS uses port 4310")
		}
		if o.target == "ssh" {
			if !hostRE.MatchString(o.host) {
				return nil, errors.New("provide --host user@host or an SSH config alias")
			}
			return o.ssh()
		}
		if o.host != "" {
			return nil, errors.New("--host requires --target ssh")
		}
		return o.vps()
	default:
		return nil, errors.New("unknown target")
	}
}

type health struct {
	Product, Instance, Version string
	Setup                      *bool
}

func probe(origin string) (health, error) {
	var h health
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, "GET", origin+"/api/health", nil)
	if e != nil {
		return h, e
	}
	c := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := c.Do(req)
	if e != nil {
		return h, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return h, fmt.Errorf("health returned HTTP %d", res.StatusCode)
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&h); e != nil {
		return h, e
	}
	if h.Product != "openagentinbox" || len(h.Instance) != 32 || h.Setup == nil {
		return h, errors.New("unexpected health response")
	}
	return h, nil
}
func ready(r report, h health, origin, data string) report {
	r["status"] = "ready"
	r["origin"] = origin
	r["version"] = h.Version
	r["owner_setup_required"] = *h.Setup
	if *h.Setup {
		r["next"] = "owner_setup"
		r["setup_code_file"] = filepath.Join(data, "setup-code")
	} else {
		r["next"] = "enroll_agent"
	}
	r["discovery_url"] = origin + "/.well-known/openagentinbox.json"
	r["mcp_url"] = origin + "/mcp"
	return r
}
func command(dir string, env []string, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	c.Stdout = os.Stderr
	c.Stderr = os.Stderr
	return c.Run()
}
func (o options) local() (report, error) {
	origin := fmt.Sprintf("http://127.0.0.1:%d", o.port)
	r := report{"target": "local", "origin": origin, "install_dir": o.installDir, "data_dir": o.data}
	if o.action == "plan" {
		r["status"] = "planned"
		r["action"] = "install_and_start_background_process"
		return r, nil
	}
	idPath := filepath.Join(o.installDir, fmt.Sprintf("agent-%d.instance", o.port))
	if o.action == "apply" {
		if e := os.MkdirAll(o.installDir, 0700); e != nil {
			return r, e
		}
		lock, e := os.OpenFile(filepath.Join(o.installDir, "agent.lock"), os.O_CREATE|os.O_RDWR, 0600)
		if e != nil {
			return r, e
		}
		defer lock.Close()
		if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
			return r, errors.New("another local deployment is running; retry")
		}
	}
	id, e := os.ReadFile(idPath)
	if e != nil && !os.IsNotExist(e) {
		return r, e
	}
	h, err := probe(origin)
	if err == nil {
		if string(id) != h.Instance+"\n"+filepath.Clean(o.data) {
			return r, errors.New("port belongs to another inbox; inspect the existing service or choose --port")
		}
		if o.action == "apply" && h.Version != Version {
			return r, errors.New("another version is running; stop it before upgrading")
		}
		r["changed"] = false
		return ready(r, h, origin, o.data), nil
	}
	if o.action == "status" {
		r["status"] = "not_ready"
		return r, nil
	}
	l, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", o.port))
	if e != nil {
		return r, fmt.Errorf("port unavailable: %w", e)
	}
	l.Close()
	if o.root == "" {
		return r, errors.New("apply using the installer")
	}
	if e = command(o.root, []string{"INBOX_INSTALL_ONLY=1"}, "bash", filepath.Join(o.root, "install-openagentinbox.sh")); e != nil {
		return r, fmt.Errorf("installation failed: %w", e)
	}
	var buf [16]byte
	rand.Read(buf[:])
	instance := hex.EncodeToString(buf[:])
	if e = os.WriteFile(idPath, []byte(instance+"\n"+filepath.Clean(o.data)), 0600); e != nil {
		return r, e
	}
	logPath := filepath.Join(o.installDir, "agent-daemon.log")
	r["log_file"] = logPath
	logFile, e := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		return r, e
	}
	defer logFile.Close()
	c := exec.Command(filepath.Join(o.installDir, "active/openagentinboxd"), "serve")
	c.Dir = o.installDir
	c.Stdout = logFile
	c.Stderr = logFile
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	c.Env = append(os.Environ(), "INBOX_LISTEN=127.0.0.1", "INBOX_ORIGIN="+origin, fmt.Sprintf("INBOX_PORT=%d", o.port), "INBOX_DATA="+o.data, "INBOX_UI="+filepath.Join(o.installDir, "active/ui"), "INBOX_DEPLOY_INSTANCE="+instance)
	if e = c.Start(); e != nil {
		return r, e
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if h, e = probe(origin); e == nil && h.Instance == instance {
			r["changed"] = true
			r["pid"] = c.Process.Pid
			return ready(r, h, origin, o.data), nil
		}
		select {
		case <-done:
			return r, errors.New("daemon exited; see log_file")
		case <-time.After(150 * time.Millisecond):
		}
	}
	r["status"] = "not_ready"
	r["next"] = "retry_status"
	r["pid"] = c.Process.Pid
	return r, nil
}
func (o options) vps() (report, error) {
	origin := "https://" + o.domain
	r := report{"target": "vps", "origin": origin, "unit": "openagentinbox.service", "existing_proxy": o.existing}
	if o.action == "plan" {
		r["status"] = "planned"
		r["requires"] = []string{"Linux with systemd", "root or noninteractive sudo", "DNS and HTTPS routing"}
		return r, nil
	}
	active := func() bool {
		return exec.Command("systemctl", "is-active", "--quiet", "openagentinbox.service").Run() == nil
	}
	if o.action == "apply" {
		if o.root == "" {
			return r, errors.New("apply using the packaged installer")
		}
		args := []string{filepath.Join(o.root, "web/deploy/install-native.sh"), o.domain}
		if o.existing {
			args = append(args, "--existing-proxy")
		}
		// The VPS installer owns service configuration and locking. Record the
		// completed artifact and routing settings inside its critical section.
		if e := command(o.root, nil, "bash", args...); e != nil {
			return r, fmt.Errorf("system installation failed: %w", e)
		}
	}
	if !active() {
		r["status"] = "not_ready"
		r["next"] = "inspect_systemd_service"
		return r, nil
	}
	h, e := probe("http://127.0.0.1:4310")
	if e != nil {
		r["status"] = "not_ready"
		r["next"] = "inspect_systemd_service"
		return r, nil
	}
	pub, e := probe(origin)
	if e != nil || pub.Instance != h.Instance {
		r["status"] = "not_ready"
		r["loopback_ready"] = true
		r["next"] = "verify_dns_and_https_then_retry_status"
		return r, nil
	}
	r["instance"] = h.Instance
	return ready(r, h, origin, "/var/lib/openagentinbox"), nil
}
func (o options) ssh() (report, error) {
	r := report{"target": "ssh", "host": o.host, "origin": "https://" + o.domain}
	if o.action == "plan" {
		r["status"] = "planned"
		r["requires"] = []string{"configured SSH host key", "noninteractive authentication", "root or sudo -n", "packaged installer", "DNS and HTTPS routing"}
		return r, nil
	}
	args := " --target vps --domain " + o.domain
	if o.existing {
		args += " --existing-proxy"
	}
	var input io.Reader
	script := "if [ -x /opt/openagentinbox/active/openagentinboxd ]; then exec /opt/openagentinbox/active/openagentinboxd deploy status" + args + "; else echo '" + `{"schema":"openagentinbox.deploy/v1","status":"not_ready","next":"apply"}` + "'; exit 2; fi"
	if o.action == "apply" {
		source := os.Getenv("INBOX_INSTALL_ARCHIVE")
		if source == "" {
			return r, errors.New("SSH apply requires the packaged installer")
		}
		payload, e := os.Open(source)
		if e != nil {
			return r, e
		}
		defer payload.Close()
		digest := sha256.New()
		if _, e = io.Copy(digest, payload); e != nil {
			return r, e
		}
		if _, e = payload.Seek(0, io.SeekStart); e != nil {
			return r, e
		}
		input = payload
		script = `set -eu; umask 077; f=$(mktemp); trap 'rm -f "$f"' EXIT; cat > "$f"; printf '%s  %s\n' ` + hex.EncodeToString(digest.Sum(nil)) + ` "$f" | sha256sum -c --status; if [ "$(id -u)" = 0 ]; then set -- bash; else set -- sudo -n bash; fi; "$@" "$f" agent apply` + args
	}
	c := exec.Command("ssh", "-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "--", o.host, script)
	c.Stdin = input
	c.Stderr = os.Stderr
	data, e := c.Output()
	var remote report
	if json.Unmarshal(data, &remote) != nil || remote["schema"] != "openagentinbox.deploy/v1" {
		return r, errors.New("SSH or remote installer failed; see stderr")
	}
	if e != nil && remote["status"] != "not_ready" {
		return r, fmt.Errorf("remote deployment failed: %v", remote["error"])
	}
	remote["target"] = "ssh"
	remote["host"] = o.host
	remote["origin"] = "https://" + o.domain
	if remote["status"] == "ready" {
		h, err := probe("https://" + o.domain)
		if err != nil || h.Instance != remote["instance"] {
			remote["status"] = "not_ready"
			remote["next"] = "verify_https_from_controller_then_retry_status"
		}
	}
	delete(remote, "instance")
	return remote, nil
}
