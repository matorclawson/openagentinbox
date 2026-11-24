package console

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func request(r *http.Request, target string, in, out any) error {
	method := "GET"
	var body io.Reader
	if in != nil {
		v, e := json.Marshal(in)
		if e != nil {
			return e
		}
		method = "POST"
		body = bytes.NewReader(v)
	}
	req, e := http.NewRequestWithContext(r.Context(), method, target, body)
	if e != nil {
		return e
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, e := client.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return failure{502, "Upstream request declined"}
	}
	return json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(out)
}
func (s *server) active(r *http.Request, d string) (bool, error) {
	if s.broker == "" {
		return false, nil
	}
	var v struct{ Grant, Membership, Registration string }
	e := request(r, s.broker+"/delegate/status", object{"application_id": s.app, "delegation": d}, &v)
	return v.Grant == "active" && v.Membership == "active" && v.Registration == "live", e
}
func (s *server) connect(w http.ResponseWriter, r *http.Request) (any, error) {
	if s.broker == "" {
		return nil, failure{409, "OpenPassword is not configured"}
	}
	var q struct{ Code string }
	if e := input(r, &q); e != nil {
		return nil, e
	}
	var p sql.NullString
	e := s.store.Reader().QueryRow("SELECT principal FROM owner").Scan(&p)
	intent := "login"
	if errors.Is(e, sql.ErrNoRows) {
		if !s.setupCode(q.Code) {
			return nil, failure{403, "Invalid setup code"}
		}
		intent = "setup"
	} else if e != nil {
		return nil, e
	} else if !p.Valid {
		ok, e := s.signed(r)
		if e != nil {
			return nil, e
		}
		if ok {
			intent = "link"
		}
	}
	state, browser, verifier := token(), token(), token()
	if _, e = s.db.Exec("DELETE FROM attempts WHERE expires<=?", now()); e != nil {
		return nil, e
	}
	if _, e = s.db.Exec("INSERT INTO attempts VALUES(?,?,?,?,?)", state, hash(browser), verifier, now()+300000, intent); e != nil {
		return nil, e
	}
	s.set(w, "oai_pair", browser, 300)
	digest := sha256.Sum256([]byte(verifier))
	params := url.Values{"application_id": {s.app}, "redirect_uri": {s.origin + "/api/callback"}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"}, "classes": {""}}
	return object{"url": s.broker + "/delegate?" + params.Encode()}, nil
}
func (s *server) identity(r *http.Request, token string) (string, int64, error) {
	bad := failure{401, "Invalid delegated identity"}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", 0, bad
	}
	dec := base64.RawURLEncoding.DecodeString
	h, e := dec(parts[0])
	if e != nil {
		return "", 0, bad
	}
	var head struct{ Alg, Typ, Kid string }
	if json.Unmarshal(h, &head) != nil || head.Alg != "EdDSA" || head.Typ != s.delegationType {
		return "", 0, bad
	}
	var jwks struct {
		Keys []struct{ Kty, Crv, Kid, X, Alg, Use string }
	}
	if e = request(r, s.broker+"/.well-known/openpassword-jwks.json", nil, &jwks); e != nil {
		return "", 0, e
	}
	sig, e := dec(parts[2])
	if e != nil {
		return "", 0, bad
	}
	valid := false
	for _, j := range jwks.Keys {
		if j.Kty != "OKP" || j.Crv != "Ed25519" || head.Kid != "" && j.Kid != head.Kid || j.Alg != "" && j.Alg != "EdDSA" || j.Use != "" && j.Use != "sig" {
			continue
		}
		key, e := dec(j.X)
		if e == nil && len(key) == ed25519.PublicKeySize && ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), sig) {
			valid = true
			break
		}
	}
	if !valid {
		return "", 0, bad
	}
	b, e := dec(parts[1])
	if e != nil {
		return "", 0, bad
	}
	var c struct {
		Iss, Hiss, Sub, Org, App string
		Aud                      json.RawMessage
		Exp, Nbf                 int64
		Classes                  []string
	}
	if json.Unmarshal(b, &c) != nil {
		return "", 0, bad
	}
	var audience string
	var audiences []string
	json.Unmarshal(c.Aud, &audience)
	json.Unmarshal(c.Aud, &audiences)
	for _, v := range audiences {
		if v == s.audience {
			audience = v
		}
	}
	if c.Iss != s.broker || c.Hiss != s.broker+"/auth/v1" || c.App != s.app || audience != s.audience || c.Sub == "" || c.Org == "" || c.Classes == nil || len(c.Classes) != 0 || c.Exp <= time.Now().Unix() || c.Nbf <= 0 || c.Nbf > time.Now().Unix() {
		return "", 0, bad
	}
	principal, _ := json.Marshal([]string{c.Iss, c.Org, c.Sub})
	return string(principal), c.Exp * 1000, nil
}
func (s *server) callback(w http.ResponseWriter, r *http.Request) (any, error) {
	if s.broker == "" {
		return nil, failure{404, "OpenPassword is not configured"}
	}
	var verifier, intent string
	q := r.URL.Query()
	e := s.db.QueryRow("DELETE FROM attempts WHERE state=? AND browser=? AND expires>? RETURNING verifier,intent", q.Get("state"), hash(cookie(r, "oai_pair")), now()).Scan(&verifier, &intent)
	if e != nil {
		return nil, failure{401, "Expired sign-in attempt"}
	}
	var grant struct{ Delegation string }
	if e = request(r, s.broker+"/delegate/exchange", object{"grant_type": "authorization_code", "application_id": s.app, "redirect_uri": s.origin + "/api/callback", "code": q.Get("code"), "code_verifier": verifier}, &grant); e != nil {
		return nil, e
	}
	p, exp, e := s.identity(r, grant.Delegation)
	if e != nil {
		return nil, e
	}
	if intent == "setup" {
		_, e = s.db.Exec("INSERT OR IGNORE INTO owner VALUES(1,'Workspace owner',NULL,NULL,?)", p)
	} else if intent == "link" {
		ok, err := s.signed(r)
		if err != nil {
			return nil, err
		}
		if ok {
			_, e = s.db.Exec("UPDATE owner SET principal=? WHERE id=1 AND principal IS NULL", p)
		}
	}
	if e != nil {
		return nil, e
	}
	var matches int
	if e = s.store.Reader().QueryRow("SELECT count(*) FROM owner WHERE principal=?", p).Scan(&matches); e != nil {
		return nil, e
	}
	if matches != 1 {
		return nil, failure{403, "This identity does not own this inbox"}
	}
	if e = s.issue(w, exp, grant.Delegation); e != nil {
		return nil, e
	}
	http.Redirect(w, r, "/", 303)
	return nil, nil
}
