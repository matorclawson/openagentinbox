package deploy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPlanDoesNotCreateState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new")
	t.Setenv("INBOX_INSTALL_DIR", root)
	r, e := run([]string{"plan", "--target", "local"})
	if e != nil || r["status"] != "planned" {
		t.Fatal(r, e)
	}
	if _, e := os.Stat(root); !os.IsNotExist(e) {
		t.Fatal("plan changed the filesystem")
	}
}
func TestInvalidDestinations(t *testing.T) {
	for _, args := range [][]string{
		{"apply", "--target", "ssh", "--host", "-F", "--domain", "inbox.example.org"},
		{"plan", "--target", "ssh", "--host", "joe@box", "--domain", "inbox.example.org;exit"},
		{"apply", "--target", "local", "--port", "65536"},
	} {
		if _, e := run(args); e == nil {
			t.Fatal("accepted", args)
		}
	}
}
func TestHealthRejectsGenericAndRedirectedSites(t *testing.T) {
	for _, h := range []http.HandlerFunc{
		func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{}")) },
		func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/other", 302) },
	} {
		srv := httptest.NewServer(h)
		_, e := probe(srv.URL)
		srv.Close()
		if e == nil {
			t.Fatal("accepted unrelated endpoint")
		}
	}
}
