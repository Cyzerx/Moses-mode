package traffic

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEngagementTrafficIsolation(t *testing.T) {
	tr, _ := openTraffic(t)
	const host = "shared.example"
	ids := map[int64]string{}
	hashes := map[int64]string{}
	for _, owner := range []int64{10, 20, 0} {
		marker := fmt.Sprintf("owner-%d-only", owner)
		body := strings.Repeat(marker, maxInlineBody/len(marker)+1)
		if err := tr.RecordExternalForTask(ExternalExchange{Method: "GET", URL: "https://" + host + "/" + marker, ResponseBody: body}, owner); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := tr.DB().QueryRow("SELECT id FROM exchanges WHERE url=?", "https://"+host+"/"+marker).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[owner] = id
		hashes[owner] = fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	}
	call := func(owner int64, name string, input map[string]any, wantErr bool) string {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range tr.TaskTools(func(context.Context) int64 { return owner }) {
			if tool.Name() != name {
				continue
			}
			result, err := tool.Call(t.Context(), raw, nil)
			if err != nil || result.IsError != wantErr {
				t.Fatalf("%s error %v: %s", name, err, result.Flatten())
			}
			return result.Flatten()
		}
		t.Fatalf("missing %s", name)
		return ""
	}
	if got := call(10, "traffic_search", map[string]any{"host": host}, false); !strings.Contains(got, ids[10]) || strings.Contains(got, ids[20]) || strings.Contains(got, ids[0]) {
		t.Fatal(got)
	}
	if got := call(10, "traffic_blob", map[string]any{"hash": hashes[10]}, false); !strings.Contains(got, "owner-10-only") {
		t.Fatal("missing owned bytes")
	}
	for _, owner := range []int64{0, 20} {
		call(10, "traffic_get", map[string]any{"id": ids[owner]}, true)
		call(10, "traffic_messages", map[string]any{"id": ids[owner]}, true)
		call(10, "traffic_blob", map[string]any{"hash": hashes[owner]}, true)
		if err := tr.ReadEvidence(t.Context(), 10, []string{ids[owner]}, func(EvidenceExchange) error { t.Fatal("foreign body callback"); return nil }); err == nil {
			t.Fatal("foreign binding accepted")
		}
	}
	call(0, "traffic_search", map[string]any{"host": host}, true)
	out := t.TempDir()
	if n, err := tr.ExportTask(10, out); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "traffic.json"))
	if err != nil || strings.Contains(string(raw), "owner-20-only") || strings.Contains(string(raw), "owner-0-only") {
		t.Fatal("mixed archive", err)
	}
	stage, err := tr.StageTaskTraffic(10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = stage.Commit(); err != nil {
		t.Fatal(err)
	}
	call(20, "traffic_get", map[string]any{"id": ids[20]}, false)
	if _, _, err := tr.Get(ids[0]); err != nil {
		t.Fatal("legacy/operator record removed", err)
	}
	if _, err := tr.ImportTaskArchive(out, 20); err == nil {
		t.Fatal("foreign archive accepted")
	}
	if n, err := tr.ImportTaskArchive(out, 10); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	call(10, "traffic_get", map[string]any{"id": ids[10]}, false)
}

