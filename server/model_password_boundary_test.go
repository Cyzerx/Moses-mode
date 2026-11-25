package server

import (
	"context"
	"encoding/json"
	"iter"
	"strconv"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Cyzerx/Moses-x1/db"
	"github.com/Cyzerx/Moses-x1/llmrec"
)

type accountPrivacyTransport struct {
	bodies []string
}

func (p *accountPrivacyTransport) Complete(_ context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	b, err := json.Marshal(req)
	p.bodies = append(p.bodies, string(b))
	return llm.Message{}, "", llm.Usage{}, err
}
func (p *accountPrivacyTransport) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		_, _, _, err := p.Complete(ctx, req)
		if err != nil {
			yield(llm.StreamEvent{}, err)
		}
	}
}

func TestModelPasswordBoundaryIncludesAllTriggerSources(t *testing.T) {
	s, _, _ := trafficEvidenceServer(t)
	var ids []int64
	secrets := []string{"alpha-private-pass-81", "beta-private-pass-92"}
	for _, secret := range secrets {
		task, err := s.m.CreateTaskWithOptions("privacy test", "local review", db.TaskCreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		id, _ := strconv.ParseInt(task.ID, 10, 64)
		ids = append(ids, id)
		t.Cleanup(func() { s.m.pg.Exec("DELETE FROM tasks WHERE id=$1", id) })
		if _, err := s.m.pg.UpsertTaskCredential(id, db.TaskCredentialInput{Domain: "example.invalid", Username: "alias", Password: secret}); err != nil {
			t.Fatal(err)
		}
	}
	conv, err := s.m.pg.CreateConversationGated("worker", "private sources", nil, false, ids...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.m.pg.Exec("DELETE FROM conversations WHERE id=$1", conv.ID) })
	ctx, stop, err := s.conversationAuthorization(context.Background(), conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	probe := &accountPrivacyTransport{}
	rec := llmrec.Wrap(probe, s.m.pg, "", "", "", "", func() bool { return false })
	req := llm.CompletionRequest{Messages: []llm.Message{llm.UserText("earlier transcript: " + strings.Join(secrets, " "))}}
	if _, _, _, err = rec.Complete(ctx, req); err != nil {
		t.Fatal(err)
	}
	for _, err := range rec.Stream(ctx, req) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(probe.bodies) != 2 {
		t.Fatal("expected both physical provider calls")
	}
	for _, body := range probe.bodies {
		for _, secret := range secrets {
			if strings.Contains(body, secret) {
				t.Fatal("provider received a stored password")
			}
		}
	}
	if !strings.Contains(req.Messages[0].Content[0].Text, secrets[0]) {
		t.Fatal("source evidence was mutated")
	}
}
