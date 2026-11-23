package db

import (
	"encoding/json"
	"testing"
)

func TestArchiveRejectsMissingReviewAndApproval(t *testing.T) {
	for _, nodes := range []string{
		`[{"id":1}]`,
		`[{"proposal_review":{"revision":1,"required_at":"1","required_by":"operator","approved_revision":1,"approved_by":"operator","approved_at":"2","approvals":[]}}]`,
	} {
		s := &TaskArchiveSnapshot{Tables: map[string]json.RawMessage{"exploration_nodes": json.RawMessage(nodes)}, DataCounts: map[string]int64{"proposal_reviews": 1}}
		if err := validateArchiveReviews(s); err == nil {
			t.Fatal("restore accepted incomplete approval metadata")
		}
	}
}
