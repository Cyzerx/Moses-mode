package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cyzerx/Moses-x1/db"
	"github.com/Cyzerx/Moses-x1/evidence"
)

func TestTaskIntegrityReadOnlyAndDatabaseOnly(t *testing.T) {
	s, first, req := trafficEvidenceServer(t)
	pg := s.m.pg
	f, err := pg.GetFinding(first.FindingID)
	if err != nil || f == nil || f.TaskID == nil {
		t.Fatal("missing fixture task", err)
	}
	taskID := *f.TaskID
	task := s.m.ResolveTask(fmt.Sprint(taskID))
	body := []byte("PRIVATE-BODY-CANARY")
	sourceID := fmt.Sprintf("PRIVATE-TRAFFIC-CANARY-%d", taskID)
	seedServerEvidenceFlow(t, s, taskID, sourceID, body)
	w := req("POST", fmt.Sprintf("/api/exploration/findings/%d/traffic", f.ID),
		fmt.Sprintf(`{"traffic_refs":[{"traffic_id":%q,"role":"proof","note":"PRIVATE-NOTE-CANARY"}]}`, sourceID))
	if w.Code != 200 {
		t.Fatalf("bind: %d %s", w.Code, w.Body)
	}
	url := fmt.Sprintf("/api/tasks/%d/integrity", taskID)
	check := func(mode string) evidence.IntegrityReport {
		t.Helper()
		w := req("GET", url+"?mode="+mode, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "PRIVATE-") {
			t.Fatalf("integrity response %d: %s", w.Code, w.Body)
		}
		var r evidence.IntegrityReport
		if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := check("full"); r.Problems != 0 || r.Bindings != 1 || r.BodiesChecked != 2 || r.BodiesSkipped != 0 {
		t.Fatalf("healthy restore: %+v", r)
	}
	other, err := pg.CreateTask("PRIVATE-OTHER-TASK", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.DeleteTask(other.ID) })
	source, err := pg.Exploration(other.ExplorationID).AddNode(db.KindFact, nil, 0, "confirmed", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	goal, err := task.Store.AddNode(db.KindGoal, nil, 0, "open", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = task.Store.Link(source, db.RelProves, goal); err != nil {
		t.Fatal(err)
	}
	if _, err = pg.Exec("UPDATE findings SET report='PRIVATE-OLD-REPORT',report_evidence_version=0 WHERE id=$1", f.ID); err != nil {
		t.Fatal(err)
	}
	r := check("full")
	if r.Problems != 1 || r.StaleReports != 1 || r.Issues[0].Code != "edge_outside_engagement" {
		t.Fatalf("cross-task edge or stale status: %+v", r)
	}
	if _, err = pg.Exec("INSERT INTO task_relations(task_id,source_task_id) VALUES($1,$2)", taskID, other.ID); err != nil {
		t.Fatal(err)
	}
	if r := check("full"); r.Problems != 0 || r.StaleReports != 1 {
		t.Fatalf("legal source edge: %+v", r)
	}
	if err = task.Store.Link(first.NodeID, db.RelProves, source); err != nil {
		t.Fatal(err)
	}
	if r := check("database-only"); r.Problems != 1 || r.Issues[0].Code != "invalid_proof_edge" {
		t.Fatalf("wrong node kind: %+v", r)
	}
	if _, err = pg.Exec(`DELETE FROM exploration_edges WHERE exploration_id=$1 AND src_id=$2 AND dst_id=$3`, task.ExpID, first.NodeID, source); err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(body))
	path := filepath.Join(s.evidenceStore().Dir, "blobs", hash[:2], hash+".bin")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", len(body))), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := check("full"); r.Problems != 1 || r.Issues[len(r.Issues)-1].Code != "body_mismatch" {
		t.Fatalf("same-length corrupt body: %+v", r)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if r := check("full"); r.Problems != 1 || r.Issues[len(r.Issues)-1].Code != "missing_body" {
		t.Fatalf("missing body: %+v", r)
	}
	if r := check("database-only"); r.Problems != 0 || r.BodiesChecked != 0 || r.BodiesSkipped != 2 || r.StaleReports != 1 {
		t.Fatalf("DB-only restore: %+v", r)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("integrity check recreated the missing file")
	}
	if _, err := pg.Exec(`UPDATE traffic_evidence_snapshots SET method='HEAD'
		WHERE id IN (SELECT snapshot_id FROM finding_traffic_bindings WHERE finding_id=$1)`, f.ID); err != nil {
		t.Fatal(err)
	}
	if r := check("database-only"); r.Problems != 1 || r.Issues[len(r.Issues)-1].Code != "snapshot_mismatch" {
		t.Fatalf("modified snapshot metadata: %+v", r)
	}
	if r := req("GET", url+"?mode=invalid", ""); r.Code != 400 {
		t.Fatalf("invalid mode %d", r.Code)
	}
}
