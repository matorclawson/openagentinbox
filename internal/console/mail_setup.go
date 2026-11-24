package console

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const setupMailSchema = `
CREATE TABLE IF NOT EXISTS mail_domains(domain TEXT PRIMARY KEY,mx TEXT NOT NULL,ip TEXT NOT NULL,selector TEXT NOT NULL,proof TEXT NOT NULL,active INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS mail_defaults(singleton INTEGER PRIMARY KEY CHECK(singleton=1),domain TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS mail_sender_policy(box TEXT PRIMARY KEY,enabled INTEGER NOT NULL);

`

type domainPlan struct {
	Domain   string `json:"domain"`
	MX       string `json:"mx"`
	IP       string `json:"ip"`
	Selector string `json:"selector"`
	Proof    string `json:"proof"`
	Active   bool   `json:"active"`
}

func (s *server) setupRoutes(add func(string, handler)) {
	add("GET /api/desk/email/setup", s.setupStatus)
	add("POST /api/desk/email/domains/{domain}/primary", s.choosePrimary)
	add("POST /api/desk/email/domains", s.saveDomain)
	add("POST /api/desk/email/domains/{domain}/check", s.checkDomain)
	add("POST /api/desk/email/domains/{domain}/activate", s.activateDomain)
	add("POST /api/desk/email/aliases", s.addAlias)
	add("GET /api/desk/email/addresses", s.listMailAddresses)
	add("POST /api/desk/email/sender", s.setSender)
}
func (s *server) hosted(ctx context.Context, domain string) (bool, error) {
	if s.email != nil && domain == s.email.domain && domain != "" {
		return true, nil
	}
	var b bool
	e := s.store.Reader().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM mail_domains WHERE domain=? AND active=1)", domain).Scan(&b)
	return b, e
}
func (s *server) primaryDomain() string {
	var chosen string
	if s.store.Reader().QueryRow("SELECT domain FROM mail_defaults WHERE singleton=1").Scan(&chosen) == nil {
		return chosen
	}
	if s.email != nil && s.email.domain != "" {
		return s.email.domain
	}
	var d string
	s.store.Reader().QueryRow("SELECT domain FROM mail_domains WHERE active=1 ORDER BY rowid LIMIT 1").Scan(&d)
	return d
}
func (s *server) choosePrimary(_ http.ResponseWriter, r *http.Request) (any, error) {
	d := strings.ToLower(r.PathValue("domain"))
	ok, e := s.hosted(r.Context(), d)
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, failure{409, "Activate this domain first"}
	}
	_, e = s.db.Exec("INSERT INTO mail_defaults VALUES(1,?) ON CONFLICT(singleton) DO UPDATE SET domain=excluded.domain", d)
	if e != nil {
		return nil, e
	}
	return s.setupStatus(nil, r)
}
func (s *server) maySend(box string) bool {
	if s.email == nil || s.email.relay == "" {
		return false
	}
	var enabled bool
	err := s.store.Reader().QueryRow("SELECT enabled FROM mail_sender_policy WHERE box=?", box).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return s.email.senders[box]
	}
	return err == nil && enabled
}
func (s *server) setupStatus(_ http.ResponseWriter, r *http.Request) (any, error) {
	rows, e := s.store.Reader().QueryContext(r.Context(), "SELECT domain,mx,ip,selector,proof,active FROM mail_domains ORDER BY domain")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	plans := []domainPlan{}
	for rows.Next() {
		var p domainPlan
		if e = rows.Scan(&p.Domain, &p.MX, &p.IP, &p.Selector, &p.Proof, &p.Active); e != nil {
			return nil, e
		}
		plans = append(plans, p)
	}
	rows.Close()
	legacy := ""
	if s.email != nil {
		legacy = s.email.domain
	}
	return object{"domains": plans, "edge": s.email != nil, "relay": s.email != nil && s.email.relay != "", "host_domain": legacy, "primary": s.primaryDomain()}, rows.Err()
}
func (s *server) saveDomain(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct{ Domain, MX, IP, Selector string }
	if e := input(r, &q); e != nil {
		return nil, e
	}
	q.Domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(q.Domain), "."))
	q.MX = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(q.MX), "."))
	if q.MX == "" {
		q.MX = "mx." + q.Domain
	}
	if q.Selector == "" {
		q.Selector = "mail"
	}
	ip := net.ParseIP(q.IP)
	if !validDomain(q.Domain) || !validDomain(q.MX) || ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || !dnsName.MatchString(q.Selector) {
		return nil, failure{400, "Enter a domain, a mail hostname, a public server IP and a DKIM selector"}
	}
	_, e := s.db.ExecContext(r.Context(), `INSERT INTO mail_domains(domain,mx,ip,selector,proof) VALUES(?,?,?,?,?) ON CONFLICT(domain) DO UPDATE SET mx=excluded.mx,ip=excluded.ip,selector=excluded.selector`, q.Domain, q.MX, ip.String(), q.Selector, token())
	if e != nil {
		return nil, e
	}
	return s.setupStatus(nil, r)
}
func (s *server) plan(r *http.Request) (domainPlan, error) {
	var p domainPlan
	e := s.store.Reader().QueryRowContext(r.Context(), "SELECT domain,mx,ip,selector,proof,active FROM mail_domains WHERE domain=?", r.PathValue("domain")).Scan(&p.Domain, &p.MX, &p.IP, &p.Selector, &p.Proof, &p.Active)
	return p, e
}

