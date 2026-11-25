package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Cyzerx/Moses-x1/db"
)

func TestTaskCredentialContextContainsReferencesOnly(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { d.Close() })
	task, err := d.CreateTaskWithOptions("privacy fixture", "review", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.DeleteTask(task.ID) })
	const domain, username, password, note = "secret.example", "sensitive-alias", "canary-password", "canary-private-note"
	// This is also the upgrade case: the model-facing query must work without
	// decrypting old plaintext or requiring the operator's encryption key.
	_, err = d.Exec(`INSERT INTO task_credentials(task_id,domain,username,password,note)
VALUES ($1,$2,$3,$4,$5)`, task.ID, domain, username, password, note)
	if err != nil {
		t.Fatal(err)
	}
	ts := NewToolSet(d.Exploration(task.ExplorationID), "worker")
	ts.SetTaskID(task.ID)
	list := callGuardTool(t, ts.listTaskCredentials(), "{}")
	saved := callGuardTool(t, ts.saveTaskCredential(), `{"domain":"secret.example","username":"sensitive-alias"}`)
	if list.IsError || saved.IsError {
		t.Fatal(list.Flatten(), saved.Flatten())
	}
	for _, output := range []string{
		list.Flatten(), saved.Flatten(),
		renderWorkerGraphOverview(ts.graphOverviewData(), newDataEnvelope()),
		renderOverviewSections(ts.graphOverviewData(), newDataEnvelope()),
	} {
		if !strings.Contains(output, "has_password") {
			t.Fatal("missing account reference")
		}
		for _, secret := range []string{domain, username, password, note} {
			if strings.Contains(output, secret) {
				t.Fatal("sensitive account field leaked into model output")
			}
		}
	}
	var storedPassword, source string
	err = d.QueryRow(`SELECT password,source FROM task_credentials WHERE task_id=$1`, task.ID).Scan(&storedPassword, &source)
	if err != nil || storedPassword != password || source != "user" {
		t.Fatal("metadata update changed stored credential")
	}
}

func TestLegacyCredentialToolNeverInvitesPassword(t *testing.T) {
	tool := NewToolSet(nil, "").saveTaskCredential()
	legacy := map[string]any{"properties": map[string]any{"password": map[string]any{"type": "string"}}}
	visible := DecorateTool(tool, "legacy prompt requesting a password", legacy)
	raw, _ := json.Marshal(visible.InputSchema())
	if strings.Contains(string(raw), "password") || strings.Contains(visible.Description(), "legacy prompt") {
		t.Fatalf("legacy secret input exposed: %s", raw)
	}
	res := callGuardTool(t, visible, `{"domain":"fixture","username":"u","password":"canary-no-echo"}`)
	if !res.IsError || strings.Contains(res.Flatten(), "canary-no-echo") {
		t.Fatal("former password input must be rejected without echo")
	}
}
