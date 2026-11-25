package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cyzerx/Moses-x1/db"
)

func TestProposalSignoffAcrossClaimsAndRevisions(t *testing.T) {
	s, finding, request := trafficEvidenceServer(t)
	f, err := s.m.pg.GetFinding(finding.FindingID)
	if err != nil || f.TaskID == nil {
		t.Fatal(err)
	}
	task := s.m.ResolveTask(fmt.Sprint(*f.TaskID))
	base := fmt.Sprintf("/api/tasks/%d/proposal-reviews", *f.TaskID)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("POST", base, strings.NewReader(`{"action":"create","summary":"review local notes"}`)))
	if w.Code != 401 {
		t.Fatalf("unauthenticated create: %d", w.Code)
	}
	w = request("POST", base, `{"action":"create","summary":"review local notes"}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var proposal db.ReviewableProposal
	if err := json.Unmarshal(w.Body.Bytes(), &proposal); err != nil {
		t.Fatal(err)
	}
	endpoint := fmt.Sprintf("%s/%d", base, proposal.ID)
	if token, err := task.Store.ClaimIntentToken(proposal.ID, "fixture"); err != nil || token != "" {
		t.Fatal("pending proposal claimed", token, err)
	}
	if err := task.Store.SetNodeState(proposal.ID, "done"); err == nil {
		t.Fatal("pending proposal settled directly")
	}
	if rows, err := task.Store.Frontier(500); err != nil {
		t.Fatal(err)
	} else {
		for _, n := range rows {
			if n.ID == proposal.ID {
				t.Fatal("pending proposal in execution frontier")
			}
		}
	}
	if w = request("POST", endpoint, `{"action":"approve","revision":1}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"approved_by"`) {
		t.Fatal(w.Code, w.Body)
	}
	if w = request("POST", endpoint, `{"action":"edit","revision":1,"summary":"revised local notes"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w = request("POST", endpoint, `{"action":"approve","revision":1}`); w.Code != 409 {
		t.Fatal("stale approval", w.Code)
	}
	if token, err := task.Store.ClaimIntentToken(proposal.ID, "fixture"); err != nil || token != "" {
		t.Fatal("stale revision claimed", token, err)
	}
	if err := task.Store.SetNodeState(proposal.ID, "paused"); err != nil {
		t.Fatal(err)
	}
	if token, err := task.Store.ClaimPausedIntentToken(proposal.ID); err != nil || token != "" {
		t.Fatal("pending revision resumed", token, err)
	}
	if w = request("POST", endpoint, `{"action":"approve","revision":2}`); w.Code != 200 || !strings.Contains(w.Body.String(), "review local notes") || !strings.Contains(w.Body.String(), "revised local notes") {
		t.Fatal("missing revision history", w.Code, w.Body)
	}
	token, err := task.Store.ClaimPausedIntentToken(proposal.ID)
	if err != nil || token == "" {
		t.Fatal("approved revision could not run", err)
	}
	if _, err = s.m.pg.Exec(`UPDATE exploration_nodes SET payload='{"summary":"different"}' WHERE id=$1`, proposal.ID); err == nil {
		t.Fatal("running approved scope changed")
	}
	if err = task.Store.SettleIntentClaim(proposal.ID, token, "done"); err != nil {
		t.Fatal(err)
	}
}

func TestProposalReviewSurvivesArchiveAndRestore(t *testing.T) {
	s, finding, request := trafficEvidenceServer(t)
	s.ctx = t.Context()
	f, err := s.m.pg.GetFinding(finding.FindingID)
	if err != nil || f.TaskID == nil {
		t.Fatal(err)
	}
	tid := *f.TaskID
	base := fmt.Sprintf("/api/tasks/%d/proposal-reviews", tid)
	var held, approved db.ReviewableProposal
	for _, target := range []*db.ReviewableProposal{&held, &approved} {
		w := request("POST", base, `{"action":"create","summary":"review archive fixture"}`)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body)
		}
		if err = json.Unmarshal(w.Body.Bytes(), target); err != nil {
			t.Fatal(err)
		}
	}
	if w := request("POST", fmt.Sprintf("%s/%d", base, approved.ID), `{"action":"approve","revision":1}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if err = s.m.SetTaskPaused(fmt.Sprint(tid), true); err != nil {
		t.Fatal(err)
	}
	job, err := s.m.pg.QueueTaskArchive(tid)
	if err != nil {
		t.Fatal(err)
	}
	if w := request("POST", fmt.Sprintf("%s/%d", base, held.ID), `{"action":"approve","revision":1}`); w.Code != 409 {
		t.Fatal("archiving accepted an approval", w.Code)
	}
	if err = s.runOneTaskArchiveJob(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.m.pg.QueueTaskArchiveRestore(job.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.runOneTaskArchiveJob(); err != nil {
		t.Fatal(err)
	}
	rows, err := s.m.ResolveTask(fmt.Sprint(tid)).Store.ReviewableProposals(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, p := range rows {
		if p.ID != approved.ID && p.ID != held.ID {
			continue
		}
		seen++
		var r struct {
			Revision   int
			ApprovedBy string `json:"approved_by"`
			Approvals  []struct{ Revision int }
		}
		if err = json.Unmarshal(p.Review, &r); err != nil || r.Revision != 1 {
			t.Fatalf("lost review: %s", p.Review)
		}
		if p.ID == approved.ID && (len(r.Approvals) != 1 || r.ApprovedBy == "") {
			t.Fatal("lost approval", r)
		}
		if p.ID == held.ID && r.ApprovedBy != "" {
			t.Fatal("restored a pending proposal as approved")
		}
	}
	if seen != 2 {
		t.Fatal("missing restored proposals", seen)
	}
}
