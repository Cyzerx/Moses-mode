package traffic

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CaptureAssignment struct {
	ID         string `json:"id"`
	Method     string `json:"method"`
	URL        string `json:"url"`
	TaskID     int64  `json:"task_id,omitempty"`
	AssignedBy string `json:"assigned_by,omitempty"`
	AssignedAt int64  `json:"assigned_at,omitempty"`
}

// ReviewAssignments previews the exact IDs a human selected. When taskID is
// supplied, every assignment and receipt is committed in one SQLite transaction.
// The receipt preserves the original operator and time on a retried submission.
func (t *Traffic) ReviewAssignments(ctx context.Context, ids []string, taskID int64, actor string) ([]CaptureAssignment, error) {
	if len(ids) == 0 || len(ids) > 100 || taskID < 0 || actor == "" {
		return nil, errors.New("a signed-in operator and 1–100 selected exchanges are required")
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	items := make([]CaptureAssignment, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			return nil, errors.New("invalid or duplicate exchange selection")
		}
		seen[id] = true
		var c CaptureAssignment
		err = tx.QueryRowContext(ctx, `SELECT e.id,e.method,e.url,COALESCE(e.task_id,0),COALESCE(a.operator,''),COALESCE(a.assigned_at,0)
			FROM exchanges e LEFT JOIN exchange_assignments a ON a.exchange_id=e.id WHERE e.id=?`, id).Scan(&c.ID, &c.Method, &c.URL, &c.TaskID, &c.AssignedBy, &c.AssignedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("a selected exchange is no longer available")
		}
		if err != nil {
			return nil, err
		}
		if taskID > 0 && c.TaskID != 0 && (c.TaskID != taskID || c.AssignedBy == "") {
			return nil, fmt.Errorf("exchange %s already belongs to an engagement", id)
		}
		if taskID > 0 && c.TaskID == 0 {
			if _, live := t.liveStreams.Load(id); live {
				return nil, errors.New("wait for the selected stream to finish before assigning it")
			}
			c.AssignedAt = time.Now().UTC().Unix()
			c.AssignedBy = actor
			c.TaskID = taskID
			res, err := tx.ExecContext(ctx, `UPDATE exchanges SET task_id=? WHERE id=? AND task_id IS NULL`, taskID, id)
			if err != nil {
				return nil, err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return nil, err
			}
			if n != 1 {
				return nil, errors.New("capture ownership changed")
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO exchange_assignments(exchange_id,operator,assigned_at) VALUES(?,?,?)`, id, actor, c.AssignedAt); err != nil {
				return nil, err
			}
		}
		items = append(items, c)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func (t *Traffic) legacyExchangeDirectory(relative string) (string, error) {
	clean := filepath.Clean(relative)
	if !filepath.IsLocal(relative) || clean == "." || clean != relative || strings.HasPrefix(clean, "_") || strings.HasPrefix(clean, ".") {
		return "", errors.New("invalid legacy exchange directory")
	}
	base, err := filepath.EvalSymlinks(t.dir)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, clean)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	if resolved != dir {
		return "", errors.New("legacy exchange directory contains a symbolic link")
	}
	for _, file := range []string{"request.http", "response.http"} {
		info, err := os.Lstat(filepath.Join(dir, file))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("legacy capture body is not a regular file")
		}
	}
	return dir, nil
}

func (t *Traffic) taskLegacyFiles(tx *sql.Tx, taskID int64) ([]string, error) {
	rows, err := tx.Query(`SELECT DISTINCT e.path FROM exchanges e WHERE e.task_id=? AND e.path<>''
		AND NOT EXISTS(SELECT 1 FROM exchanges sibling WHERE sibling.path=e.path AND sibling.task_id IS NOT e.task_id)`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []string
	for rows.Next() {
		var rel string
		if err := rows.Scan(&rel); err != nil {
			return nil, err
		}
		dir, err := t.legacyExchangeDirectory(rel)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		files = append(files, filepath.Join(dir, "request.http"), filepath.Join(dir, "response.http"))
	}
	return files, rows.Err()
}