type dnsResolver interface {
	LookupTXT(context.Context, string) ([]string, error)
	LookupMX(context.Context, string) ([]*net.MX, error)
	LookupHost(context.Context, string) ([]string, error)
}
type dnsRecord struct {
	Type, Name, Value, Help string
	OK                      bool
	Error                   string `json:",omitempty"`
}

func inspectDNS(ctx context.Context, p domainPlan, res dnsResolver) []dnsRecord {
	ipType := "A"
	if net.ParseIP(p.IP).To4() == nil {
		ipType = "AAAA"
	}
	records := []dnsRecord{
		{Type: "TXT", Name: "_openagentinbox." + p.Domain, Value: "openagentinbox=" + p.Proof, Help: "Proves you control this domain."},
		{Type: ipType, Name: p.MX, Value: p.IP, Help: "Points the mail hostname to your public server. Use DNS only."},
		{Type: "MX", Name: p.Domain, Value: p.MX, Help: "Set priority 10. Add this only when the receiving server is ready."},
		{Type: "TXT", Name: p.Domain, Value: "v=spf1 mx -all", Help: "For direct sending from the MX only. For a sending provider, use its SPF instructions. Keep one SPF record."},
		{Type: "TXT", Name: p.Selector + "._domainkey." + p.Domain, Help: "Copy the public DKIM value from your mail signer. Keep the private key on the server."},
		{Type: "TXT", Name: "_dmarc." + p.Domain, Value: "v=DMARC1; p=none", Help: "Start with monitoring. Keep an existing DMARC policy if you have one."},
	}
	for i := range records {
		v := &records[i]
		var e error
		switch i {
		case 1:
			var ips []string
			ips, e = res.LookupHost(ctx, v.Name+".")
			for _, ip := range ips {
				if net.ParseIP(ip).Equal(net.ParseIP(p.IP)) {
					v.OK = true
				}
			}
		case 2:
			var mx []*net.MX
			mx, e = res.LookupMX(ctx, v.Name+".")
			for _, m := range mx {
				if strings.EqualFold(strings.TrimSuffix(m.Host, "."), p.MX) {
					v.OK = true
				}
			}
		default:
			var txt []string
			txt, e = res.LookupTXT(ctx, v.Name+".")
			matches := 0
			for _, t := range txt {
				switch i {
				case 0:
					if t == v.Value {
						matches++
					}
				case 3:
					if strings.HasPrefix(t, "v=spf1 ") {
						matches++
					}
				case 4:
					for _, tag := range strings.Split(t, ";") {
						tag = strings.TrimSpace(tag)
						if strings.HasPrefix(tag, "p=") && len(tag) > 2 {
							matches++
						}
					}
				case 5:
					if strings.HasPrefix(t, "v=DMARC1;") {
						matches++
					}
				}
			}
			v.OK = matches == 1
		}
		if e != nil {
			var de *net.DNSError
			if !errors.As(e, &de) || !de.IsNotFound {
				v.Error = "DNS lookup unavailable. Try again."
			}
		}
	}
	return records
}
func (s *server) domainChecks(r *http.Request) (domainPlan, []dnsRecord, error) {
	p, e := s.plan(r)
	if e != nil {
		return p, nil, e
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	res := s.resolver
	if res == nil {
		res = net.DefaultResolver
	}
	records := inspectDNS(ctx, p, res)
	if b, e := os.ReadFile(filepath.Join("/etc/openagentinbox/dkim-public", p.Domain+"."+p.Selector+".txt")); e == nil {
		v := strings.TrimSpace(string(b))
		if strings.HasPrefix(v, "v=DKIM1;") && !strings.ContainsAny(v, "\r\n") {
			records[4].Value = v
			txt, e := res.LookupTXT(ctx, records[4].Name+".")
			records[4].OK = false
			for _, t := range txt {
				if t == v && e == nil {
					records[4].OK = true
				}
			}
		}
	}
	return p, records, nil
}

// ownerProved reports whether exactly one ownership TXT record carries the plan's proof.
func ownerProved(ctx context.Context, p domainPlan, res dnsResolver) bool {
	txt, e := res.LookupTXT(ctx, "_openagentinbox."+p.Domain+".")
	if e != nil {
		return false
	}
	n := 0
	for _, t := range txt {
		if t == "openagentinbox="+p.Proof {
			n++
		}
	}
	return n == 1
}
func (s *server) checkDomain(_ http.ResponseWriter, r *http.Request) (any, error) {
	_, records, e := s.domainChecks(r)
	return object{"records": records}, e
}

// activateDomain checks the mail edge first so a request that cannot succeed makes no DNS
// lookup, then re-verifies only the ownership record, the one proof activation needs.
func (s *server) activateDomain(_ http.ResponseWriter, r *http.Request) (any, error) {
	if s.email == nil {
		return nil, failure{409, "Connect the mail server first"}
	}
	p, e := s.plan(r)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	dns := s.resolver
	if dns == nil {
		dns = net.DefaultResolver
	}
	if !ownerProved(ctx, p, dns) {
		return nil, failure{409, "Publish the ownership TXT record, then try again"}
	}
	res, e := s.db.ExecContext(r.Context(), "UPDATE mail_domains SET active=1 WHERE domain=? AND mx=? AND ip=? AND proof=?", p.Domain, p.MX, p.IP, p.Proof)
	if e != nil {
		return nil, e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return nil, e
	}
	if n != 1 {
		return nil, failure{409, "Settings changed. Check again"}
	}
	return s.setupStatus(nil, r)
}
func (s *server) setSender(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct {
		Box     string
		Enabled *bool
	}
	if e := input(r, &q); e != nil {
		return nil, e
	}
	if q.Enabled == nil {
		return nil, failure{400, "Set enabled to true or false"}
	}
	box, e := s.canonicalBox(q.Box)
	if e != nil {
		return nil, e
	}
	_, e = s.db.Exec("INSERT INTO mail_sender_policy VALUES(?,?) ON CONFLICT(box) DO UPDATE SET enabled=excluded.enabled", box, *q.Enabled)
	return object{"ok": e == nil}, e
}
func (s *server) canonicalBox(name string) (string, error) {
	if name == s.operator {
		return name, nil
	}
	var box string
	e := s.store.Reader().QueryRow("SELECT box FROM addresses WHERE address=?", name).Scan(&box)
	return box, e
}
func (s *server) addAlias(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct{ Box, Alias string }
	if e := input(r, &q); e != nil {
		return nil, e
	}
	alias := strings.ToLower(strings.TrimSpace(q.Alias))
	if !nameRE.MatchString(alias) || !validEmail(alias+"@test.example") || alias == s.operator {
		return nil, failure{400, "Use a mailbox name such as sales or customer-support"}
	}
	box, e := s.canonicalBox(q.Box)
	if e != nil {
		return nil, e
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var reserved bool
	e = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM roster WHERE name=?1 AND expires>?2 UNION ALL SELECT 1 FROM enrollments WHERE name=?1 AND expires>?2)", alias, now()).Scan(&reserved)
	if e != nil {
		return nil, e
	}
	if reserved {
		return nil, failure{409, "This name is reserved for onboarding"}
	}
	if _, e = tx.Exec("INSERT INTO addresses(address,box,current) VALUES(?,?,0) ON CONFLICT(address) DO NOTHING", alias, box); e != nil {
		return nil, e
	}
	var owner string
	if e = tx.QueryRow("SELECT box FROM addresses WHERE address=?", alias).Scan(&owner); e != nil {
		return nil, e
	}
	if owner != box {
		return nil, failure{409, "This address already belongs to another inbox"}
	}
	e = tx.Commit()
	return object{"alias": alias, "box": box}, e
}
func (s *server) listMailAddresses(_ http.ResponseWriter, r *http.Request) (any, error) {
	box, e := s.canonicalBox(r.URL.Query().Get("box"))
	if e != nil {
		return nil, e
	}
	rows, e := s.store.Reader().Query("SELECT address FROM addresses WHERE box=? ORDER BY current DESC,address", box)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	names := []string{}
	if box == s.operator {
		names = append(names, box)
	}
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil {
			return nil, e
		}
		names = append(names, n)
	}
	rows.Close()
	if len(names) > 0 && !validEmail(names[0]+"@test.example") {
		var id string
		if e = s.store.Reader().QueryRow("SELECT id FROM mailbox_ids WHERE box=?", box).Scan(&id); e != nil {
			return nil, e
		}
		names[0] = "agent+" + id
	}
	return object{"box": box, "names": names, "may_send": s.maySend(box), "domain": s.primaryDomain()}, rows.Err()
}
