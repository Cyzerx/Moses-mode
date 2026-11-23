package llmrec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/Autumn-27/norma/llm"
)

var errPrivateHistory = errors.New("model request blocked: private account material in this conversation cannot be safely replayed; start a clean session")

type privateTasksContextKey struct{}

// WithPrivateTaskIDs adds each contributor to the privacy filter on a merged
// conversation. It confers no permission to read any contributor's raw traffic.
func WithPrivateTaskIDs(ctx context.Context, ids []int64) context.Context {
	return context.WithValue(ctx, privateTasksContextKey{}, slices.Clone(ids))
}

// Runs before recording and before the physical provider call, on every
// profile, including retries, summary calls and side questions. Work on private
// slices so removing a secret never edits the live transcript/evidence.
func (r *Recorder) protectTaskPrompt(ctx context.Context, req llm.CompletionRequest) (llm.CompletionRequest, error) {
	var secrets []string
	ids, _ := ctx.Value(privateTasksContextKey{}).([]int64)
	if id := TaskIDFrom(ctx); id != "" && r.pg != nil {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n <= 0 {
			return req, errPrivateHistory
		}
		if !slices.Contains(ids, n) {
			ids = append(slices.Clone(ids), n)
		}
	}
	if len(ids) > 0 && r.pg != nil {
		var err error
		secrets, err = r.pg.ModelPasswordPatterns(ctx, ids)
		if err != nil {
			return req, errors.New("model request blocked: task credential privacy check unavailable")
		}
	}
	return redactAccountHistory(req, secrets)
}

func redactAccountHistory(req llm.CompletionRequest, secrets []string) (llm.CompletionRequest, error) {
	// Account names and passwords may have changed or been deleted since an
	// older session was recorded. Drop its structured account material by shape.
	var replacements []string
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		replacements = append(replacements, secret, "[private]")
		quoted, _ := json.Marshal(secret)
		escaped := string(quoted[1 : len(quoted)-1])
		if escaped != secret {
			replacements = append(replacements, escaped, "[private]")
		}
	}
	var replacer *strings.Replacer
	if len(replacements) > 0 {
		replacer = strings.NewReplacer(replacements...)
	}
	rewrite := func(s string) string {
		if strings.Contains(s, `"task_credentials"`) && strings.Contains(s, `"password"`) {
			return "[legacy account overview withheld]"
		}
		if strings.Contains(s, `"credentials"`) && strings.Contains(s, `"password"`) {
			return "[legacy account list withheld]"
		}
		if replacer != nil {
			return replacer.Replace(s)
		}
		return s
	}
	// Keep signed/structural fields byte-for-byte valid. If an old tool
	// argument or a configured schema embeds an actual task secret, fail
	// closed instead of producing a different command or invalid JSON.
	sensitiveJSON := func(raw []byte) bool {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		for {
			token, err := d.Token()
			if err == io.EOF {
				return false
			}
			if err != nil {
				return true
			}
			if text, ok := token.(string); ok && rewrite(text) != text {
				return true
			}
		}
	}
	if replacer != nil {
		req.Tools = slices.Clone(req.Tools)
		for i := range req.Tools {
			tool := &req.Tools[i]
			tool.Description = rewrite(tool.Description)
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil || sensitiveJSON(schema) {
				return req, errPrivateHistory
			}
		}
		req.Stop = slices.Clone(req.Stop)
		for i, value := range req.Stop {
			req.Stop[i] = rewrite(value)
		}
	}
	req.System = slices.Clone(req.System)
	for i, s := range req.System {
		req.System[i] = rewrite(s)
	}
	req.Messages = slices.Clone(req.Messages)
	var scrub func([]llm.ContentBlock) ([]llm.ContentBlock, error)
	scrub = func(blocks []llm.ContentBlock) ([]llm.ContentBlock, error) {
		out := slices.Clone(blocks)
		for i := range out {
			b := &out[i]
			b.Text = rewrite(b.Text)
			// Signed reasoning and opaque provider state cannot be altered safely.
			if rewrite(b.Thinking) != b.Thinking || rewrite(b.Signature) != b.Signature ||
				rewrite(b.ThoughtSignature) != b.ThoughtSignature || rewrite(b.Data) != b.Data {
				return nil, errPrivateHistory
			}
			if b.Name == "save_task_credential" && len(b.Input) > 0 {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(b.Input, &fields); err != nil {
					return nil, errPrivateHistory
				}
				if _, legacy := fields["password"]; legacy {
					b.Input = json.RawMessage(`{}`)
				}
			}
			if replacer != nil && len(b.Input) > 0 && sensitiveJSON(b.Input) {
				return nil, errPrivateHistory
			}

			if len(b.Content) > 0 {
				nested, err := scrub(b.Content)
				if err != nil {
					return nil, err
				}
				b.Content = nested
			}
		}
		return out, nil
	}
	for i := range req.Messages {
		visible, err := scrub(req.Messages[i].Content)
		if err != nil {
			return req, err
		}
		req.Messages[i].Content = visible
	}
	return req, nil
}
