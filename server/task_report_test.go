package server

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Cyzerx/Artex-Moses-mode/db"
)

func TestTaskReportAndOfflineReviewIsolation(t *testing.T) {
	s, first, req := trafficEvidenceServer(t)
	pg := s.m.pg
	row, err := pg.GetFinding(first.FindingID)
	if err != nil || row == nil || row.TaskID == nil {
		t.Fatal("missing fixture task", err)
	}
	firstTask := *row.TaskID
	other, err := pg.CreateTask("other client", "synthetic", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.DeleteTask(other.ID) })
	otherID, err := pg.AddFinding(other.ID, 0, "fixture", "OTHER-CLIENT-MARKER", "low", "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.Exec("DELETE FROM findings WHERE id=$1", otherID) })
	if _, err := pg.Exec(`UPDATE findings SET status='confirmed', verification_status='verified',
		name='<script>PRIVATE-TITLE</script>', summary='PRIVATE-SUMMARY', evidence='PRIVATE-EVIDENCE',
		report='PRIVATE-REPORT', evidence_version=2, report_evidence_version=1 WHERE id=$1`, first.FindingID); err != nil {
		t.Fatal(err)
	}
	legacy, err := pg.AddFinding(firstTask, 0, "fixture", "standalone", "low", "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.Exec("DELETE FROM findings WHERE id=$1", legacy) })
	if _, err := pg.Exec(`INSERT INTO exploration_nodes (exploration_id, kind, state)
		SELECT t.exploration_id, 'finding', 'confirmed' FROM tasks t CROSS JOIN generate_series(1,1001) WHERE t.id=$1`, firstTask); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		name string
		task int64
	}{{"shared", firstTask}, {"shared", other.ID}, {"private", other.ID}} {
		id, err := s.m.Assets().UpsertRootDomain(db.UpsertRootDomainReq{
			Domain: fmt.Sprintf("%s-%d.report.example", fixture.name, firstTask), TaskID: fixture.task,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { pg.Exec("DELETE FROM assets WHERE id=$1", id) })
	}
	w := req("GET", fmt.Sprintf("/api/report?task=%d", firstTask), "")
	if w.Code != 200 {
		t.Fatalf("report status %d: %s", w.Code, w.Body)
	}
	for _, expected := range []string{"Recorded findings: **1003**", "Confirmed disposition: **1**", "Machine-verified findings: **1**", "records: **1001**", "root_domain 1", "details for 1000 graph findings of 1003"} {
		if !strings.Contains(w.Body.String(), expected) {
			t.Errorf("report missing %q", expected)
		}
	}
	task := s.m.ResolveTask(fmt.Sprint(firstTask))
	summary := s.fallbackChat(t.Context(), task, "status")
	for _, expected := range []string{"assets 1", "recorded findings 1003", "confirmed disposition 1", "machine-verified 1"} {
		if !strings.Contains(summary, expected) {
			t.Errorf("rule summary missing %q: %s", expected, summary)
		}
	}

	seedServerEvidenceFlow(t, s, firstTask, "private-source", []byte("PRIVATE-RAW-BODY"))
	w = req("POST", fmt.Sprintf("/api/exploration/findings/%d/traffic", first.FindingID), `{"traffic_refs":[{"traffic_id":"private-source","role":"proof","note":"PRIVATE-BINDING-NOTE"}]}`)
	if w.Code != 200 {
		t.Fatalf("fixture binding: %d %s", w.Code, w.Body)
	}
	base := fmt.Sprintf("/api/exploration/findings/export?scope=selected&format=review-html&task_id=%d&ids=%d", firstTask, first.FindingID)
	w = req("GET", base, "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("review status %d: %s", w.Code, w.Body)
	}
	page := w.Body.String()
	for _, expected := range []string{"confirmed", "verified", "Stale", "Binding #", "proof"} {
		if !strings.Contains(page, expected) {
			t.Errorf("review missing %q", expected)
		}
	}
	for _, forbidden := range []string{"PRIVATE-", "OTHER-CLIENT-MARKER", "private-source", "<script", "src="} {
		if strings.Contains(page, forbidden) {
			t.Errorf("review exposed %q", forbidden)
		}
	}
	w = req("GET", base+"&titles=1", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "&lt;script&gt;PRIVATE-TITLE&lt;/script&gt;") || strings.Contains(w.Body.String(), "<script>PRIVATE") {
		t.Fatal("opted-in title was not rendered as inert text")
	}
	for _, path := range []string{base + "," + fmt.Sprint(otherID), base + "," + fmt.Sprint(first.FindingID), strings.Replace(base, "scope=selected", "scope=all", 1), strings.Replace(base, fmt.Sprintf("task_id=%d", firstTask), "task_id=", 1)} {
		w = req("GET", path, "")
		if w.Code != 400 || strings.Contains(w.Body.String(), "PRIVATE-") || strings.Contains(w.Body.String(), "OTHER-CLIENT-MARKER") {
			t.Fatalf("invalid selection status %d: %s", w.Code, w.Body)
		}
	}
}
