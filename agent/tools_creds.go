package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Cyzerx/Moses-x1/db"
)

func (t *ToolSet) credentialTaskID() (int64, error) {
	if t.ts == nil {
		return 0, errors.New("task account tools require a task store")
	}
	if t.taskID > 0 {
		return t.taskID, nil
	}
	id, err := t.ts.TaskID()
	if err != nil || id <= 0 {
		return 0, errors.New("task account tools are only available inside a task")
	}
	return id, nil
}

func (t *ToolSet) saveTaskCredential() actool.CoreTool {
	return writeTool(
		"save_task_credential",
		"Record the domain and username of a test account in this task. Supply account metadata only. An operator enters passwords in the task's Test accounts tab. Returns an account reference.",
		obj(map[string]any{
			"domain":   str("domain to which the account belongs"),
			"username": str("login name"),
		}, "domain", "username"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Domain   string `json:"domain"`
				Username string `json:"username"`
			}
			dec := json.NewDecoder(bytes.NewReader(in))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&a); err != nil {
				return actool.Errorf("supply only domain and username"), nil
			}
			taskID, err := t.credentialTaskID()
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			ref, err := t.ts.RecordTaskAccount(taskID, a.Domain, a.Username)
			if err != nil {
				return actool.Errorf("could not record task account metadata"), nil
			}
			return jsonResult(ref)
		})
}

func (t *ToolSet) listTaskCredentials() actool.CoreTool {
	return writeTool(
		"list_task_credentials",
		"List account references for the current task. Each entry contains an ID and whether the operator has stored a password.",
		obj(map[string]any{}),
		func(_ context.Context, _ json.RawMessage) (actool.Result, error) {
			taskID, err := t.credentialTaskID()
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			list, err := t.ts.TaskCredentialRefs(taskID)
			if err != nil {
				return actool.Errorf("could not list task account references"), nil
			}
			return jsonResult(map[string]any{"credentials": list})
		})
}

func (t *ToolSet) taskCredentialOverview() []db.TaskCredentialRef {
	if t.ts == nil || t.taskID <= 0 {
		return nil
	}
	list, _ := t.ts.TaskCredentialRefs(t.taskID)
	return list
}
