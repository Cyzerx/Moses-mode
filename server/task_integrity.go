package server

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
)

func (s *Server) taskIntegrity(w http.ResponseWriter, r *http.Request) {
	taskID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	mode := r.URL.Query().Get("mode")
	if err != nil || taskID <= 0 || (mode != "" && mode != "full" && mode != "database-only") {
		writeErr(w, 400, "choose a task and a valid integrity check mode")
		return
	}
	result, err := s.evidenceStore().VerifyTask(r.Context(), taskID, mode == "database-only")
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, 404, "task unavailable")
		return
	}
	if err != nil {
		writeErr(w, 500, "could not finish the integrity check")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}
