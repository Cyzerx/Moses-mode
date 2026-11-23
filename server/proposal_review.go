package server

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func (s *Server) proposalReviews(w http.ResponseWriter, r *http.Request) {
	task, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if r.Method == http.MethodGet {
		rows, err := task.Store.ReviewableProposals(r.Context())
		if err != nil {
			writeErr(w, 500, "could not read proposals")
			return
		}
		writeJSON(w, 200, rows)
		return
	}
	operator := s.approver(r)
	if operator == "" {
		writeErr(w, 403, "authenticated operator required")
		return
	}
	var body struct {
		Action   string `json:"action"`
		Revision int64  `json:"revision"`
		Summary  string `json:"summary"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 40<<10)).Decode(&body); err != nil {
		writeErr(w, 400, "invalid review")
		return
	}
	if !s.engine.beginTaskOperation(task.ID) {
		writeErr(w, 409, "task unavailable")
		return
	}
	defer s.engine.decInflight(task.ID)
	if r.PathValue("iid") == "" {
		if body.Action != "create" {
			writeErr(w, 400, "expected create")
			return
		}
		p, err := task.Store.CreateReviewedProposal(r.Context(), body.Summary, operator)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 201, p)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("iid"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "invalid proposal")
		return
	}
	p, err := task.Store.ReviewProposal(r.Context(), id, body.Revision, body.Action, operator, body.Summary)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, p)
}
