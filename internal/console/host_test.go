package console

import (
	"net/http/httptest"
	"testing"
)

func TestPeerIP(t *testing.T) {
	for _, v := range []struct {
		trust  string
		header []string
		want   string
	}{
		{"", []string{"192.0.2.9"}, "127.0.0.1"},
		{"127.0.0.1", []string{"::ffff:192.0.2.9"}, "192.0.2.9"},
		{"127.0.0.1", []string{"2001:db8::1"}, "2001:db8::1"},
		{"127.0.0.1", nil, ""}, {"127.0.0.1", []string{"10.0.0.1, 10.0.0.2"}, ""}, {"127.0.0.1", []string{"192.0.2.1", "192.0.2.2"}, ""},
	} {
		s := &server{trustedProxy: v.trust}
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "127.0.0.1:54321"
		r.Header["X-Inbox-Peer-Ip"] = v.header
		got, e := s.peerIP(r)
		if got != v.want || (e == nil) != (v.want != "") {
			t.Fatalf("%+v: %s %v", v, got, e)
		}
	}
	if hostname("Mac-Mini.local.") != "mac-mini.local" || hostname("bad..example") != "" {
		t.Fatal("host normalization")
	}
}
