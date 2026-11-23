package db

import (
	"fmt"
	"testing"
	"time"
)

func TestProfileCyberAccessProgramRoundTrip(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	t.Cleanup(func() { d.Close() })
	id, err := d.SaveProfile(&LLMProfile{
		Name: fmt.Sprintf("t-cyber-%d", time.Now().UnixNano()), Format: "openai-responses",
		BaseURL: "https://api.openai.com/v1", Model: "gpt-6-sol", APIKey: "test-key",
		CyberAccessProgram: "daybreak_blue",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deleteTestProfile(t, d, id) })
	p, err := d.ProfileByID(id)
	if err != nil || p == nil || p.CyberAccessProgram != "daybreak_blue" {
		t.Fatalf("keyed read: profile=%+v err=%v", p, err)
	}
	profiles, err := d.ListProfiles()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range profiles {
		if item.ID == id {
			found = item.CyberAccessProgram == "daybreak_blue"
		}
	}
	if !found {
		t.Fatal("list read lost cyber access program")
	}
	p.APIKey = "" // retain the stored key on an update
	p.CyberAccessProgram = ""
	if _, err := d.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	p, err = d.ProfileByID(id)
	if err != nil || p == nil || p.CyberAccessProgram != "" || p.APIKey != "test-key" {
		t.Fatalf("updated read: profile=%+v err=%v", p, err)
	}
}
