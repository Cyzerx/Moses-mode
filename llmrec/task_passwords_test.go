package llmrec

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

type accountHistoryCapture struct{ req llm.CompletionRequest }

func (c *accountHistoryCapture) Complete(_ context.Context, r llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	c.req = r
	return llm.Message{}, "", llm.Usage{}, nil
}
func (c *accountHistoryCapture) Stream(_ context.Context, r llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	c.req = r
	return func(yield func(llm.StreamEvent, error) bool) {}
}

func TestLegacyTaskPasswordNeverReplayedToProvider(t *testing.T) {
	const canary = "rotated-password-canary"
	old := llm.CompletionRequest{Messages: []llm.Message{
		llm.UserText(`old overview {"task_credentials":[{"password":"rotated-password-canary"}]}`),
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: "t1", Name: "save_task_credential", Input: json.RawMessage(`{"password":"rotated-password-canary"}`)}}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.BlockToolResult, ToolUseID: "t1", Content: []llm.ContentBlock{llm.TextBlock(`{"credentials":[{"password":"rotated-password-canary"}]}`)}}}},
	}}
	capture := &accountHistoryCapture{}
	rec := Wrap(capture, nil, "", "", "", "", func() bool { return false })
	for _, stream := range []bool{false, true} {
		if stream {
			for _, err := range rec.Stream(context.Background(), old) {
				if err != nil {
					t.Fatal(err)
				}
			}
		} else if _, _, _, err := rec.Complete(context.Background(), old); err != nil {
			t.Fatal(err)
		}
		wire, _ := json.Marshal(capture.req)
		if strings.Contains(string(wire), canary) {
			t.Fatal("provider saw replayed password")
		}
	}
	source, _ := json.Marshal(old)
	if !strings.Contains(string(source), canary) {
		t.Fatal("stored source was mutated")
	}
}

func TestUnstructuredStoredPasswordRemovedOrBlocked(t *testing.T) {
	const password = "canary<&123"
	req := llm.CompletionRequest{System: []string{"private note " + password}, Messages: []llm.Message{llm.UserText("old chat " + password)}}
	safe, err := redactAccountHistory(req, []string{password})
	if err != nil || strings.Contains(safe.System[0], password) || strings.Contains(safe.Messages[0].Content[0].Text, password) {
		t.Fatal("unsafe prose")
	}
	req.Messages[0].Content = []llm.ContentBlock{{Type: llm.BlockToolUse, Name: "old_tool", Input: json.RawMessage(`{"first":[],"nested":{"after":"canary\u003c\u0026123"}}`)}}
	if _, err := redactAccountHistory(req, []string{password}); err == nil {
		t.Fatal("unsafe nested argument permitted")
	}
}
