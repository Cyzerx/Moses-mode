package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Cyzerx/Artex-Moses-mode/db"
	"github.com/Cyzerx/Artex-Moses-mode/traffic"
)

// Ingest caps: a browser tab can produce a burst of entries, but the ingest
// path is meant for deliberate operator captures, not bulk sync. Bodies are
// truncated server-side as a second line of defense behind the extension's
// own caps.
const (
	maxIngestBatch    = 50
	maxIngestBodySize = 256 << 10 // 256 KiB per body
)

// ingestTraffic accepts exchanges captured outside the MITM proxy (currently
// the browser extension) into the global traffic store, so operator-driven
// authenticated browsing becomes evidence the same way proxy traffic is.
func (s *Server) ingestTraffic(w http.ResponseWriter, r *http.Request) {
	tr := s.m.Traffic()
	if tr == nil {
		writeErr(w, 503, "traffic store is not available")
		return
	}
	var req struct {
		TaskID    int64                      `json:"task_id"`
		Exchanges []traffic.ExternalExchange `json:"exchanges"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	if len(req.Exchanges) == 0 {
		writeErr(w, 400, "empty exchanges")
		return
	}
	if len(req.Exchanges) > maxIngestBatch {
		writeErr(w, 400, "too many exchanges in one batch")
		return
	}

	ingested, failed := 0, 0
	record := func() {
		for _, e := range req.Exchanges {
			e.RequestBody = truncateBody(e.RequestBody)
			e.ResponseBody = truncateBody(e.ResponseBody)
			if err := tr.RecordExternalForTask(e, req.TaskID); err != nil {
				failed++
				continue
			}
			ingested++
		}
	}
	if req.TaskID == 0 {
		record() // older clients create operator-only captures
	} else {
		if req.TaskID < 0 || s.m.pg == nil {
			writeErr(w, 400, "invalid engagement")
			return
		}
		// Serialize capture with archive and deletion so an exchange cannot
		// arrive between the task snapshot and its subsequent compaction.
		err := s.m.pg.WithEvidenceTx(r.Context(), func(tx *sql.Tx) error {
			if err := db.LockTaskEvidenceTx(tx, req.TaskID); err != nil {
				return err
			}
			record()
			return nil
		})
		if err != nil {
			writeErr(w, 409, "engagement unavailable for capture")
			return
		}
	}
	writeJSON(w, 200, map[string]any{"ingested": ingested, "failed": failed})
}

func truncateBody(s string) string {
	if len(s) <= maxIngestBodySize {
		return s
	}
	return s[:maxIngestBodySize] + "\n... [truncated by ingest]"
}

// trafficPickerTask scopes the finding editor even when opened from the global
// findings page. The authenticated operator's standalone Traffic page omits this filter.
func (s *Server) trafficPickerTask(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if !r.URL.Query().Has("finding_id") {
		return 0, true
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("finding_id"), 10, 64)
	if err == nil && id > 0 && s.m.pg != nil {
		f, err := s.m.pg.GetFinding(id)
		if err == nil && f != nil && f.TaskID != nil && *f.TaskID > 0 {
			return *f.TaskID, true
		}
	}
	writeErr(w, 404, "engagement unavailable for this finding")
	return 0, false
}

// assignTraffic accepts authenticated operator decisions only. An empty task
// previews immutable exchange IDs. The grant is never inferred from a host.
func (s *Server) assignTraffic(w http.ResponseWriter, r *http.Request) {
	actor := s.approver(r)
	if actor == "" {
		writeErr(w, 403, "authenticated operator required")
		return
	}
	tr := s.m.Traffic()
	if tr == nil {
		writeErr(w, 503, "traffic store unavailable")
		return
	}
	var body struct {
		IDs    []string `json:"ids"`
		TaskID int64    `json:"task_id,string"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&body); err != nil || body.TaskID < 0 {
		writeErr(w, 400, "invalid selection")
		return
	}
	var out []traffic.CaptureAssignment
	var err error
	if body.TaskID > 0 {
		err = s.m.pg.WithEvidenceTx(r.Context(), func(tx *sql.Tx) error {
			if err := db.LockTaskEvidenceTx(tx, body.TaskID); err != nil {
				return err
			}
			var assignErr error
			out, assignErr = tr.ReviewAssignments(r.Context(), body.IDs, body.TaskID, actor)
			return assignErr
		})
	} else {
		out, err = tr.ReviewAssignments(r.Context(), body.IDs, 0, actor)
	}
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, out)
}