func TestOperatorAssignmentsAreAtomicAndAudited(t *testing.T) {
	tr, _ := openTraffic(t)
	ids := map[string]string{}
	for _, name := range []string{"a", "b", "c"} {
		if err := tr.RecordExternal(ExternalExchange{Method: "GET", URL: "https://same.example/" + name, ResponseBody: name}); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := tr.DB().QueryRow(`SELECT id FROM exchanges WHERE url=?`, "https://same.example/"+name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	preview, err := tr.ReviewAssignments(t.Context(), []string{ids["a"]}, 0, "operator")
	if err != nil || len(preview) != 1 || preview[0].TaskID != 0 {
		t.Fatal(preview, err)
	}
	got, err := tr.ReviewAssignments(t.Context(), []string{ids["a"]}, 4, "reviewer")
	if err != nil || got[0].AssignedAt == 0 || got[0].AssignedBy != "reviewer" {
		t.Fatal(got, err)
	}
	retry, err := tr.ReviewAssignments(t.Context(), []string{ids["a"]}, 4, "second")
	if err != nil || retry[0] != got[0] {
		t.Fatal("retry altered receipt", retry, err)
	}
	if _, err = tr.ReviewAssignments(t.Context(), []string{ids["b"], ids["a"]}, 5, "second"); err == nil {
		t.Fatal("foreign reassignment accepted")
	}
	untouched, err := tr.ReviewAssignments(t.Context(), []string{ids["b"], ids["c"]}, 0, "operator")
	if err != nil || untouched[0].TaskID != 0 || untouched[1].TaskID != 0 {
		t.Fatal("partial or host-wide assignment", untouched, err)
	}
	if _, err = tr.ReviewAssignments(t.Context(), []string{ids["b"]}, 5, "second"); err != nil {
		t.Fatal(err)
	}
	rows, err := tr.queryForTask(4, "same.example", "", "", 0, 10)
	if err != nil || len(rows) != 1 || rows[0].ID != ids["a"] {
		t.Fatal(rows, err)
	}
	if _, err = tr.DB().Exec(`INSERT INTO stream_messages(exchange_id,seq,ts,direction,kind,size,payload) VALUES(?,1,42,'s2c',1,5,?)`, ids["a"], []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err = tr.DB().Exec(`INSERT INTO exchange_rewrites(exchange_id,seq,phase,rule_id,outcome) VALUES(?,0,'request',82,'applied')`, ids["a"]); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err = tr.ExportTask(4, dir); err != nil {
		t.Fatal(err)
	}
	stage, err := tr.StageTaskTraffic(4, 42)
	if err != nil {
		t.Fatal(err)
	}
	if err = stage.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = tr.ImportTaskArchive(dir, 4); err != nil {
		t.Fatal(err)
	}
	msgs, n, err := tr.StreamMessages(ids["a"], 0, 10)
	if err != nil || n != 1 || msgs[0].Payload != "hello" {
		t.Fatal("lost stream message", msgs, err)
	}
	changed, rules, err := tr.RewriteInfo(ids["a"])
	if err != nil || !changed || len(rules) != 1 || rules[0] != 82 {
		t.Fatal("lost rewrite trace", rules, err)
	}
	after, err := tr.ReviewAssignments(t.Context(), []string{ids["a"]}, 0, "operator")
	if err != nil || after[0] != got[0] {
		t.Fatal("archive lost receipt", after, err)
	}
}

func TestAssignedLegacyFilesStayIsolatedThroughArchive(t *testing.T) {
	tr, dir := openTraffic(t)
	for _, id := range []string{"chosen", "untouched"} {
		rel := filepath.Join("shared.example", "GET", id)
		base := filepath.Join(dir, rel)
		if err := os.MkdirAll(base, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "request.http"), []byte("GET / HTTP/1.1\n\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "response.http"), []byte("HTTP 200\n\n"+id), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
			VALUES(?,1,'shared.example','GET','/','https://shared.example/',200,'text/plain',0,?,?)`, id, len(id), rel); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tr.ReviewAssignments(t.Context(), []string{"chosen"}, 6, "operator"); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if n, err := tr.ExportTask(6, out); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	stage, err := tr.StageTaskTraffic(6, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err = stage.Commit(); err != nil {
		t.Fatal(err)
	}
	check := func(id string) string { return filepath.Join(dir, "shared.example", "GET", id, "response.http") }
	if _, err = os.Stat(check("untouched")); err != nil {
		t.Fatal("removed unrelated file", err)
	}
	if _, err = os.Stat(check("chosen")); !os.IsNotExist(err) {
		t.Fatal("legacy body remained on disk", err)
	}
	if n, err := tr.ImportTaskArchive(out, 6); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := tr.ReadEvidence(t.Context(), 6, []string{"chosen"}, func(e EvidenceExchange) error {
		got, err := io.ReadAll(e.Response)
		if string(got) != "chosen" {
			t.Fatal("lost legacy bytes", string(got))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
