package evidence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"

	"github.com/Cyzerx/Artex-Moses-mode/db"
)

type IntegrityIssue struct {
	Code    string `json:"code"`
	Record  string `json:"record"`
	ID      int64  `json:"id,string"`
	Related int64  `json:"related,string,omitempty"`
	Side    string `json:"side,omitempty"`
}

type IntegrityReport struct {
	Mode           string           `json:"mode"`
	Problems       int              `json:"problems"`
	StaleReports   int              `json:"stale_reports"`
	LegacyFindings int              `json:"unlinked_findings"`
	Bindings       int              `json:"bindings"`
	BodiesChecked  int              `json:"bodies_checked"`
	BodiesSkipped  int              `json:"bodies_skipped"`
	Issues         []IntegrityIssue `json:"issues"`
}

func (r *IntegrityReport) add(issue IntegrityIssue) {
	count := &r.Problems
	switch issue.Code {
	case "stale_report":
		count = &r.StaleReports
	case "unlinked_finding":
		count = &r.LegacyFindings
	}
	*count++
	// Keep scores exact but limit display to 100 examples per category. Many
	// legacy notices should never hide an actual integrity failure.
	if *count <= 100 {
		r.Issues = append(r.Issues, issue)
	}
}

type bodyReference struct {
	binding int64
	side    string
	hash    string
	length  int64
}

// VerifyTask captures one consistent relational snapshot before reading the
// immutable body files. An operator can explicitly omit the file pass when
// checking a DB-only backup.
func (s *Store) VerifyTask(ctx context.Context, taskID int64, databaseOnly bool) (IntegrityReport, error) {
	r := IntegrityReport{Mode: "full", Issues: []IntegrityIssue{}}
	if databaseOnly {
		r.Mode = "database-only"
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	var expID int64
	if err := tx.QueryRowContext(ctx, "SELECT exploration_id FROM tasks WHERE id=$1 AND deleted_at IS NULL", taskID).Scan(&expID); err != nil {
		return r, err
	}
	// Direct, declared source explorations can supply lineage. A finding's own
	// standalone record must still refer to a node from its local exploration.
	rows, err := tx.QueryContext(ctx, `WITH permitted AS (
		SELECT $2::bigint AS id UNION
		SELECT source.exploration_id FROM task_relations rel JOIN tasks source ON source.id=rel.source_task_id
		WHERE rel.task_id=$1 AND source.deleted_at IS NULL
	)
	SELECT 'edge_outside_engagement', 'edge', e.src_id, e.dst_id
	FROM exploration_edges e LEFT JOIN exploration_nodes src ON src.id=e.src_id LEFT JOIN exploration_nodes dst ON dst.id=e.dst_id
	WHERE e.exploration_id=$2 AND (NOT EXISTS (SELECT 1 FROM permitted WHERE id=src.exploration_id)
		OR NOT EXISTS (SELECT 1 FROM permitted WHERE id=dst.exploration_id)
		OR (src.exploration_id<>$2 AND dst.exploration_id<>$2))
	UNION ALL SELECT 'invalid_proof_edge','edge',e.src_id,e.dst_id FROM exploration_edges e
		JOIN exploration_nodes src ON src.id=e.src_id JOIN exploration_nodes dst ON dst.id=e.dst_id
		WHERE e.exploration_id=$2 AND e.rel='proves'
		AND (src.kind NOT IN ('finding','fact') OR dst.kind<>'goal' OR dst.exploration_id<>$2)
	UNION ALL SELECT 'invalid_finding_node','finding',f.id,f.node_id FROM findings f
		LEFT JOIN exploration_nodes n ON n.id=f.node_id
		WHERE f.task_id=$1 AND f.node_id IS NOT NULL AND (n.id IS NULL OR n.exploration_id<>$2 OR n.kind<>'finding')
	UNION ALL SELECT 'unlinked_finding','finding',id,0 FROM findings WHERE task_id=$1 AND node_id IS NULL
	UNION ALL SELECT 'unlinked_finding','graph node',n.id,0 FROM exploration_nodes n
		WHERE n.exploration_id=$2 AND n.kind='finding' AND NOT EXISTS (SELECT 1 FROM findings f WHERE f.task_id=$1 AND f.node_id=n.id)
	UNION ALL SELECT 'stale_report','finding',id,0 FROM findings WHERE task_id=$1 AND report<>'' AND evidence_version<>report_evidence_version
	ORDER BY 1,2,3,4`, taskID, expID)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var issue IntegrityIssue
		if err := rows.Scan(&issue.Code, &issue.Record, &issue.ID, &issue.Related); err != nil {
			return r, err
		}
		r.add(issue)
	}
	if err := rows.Err(); err != nil {
		return r, err
	}
	refs, err := r.readBindings(ctx, tx, taskID)
	if err != nil {
		return r, err
	}
	if err := tx.Commit(); err != nil {
		return r, err
	}
	type bodyKey struct {
		hash string
		size int64
	}
	cache := map[bodyKey]string{}
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		k := bodyKey{ref.hash, ref.length}
		code, seen := cache[k]
		if !seen {
			path, err := hashPath(s.Dir, ref.hash)
			switch {
			case err != nil || ref.length < 0:
				code = "invalid_body_metadata"
			case databaseOnly:
				r.BodiesSkipped++
			default:
				r.BodiesChecked++
				code = checkBody(ctx, path, ref.hash, ref.length)
			}
			cache[k] = code
		}
		if code != "" {
			r.add(IntegrityIssue{Code: code, Record: "binding", ID: ref.binding, Side: ref.side})
		}
	}
	return r, ctx.Err()
}

