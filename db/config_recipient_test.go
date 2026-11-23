package db

import (
	"errors"
	"testing"
)

func TestSavedKeyBoundToRecipient(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	p := LLMProfile{Name: "recipient-binding-test", Format: "anthropic", Model: "m", BaseURL: "https://first.invalid", APIKey: "first-fixture-key"}
	p.ID, err = d.SaveProfile(&p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec("DELETE FROM llm_profiles WHERE id=$1", p.ID) })
	p.APIKey = ""
	p.BaseURL = "https://first.invalid/v1/messages"
	if _, err = d.SaveProfile(&p); err != nil {
		t.Fatal(err)
	}
	p.BaseURL = "https://second.invalid"
	if _, err = d.SaveProfile(&p); !errors.Is(err, ErrLLMKeyRecipientChanged) {
		t.Fatalf("wanted recipient error, got %v", err)
	}
	got, err := d.ProfileByID(p.ID)
	if err != nil || got.BaseURL != "https://first.invalid/v1/messages" || got.APIKey != "first-fixture-key" {
		t.Fatal("rejected edit changed stored profile")
	}
	p.APIKey = "replacement-fixture-key"
	if _, err = d.SaveProfile(&p); err != nil {
		t.Fatal(err)
	}
	p.APIKey = ""
	p.Format = "ollama"
	p.BaseURL = "http://127.0.0.1:11434"
	if _, err = d.SaveProfile(&p); err != nil {
		t.Fatal(err)
	}
	got, err = d.ProfileByID(p.ID)
	if err != nil || got.APIKey != "" || got.APIKeyHint != "" {
		t.Fatal("key or hint inherited by keyless destination")
	}
}
