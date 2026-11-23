package server

import (
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed remediation_notes.json
var bundledReviewerNotes string

type reviewerNote struct {
	Title    string   `json:"title"`
	Guidance string   `json:"guidance"`
	Tags     []string `json:"tags"`
	Source   string   `json:"source"`
	Owner    string   `json:"owner"`
	Reviewed string   `json:"reviewed"`
	Status   string   `json:"status"`
}

// Only an operator-configured catalogue can be published. In particular, an
// ordinary findings export cannot be mistaken for a global reviewer library.
func readReviewerNotes(r io.Reader) ([]reviewerNote, error) {
	var catalog struct {
		Audience string         `json:"audience"`
		Notes    []reviewerNote `json:"notes"`
	}
	const maxBytes = 2 << 20
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBytes {
		return nil, errors.New("reviewer catalogue exceeds size limit")
	}
	if err = json.Unmarshal(data, &catalog); err != nil {
		return nil, err
	}
	if catalog.Audience != "all-reviewers" || catalog.Notes == nil {
		return nil, errors.New("missing reviewer publication scope or notes")
	}
	for _, n := range catalog.Notes {
		if n.Status != "current" && n.Status != "retired" {
			return nil, errors.New("invalid note status")
		}
		for _, v := range []string{n.Title, n.Guidance, n.Owner, n.Source} {
			if strings.TrimSpace(v) == "" {
				return nil, errors.New("missing note metadata")
			}
		}
		if _, err = time.Parse(time.DateOnly, n.Reviewed); err != nil {
			return nil, errors.New("invalid review date")
		}
	}
	return catalog.Notes, nil
}

func reviewerNotes(w http.ResponseWriter, r *http.Request) {
	var input io.Reader = strings.NewReader(bundledReviewerNotes)
	if path := os.Getenv("ARTEX_MOSES_MODE_REMEDIATION_NOTES"); path != "" {
		f, err := os.Open(path)
		if err != nil {
			writeErr(w, 500, "reviewer catalogue unavailable")
			return
		}
		defer f.Close()
		input = f
	}
	notes, err := readReviewerNotes(input)
	if err != nil {
		writeErr(w, 500, "invalid reviewer catalogue")
		return
	}
	writeJSON(w, 200, notes)
}