func (r *IntegrityReport) readBindings(ctx context.Context, tx *sql.Tx, taskID int64) ([]bodyReference, error) {
	rows, err := tx.QueryContext(ctx, `SELECT b.id, b.role, CASE WHEN s.id IS NOT NULL THEN to_jsonb(s) END
		FROM findings f JOIN finding_traffic_bindings b ON b.finding_id=f.id
		LEFT JOIN traffic_evidence_snapshots s ON s.id=b.snapshot_id
		WHERE f.task_id=$1 ORDER BY b.id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []bodyReference
	identity := make(map[string]string)
	for rows.Next() {
		var id int64
		var role string
		var raw []byte
		if err := rows.Scan(&id, &role, &raw); err != nil {
			return nil, err
		}
		r.Bindings++
		if !db.ValidTrafficRole(role) {
			r.add(IntegrityIssue{Code: "invalid_binding_role", Record: "binding", ID: id})
		}
		if len(raw) == 0 {
			r.add(IntegrityIssue{Code: "missing_snapshot", Record: "binding", ID: id})
			continue
		}
		var snapshot db.TrafficEvidenceSnapshot
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			r.add(IntegrityIssue{Code: "unreadable_snapshot", Record: "binding", ID: id})
			continue
		}
		code, seen := identity[snapshot.ID]
		if !seen {
			ok, err := snapshot.CheckStoredIdentity()
			if err != nil {
				code = "unreadable_snapshot"
			} else if !ok {
				code = "snapshot_mismatch"
			}
			identity[snapshot.ID] = code
		}
		if code != "" {
			r.add(IntegrityIssue{Code: code, Record: "binding", ID: id})
		}
		refs = append(refs, bodyReference{id, "request", snapshot.ReqHash, snapshot.ReqLen},
			bodyReference{id, "response", snapshot.RespHash, snapshot.RespLen})
	}
	return refs, rows.Err()
}

func checkBody(ctx context.Context, path, hash string, length int64) string {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "missing_body"
	}
	if err != nil {
		return "unreadable_body"
	}
	if !info.Mode().IsRegular() || info.Size() != length {
		return "body_mismatch"
	}
	err = verifyFileContext(ctx, path, hash, length)
	switch {
	case err == nil:
		return ""
	case errors.Is(err, os.ErrNotExist):
		return "missing_body"
	case errors.Is(err, errBodyMismatch):
		return "body_mismatch"
	default:
		return "unreadable_body"
	}
}
