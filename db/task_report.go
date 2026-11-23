package db

import (
	"context"
	"database/sql"
)

// TaskFindingTotals includes standalone findings and older graph findings with
// no standalone row. Only standalone rows have a disposition or verification.
type TaskFindingTotals struct {
	Recorded        int
	Confirmed       int
	Verified        int
	WithoutMetadata int
}

// ReviewFinding is the full disclosure allowlist for offline handoffs. Binding
// identifiers are local to Artex-Moses-mode. Raw traffic identifiers and contents stay private.
type ReviewFinding struct {
	ID           int64
	Title        string
	Disposition  string
	Verification string
	Freshness    string
	Bindings     []ReviewBinding
}

type ReviewBinding struct {
	ID   int64
	Role string
}

// SelectedReviewFindings reads one engagement and one snapshot of its selected
// findings and their references. It never loads request/response data or notes.
func (d *DB) SelectedReviewFindings(ctx context.Context, taskID int64, ids []int64, includeTitles bool) ([]ReviewFinding, error) {
	rows, err := d.QueryContext(ctx, `SELECT f.id, CASE WHEN $3 THEN f.name ELSE '' END,
			f.status, f.verification_status,
			CASE WHEN f.report = '' THEN 'No report'
			     WHEN f.report_evidence_version = f.evidence_version THEN 'Current' ELSE 'Stale' END,
			b.id, b.role
		FROM findings f LEFT JOIN finding_traffic_bindings b ON b.finding_id = f.id
		WHERE f.task_id = $1 AND f.id = ANY($2::bigint[])
		ORDER BY f.id, b.position, b.id`, taskID, ids, includeTitles)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ReviewFinding
	for rows.Next() {
		var finding ReviewFinding
		var bindingID sql.NullInt64
		var role sql.NullString
		if err := rows.Scan(&finding.ID, &finding.Title, &finding.Disposition, &finding.Verification,
			&finding.Freshness, &bindingID, &role); err != nil {
			return nil, err
		}
		if len(result) == 0 || result[len(result)-1].ID != finding.ID {
			if !ValidFindingStatus(finding.Disposition) {
				finding.Disposition = "unknown"
			}
			if !ValidVerificationStatus(finding.Verification) {
				finding.Verification = "unknown"
			}
			result = append(result, finding)
		}
		if bindingID.Valid {
			label := "unknown"
			if ValidTrafficRole(role.String) {
				label = role.String
			}
			last := &result[len(result)-1]
			last.Bindings = append(last.Bindings, ReviewBinding{ID: bindingID.Int64, Role: label})
		}
	}
	return result, rows.Err()
}

func (d *DB) TaskFindingTotals(ctx context.Context, taskID int64) (TaskFindingTotals, error) {
	var totals TaskFindingTotals
	err := d.QueryRowContext(ctx, `WITH graph_only AS (
		SELECT COUNT(*) AS n FROM exploration_nodes n
		JOIN tasks t ON t.exploration_id = n.exploration_id
		WHERE t.id = $1 AND n.kind = 'finding'
		AND NOT EXISTS (SELECT 1 FROM findings f WHERE f.task_id = t.id AND f.node_id = n.id)
	)
	SELECT COUNT(*) + (SELECT n FROM graph_only),
	       COUNT(*) FILTER (WHERE status = $2),
	       COUNT(*) FILTER (WHERE verification_status = $3),
	       (SELECT n FROM graph_only)
	FROM findings WHERE task_id = $1`, taskID, FindingConfirmed, VerificationVerified).
		Scan(&totals.Recorded, &totals.Confirmed, &totals.Verified, &totals.WithoutMetadata)
	return totals, err
}
