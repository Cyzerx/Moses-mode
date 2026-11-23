package server

import (
	"bytes"
	"net/http"
	"strconv"

	"github.com/Cyzerx/Artex-Moses-mode/report"
)

func (s *Server) offlineFindingReview(w http.ResponseWriter, r *http.Request, ids []int64) {
	taskID, err := strconv.ParseInt(r.URL.Query().Get("task_id"), 10, 64)
	if err != nil || taskID < 1 || r.URL.Query().Get("scope") != "selected" || len(ids) == 0 || len(ids) > 100 {
		writeErr(w, 400, "choose one task and 1–100 selected findings")
		return
	}
	findings, err := s.m.pg.SelectedReviewFindings(r.Context(), taskID, ids, r.URL.Query().Get("titles") == "1")
	if err != nil {
		writeErr(w, 500, "could not read the selected evidence")
		return
	}
	if len(findings) != len(ids) {
		writeErr(w, 400, "all selected findings must be distinct and belong to the selected task")
		return
	}
	var doc bytes.Buffer
	if err := report.WriteOfflineReview(&doc, taskID, findings); err != nil {
		writeErr(w, 500, "could not render the evidence view")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `attachment; filename="selected-finding-evidence.html"`)
	w.Write(doc.Bytes())
}
