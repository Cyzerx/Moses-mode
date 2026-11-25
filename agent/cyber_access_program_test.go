package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Cyzerx/Moses-x1/llmrec"
)

func TestValidateCyberAccessProgram(t *testing.T) {
	for _, tc := range []struct {
		format  llm.Format
		base    string
		program string
		ok      bool
	}{
		{llm.FormatOpenAIResponses, "", "", true},
		{llm.FormatOpenAIResponses, "", "daybreak_blue", true},
		{llm.FormatOpenAIResponses, "https://api.openai.com/v1", "daybreak_red", true},
		{llm.FormatOpenAIResponses, "https://api.openai.com/v1", "standard", true},
		{llm.FormatOpenAIResponses, "http://api.openai.com/v1", "daybreak_blue", false},
		{llm.FormatOpenAIResponses, "https://api.openai.com.evil.example/v1", "daybreak_blue", false},
		{llm.FormatOpenAIResponses, "https://api.openai.com/v1/other", "daybreak_blue", false},
		{llm.FormatOpenAI, "https://api.openai.com/v1", "daybreak_blue", false},
		{llm.FormatAnthropic, "", "daybreak_blue", false},
		{llm.FormatOpenAIResponses, "", "unknown", false},
	} {
		err := ValidateCyberAccessProgram(tc.format, tc.base, tc.program)
		if (err == nil) != tc.ok {
			t.Errorf("format=%q base=%q program=%q: err=%v, want success=%v", tc.format, tc.base, tc.program, err, tc.ok)
		}
	}
	if _, err := (Config{Format: llm.FormatOpenAI, CyberAccessProgram: "daybreak_blue"}).NewProvider(); err == nil {
		t.Fatal("NewProvider accepted a cyber program on Chat Completions")
	}
}

func TestCyberAccessProgramRequestBody(t *testing.T) {
	for _, streaming := range []bool{true, false} {
		original := `{"model":"gpt-6-sol","stream":` + map[bool]string{true: "true", false: "false"}[streaming] + `,"instructions":"keep this text","input":[{"role":"user","content":"hello"}]}`
		req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader(original))
		if err != nil {
			t.Fatal(err)
		}
		modified, err := withCyberAccessProgram(req, "daybreak_blue")
		if err != nil {
			t.Fatal(err)
		}
		wire, err := io.ReadAll(modified.Body)
		if err != nil {
			t.Fatal(err)
		}
		if modified.ContentLength != int64(len(wire)) {
			t.Fatalf("content length=%d, payload length=%d", modified.ContentLength, len(wire))
		}
		var body struct {
			Model          string `json:"model"`
			Stream         bool   `json:"stream"`
			Instructions   string `json:"instructions"`
			AccessPrograms struct {
				Cyber string `json:"cyber"`
			} `json:"access_programs"`
		}
		if err := json.Unmarshal(wire, &body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "gpt-6-sol" || body.Stream != streaming || body.Instructions != "keep this text" || body.AccessPrograms.Cyber != "daybreak_blue" {
			t.Fatalf("unexpected body: %+v", body)
		}
		originalAgain, err := req.GetBody()
		if err != nil {
			t.Fatal(err)
		}
		unchanged, _ := io.ReadAll(originalAgain)
		originalAgain.Close()
		if string(unchanged) != original {
			t.Fatal("request mutation changed the original body")
		}
	}
}

func TestCyberAccessProgramFailsClosed(t *testing.T) {
	for _, endpoint := range []string{
		"http://api.openai.com/v1/responses",
		"https://api.openai.com.evil.example/v1/responses",
		"https://api.openai.com/v1/chat/completions",
		"https://api.openai.com/v1/responses?redirect=1",
	} {
		req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := withCyberAccessProgram(req, "daybreak_blue"); err == nil {
			t.Errorf("allowed cyber program on %s", endpoint)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader(`{"access_programs":{"cyber":"standard"}}`))
	if _, err := withCyberAccessProgram(req, "daybreak_blue"); err == nil {
		t.Fatal("overwrote a provider-supplied access_programs value")
	}
}

func TestCyberAccessProgramTransportAndCapture(t *testing.T) {
	var received string
	transport := quotaAwareTransport{
		base: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			received = string(body)
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
		}),
		cyberAccessProgram: "daybreak_blue",
	}
	req, _ := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader(`{"model":"gpt-6-sol","stream":true}`))
	ctx, capture := llmrec.NewCapture(req.Context())
	resp, err := transport.RoundTrip(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if received == "" || capture.RawRequest() != received {
		t.Fatalf("capture and wire mismatch: captured=%q sent=%q", capture.RawRequest(), received)
	}
	var body struct {
		AccessPrograms struct {
			Cyber string `json:"cyber"`
		} `json:"access_programs"`
	}
	if err := json.Unmarshal([]byte(received), &body); err != nil || body.AccessPrograms.Cyber != "daybreak_blue" {
		t.Fatalf("program on wire: body=%q err=%v", received, err)
	}
}
