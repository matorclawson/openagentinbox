package deliverystore

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// The operations table retains these fingerprints across daemon upgrades.
// Consume the same vectors in each implementation.
func TestProductSubmissionDigestVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/submission-digests.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name    string
		Message Message
		SHA256  string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("empty fixture")
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			if got := hex.EncodeToString(digest(tc.Message)); got != tc.SHA256 {
				t.Fatalf("submission fingerprint %s, want %s", got, tc.SHA256)
			}
		})
	}
}
