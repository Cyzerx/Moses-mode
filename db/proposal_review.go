package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

const approvedProposalSQL = "(proposal_review IS NULL OR proposal_review->>'approved_revision' = proposal_review->>'revision')"

type ReviewableProposal struct {
	ID       int64           `json:"id,string"`
	Payload  json.RawMessage `json:"payload"`
	Priority int             `json:"priority"`
	State    string          `json:"state"`
	Review   json.RawMessage `json:"review"`
}

const proposalColumns = "id,payload,priority,state,COALESCE(proposal_review,'null'::jsonb)"
const initialReview = "jsonb_build_object('revision',1,'required_by',$3::text,'required_at',now(),'approvals','[]'::jsonb)"

func scanProposal(row interface{ Scan(...any) error }) (ReviewableProposal, error) {
	var p ReviewableProposal
	err := row.Scan(&p.ID, &p.Payload, &p.Priority, &p.State, &p.Review)
	return p, err
}

func (s *ExplorationStore) ReviewableProposals(ctx context.Context) ([]ReviewableProposal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+proposalColumns+` FROM exploration_nodes
		WHERE exploration_id=$1 AND kind='intent' AND (proposal_review IS NOT NULL OR state IN ('open','paused'))
		ORDER BY (proposal_review IS NOT NULL AND NOT `+approvedProposalSQL+`) DESC NULLS FIRST, id DESC`, s.expID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewableProposal{}
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Archive queueing also locks the task row. A decision recorded here will
// therefore either be in the archive snapshot or fail before it is written.
func (s *ExplorationStore) writeProposal(ctx context.Context, query string, args ...any) (p ReviewableProposal, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	var taskID int64
	if err = tx.QueryRowContext(ctx, "SELECT id FROM tasks WHERE exploration_id=$1", s.expID).Scan(&taskID); err != nil {
		return p, err
	}
	if err = LockTaskEvidenceTx(tx, taskID); err != nil {
		return p, err
	}
	p, err = scanProposal(tx.QueryRowContext(ctx, query, args...))
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}

// CreateReviewedProposal inserts the review flag in the same statement as the
// proposal. It cannot become eligible between creation and a separate hold.
func (s *ExplorationStore) CreateReviewedProposal(ctx context.Context, summary, operator string) (ReviewableProposal, error) {
	summary = strings.TrimSpace(summary)
	if operator == "" || summary == "" || len(summary) > 8000 {
		return ReviewableProposal{}, errors.New("operator and a proposal of 1–8000 bytes required")
	}
	return s.writeProposal(ctx, `INSERT INTO exploration_nodes(exploration_id,kind,payload,priority,state,origin,proposal_review)
		VALUES($1,'intent',jsonb_build_object('summary',$2::text),5,'open','human',`+initialReview+`) RETURNING `+proposalColumns, s.expID, summary, operator)
}

func (s *ExplorationStore) ReviewProposal(ctx context.Context, id, revision int64, action, operator, summary string) (ReviewableProposal, error) {
	if operator == "" {
		return ReviewableProposal{}, errors.New("authenticated operator required")
	}
	where := ` WHERE exploration_id=$1 AND id=$2 AND kind='intent' AND state IN ('open','paused') `
	args := []any{s.expID, id, operator}
	var update string
	switch action {
	case "require":
		update = `SET proposal_review=` + initialReview + where + ` AND proposal_review IS NULL`
	case "approve":
		args = append(args, revision)
		// Preserve each approval, its operator and the exact reviewed proposal.
		update = `SET proposal_review=proposal_review || jsonb_build_object(
			'approved_revision',$4::bigint,'approved_by',$3::text,'approved_at',now(),
			'approvals',COALESCE(proposal_review->'approvals','[]'::jsonb) || jsonb_build_array(jsonb_build_object(
				'revision',$4::bigint,'by',$3::text,'at',now(),'payload',payload,'priority',priority)))` +
			where + ` AND (proposal_review->>'revision')::bigint=$4 AND (proposal_review->>'approved_revision') IS DISTINCT FROM (proposal_review->>'revision')`
	case "edit":
		summary = strings.TrimSpace(summary)
		if summary == "" || len(summary) > 8000 {
			return ReviewableProposal{}, errors.New("a proposal of 1–8000 bytes is required")
		}
		args = []any{s.expID, id, summary, revision}
		update = `SET payload=jsonb_set(payload,'{summary}',to_jsonb($3::text))` + where + ` AND (proposal_review->>'revision')::bigint=$4`
	default:
		return ReviewableProposal{}, errors.New("unknown review action")
	}
	p, err := s.writeProposal(ctx, `UPDATE exploration_nodes `+update+` RETURNING `+proposalColumns, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return p, errors.New("proposal state or revision changed; reopen it before deciding")
	}
	return p, err
}

// validateArchiveReviews rejects a missing or malformed approval instead of
// allowing a restored proposal to fall back to ordinary work.
func validateArchiveReviews(s *TaskArchiveSnapshot) error {
	var nodes []struct {
		Review *struct {
			Revision         int64  `json:"revision"`
			RequiredBy       string `json:"required_by"`
			RequiredAt       string `json:"required_at"`
			ApprovedRevision *int64 `json:"approved_revision"`
			ApprovedBy       string `json:"approved_by"`
			ApprovedAt       string `json:"approved_at"`
			Approvals        *[]struct {
				Revision int64  `json:"revision"`
				By       string `json:"by"`
				At       string `json:"at"`
			} `json:"approvals"`
		} `json:"proposal_review"`
	}
	if raw := s.Tables["exploration_nodes"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &nodes); err != nil {
			return err
		}
	}
	var count int64
	for _, n := range nodes {
		r := n.Review
		if r == nil {
			continue
		}
		count++
		if r.Revision < 1 || r.RequiredBy == "" || r.RequiredAt == "" || r.Approvals == nil {
			return errors.New("archive contains incomplete proposal review")
		}
		recorded := false
		for _, a := range *r.Approvals {
			if a.Revision < 1 || a.Revision > r.Revision || a.By == "" || a.At == "" {
				return errors.New("archive contains incomplete proposal approval history")
			}
			if a.Revision == r.Revision && a.By == r.ApprovedBy && a.At == r.ApprovedAt {
				recorded = true
			}
		}
		if r.ApprovedRevision != nil && (*r.ApprovedRevision != r.Revision || !recorded) {
			return errors.New("archive is missing the current approval receipt")
		}
		if r.ApprovedRevision == nil && (r.ApprovedBy != "" || r.ApprovedAt != "") {
			return errors.New("archive is missing the approved proposal revision")
		}
	}
	if expected, ok := s.DataCounts["proposal_reviews"]; ok && count != expected {
		return errors.New("archive proposal review count mismatch")
	}
	return nil
}
