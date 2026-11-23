package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewerNotesOnlyServePublishedCatalogue(t *testing.T) {
	f := filepath.Join(t.TempDir(), "review.json")
	t.Setenv("ARTEX_MOSES_MODE_REMEDIATION_NOTES", f)
	get := func() (int, string) {
		w := httptest.NewRecorder()
		reviewerNotes(w, httptest.NewRequest("GET", "/api/remediation-notes", nil))
		return w.Code, w.Body.String()
	}
	for _, bad := range []string{`{"findings":[{"summary":"private-marker"}]}`, strings.Replace(bundledReviewerNotes, "all-reviewers", "private-marker", 1), strings.Replace(bundledReviewerNotes, "2026-11-18", "private-marker", 1)} {
		if err := os.WriteFile(f, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if status, body := get(); status != 500 || strings.Contains(body, "private-marker") {
			t.Fatalf("private or invalid data served: %d", status)
		}
	}
	if err := os.WriteFile(f, []byte(bundledReviewerNotes), 0600); err != nil {
		t.Fatal(err)
	}
	if status, body := get(); status != 200 || !strings.Contains(body, "Bind a saved secret") {
		t.Fatalf("missing published catalogue: %d", status)
	}
}
