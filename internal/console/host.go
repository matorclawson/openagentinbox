package console

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func hostname(raw string) string {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if len(name) > 253 {
		return ""
	}
	for _, part := range strings.Split(name, ".") {
		if len(part) < 1 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return ""
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return ""
			}
		}
	}
	return name
}
func literalIP(s string) (netip.Addr, bool) {
	a, e := netip.ParseAddr(s)
	if e != nil || a.Zone() != "" || a.IsUnspecified() || a.IsMulticast() {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}
func (s *server) peerIP(r *http.Request) (string, error) {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		return "", failure{403, "Unknown connection peer"}
	}
	peer, ok := literalIP(host)
	if !ok {
		return "", failure{403, "Invalid connection peer"}
	}
	if peer.String() == s.trustedProxy {
		vals := r.Header.Values("X-Inbox-Peer-IP")
		if len(vals) != 1 {
			return "", failure{403, "Proxy did not provide a single client address"}
		}
		peer, ok = literalIP(vals[0])
		if !ok {
			return "", failure{403, "Invalid proxy client address"}
		}
	}
	return peer.String(), nil
}
