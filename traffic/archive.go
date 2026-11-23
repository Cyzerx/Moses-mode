package traffic

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ArchiveSnapshot is the portable traffic subset embedded in one task archive.
// Large content-addressed bodies are copied alongside this manifest rather than
// base64-encoded, preserving deduplication and allowing the tar writer to stream.
type ArchiveSnapshot struct {
	Version   int               `json:"version"`
	TaskID    int64             `json:"task_id,omitempty"`
	Exchanges []ArchiveExchange `json:"exchanges"`
	Blobs     []string          `json:"blobs"`
}

type ArchiveExchange struct {
	Stream      []archiveStreamRow  `json:"stream,omitempty"`
	Rewrites    []archiveRewriteRow `json:"rewrites,omitempty"`
	AssignedBy  string              `json:"assigned_by,omitempty"`
	AssignedAt  int64               `json:"assigned_at,omitempty"`
	ID          string              `json:"id"`
	TS          int64               `json:"ts"`
	Host        string              `json:"host"`
	Method      string              `json:"method"`
	URLTemplate string              `json:"url_template"`
	URL         string              `json:"url"`
	Status      int                 `json:"status"`
	ContentType string              `json:"content_type"`
	ReqLen      int                 `json:"req_len"`
	RespLen     int                 `json:"resp_len"`
	ReqHead     string              `json:"req_head"`
	ReqBody     []byte              `json:"req_body,omitempty"`
	ReqBlob     string              `json:"req_blob,omitempty"`
	RespHead    string              `json:"resp_head"`
	RespBody    []byte              `json:"resp_body,omitempty"`
	RespBlob    string              `json:"resp_blob,omitempty"`
}

// ExportHosts writes an exact-host snapshot to dir. It holds the traffic writer
// lock while reading SQLite and blobs, so each body and its index row come from
// one consistent point in time.
func (t *Traffic) ExportHosts(hosts []string, dir string) (int64, error) {
	if t == nil || len(hosts) == 0 {
		return 0, nil
	}
	unique := uniqueArchiveHosts(hosts)
	placeholders := make([]string, len(unique))
	args := make([]any, len(unique))
	for i, host := range unique {
		placeholders[i] = "?"
		args[i] = host
	}
	return t.exportArchive(dir, 0, "host IN ("+strings.Join(placeholders, ",")+")", args...)
}

// ExportTask packages only captures with trusted ownership, even for shared hosts.
func (t *Traffic) ExportTask(taskID int64, dir string) (int64, error) {
	if taskID <= 0 {
		return 0, errors.New("missing traffic engagement")
	}
	return t.exportArchive(dir, taskID, "task_id=?", taskID)
}

func (t *Traffic) exportArchive(dir string, taskID int64, filter string, args ...any) (int64, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return 0, err
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	rows, err := t.db.Query(`SELECT id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path
FROM exchanges WHERE `+filter+` ORDER BY ts,id`, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	snapshot := ArchiveSnapshot{Version: 1}
	if taskID > 0 {
		snapshot.Version = 2
		snapshot.TaskID = taskID
	}
	blobs := map[string]struct{}{}
	for rows.Next() {
		var item ArchiveExchange
		var legacyPath string
		if err := rows.Scan(&item.ID, &item.TS, &item.Host, &item.Method, &item.URLTemplate,
			&item.URL, &item.Status, &item.ContentType, &item.ReqLen, &item.RespLen, &legacyPath); err != nil {
			return 0, err
		}
		if taskID > 0 {
			err := t.db.QueryRow(`SELECT operator,assigned_at FROM exchange_assignments WHERE exchange_id=?`, item.ID).Scan(&item.AssignedBy, &item.AssignedAt)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return 0, err
			}
		}
		var reqBlob, respBlob sql.NullString
		err := t.db.QueryRow(`SELECT req_head,req_body,req_blob,resp_head,resp_body,resp_blob
FROM exchange_bodies WHERE id=?`, item.ID).Scan(&item.ReqHead, &item.ReqBody, &reqBlob, &item.RespHead, &item.RespBody, &respBlob)
		if errors.Is(err, sql.ErrNoRows) && strings.TrimSpace(legacyPath) != "" {
			// Preserve both halves of the legacy wire files, including the body.
			err = t.readEvidence(item.ID, func(e EvidenceExchange) error {
				item.ReqHead, item.RespHead = e.ReqHead, e.RespHead
				var err error
				if item.ReqBody, err = io.ReadAll(e.Request); err != nil {
					return err
				}
				if item.RespBody, err = io.ReadAll(e.Response); err != nil {
					return err
				}
				item.ReqLen, item.RespLen = len(item.ReqBody), len(item.RespBody)
				return nil
			})
			if err != nil {
				return 0, err
			}
		} else if err != nil {
			return 0, err
		}
		item.ReqBlob, item.RespBlob = reqBlob.String, respBlob.String
		if taskID > 0 {
			if err := t.exportDetails(&item); err != nil {
				return 0, err
			}
		}
		for _, hash := range item.blobHashes() {
			if hash != "" {
				blobs[hash] = struct{}{}
			}
		}
		snapshot.Exchanges = append(snapshot.Exchanges, item)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for hash := range blobs {
		source, _, err := t.Blob(hash)
		if err != nil {
			return 0, err
		}
		data, err := io.ReadAll(source)
		source.Close()
		if err != nil {
			return 0, err
		}
		if err := os.WriteFile(filepath.Join(dir, "blobs", hash+".bin"), data, 0o600); err != nil {
			return 0, err
		}
		snapshot.Blobs = append(snapshot.Blobs, hash)
	}
	sort.Strings(snapshot.Blobs)
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(dir, "traffic.json"), raw, 0o600); err != nil {
		return 0, err
	}
	return int64(len(snapshot.Exchanges)), nil
}

// ImportArchive imports only missing exchange IDs. Current hot rows always win,
// and repeated restore attempts are safe after a partial external failure.
func (t *Traffic) ImportArchive(dir string) (int64, error) {
	return t.ImportTaskArchive(dir, 0)
}

// ImportTaskArchive requires explicit ownership in the source manifest. Old
// host-based packages remain visible in the global operator view only.
func (t *Traffic) ImportTaskArchive(dir string, taskID int64) (int64, error) {
	if t == nil {
		return 0, nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, "traffic.json"))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var snapshot ArchiveSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return 0, err
	}
	var owner any
	if snapshot.Version == 2 {
		if taskID <= 0 || snapshot.TaskID != taskID {
			return 0, errors.New("traffic archive engagement mismatch")
		}
		owner = taskID
	} else if snapshot.Version != 1 {
		return 0, fmt.Errorf("unsupported traffic archive version %d", snapshot.Version)
	}
	if snapshot.Version == 2 {
		packaged := make(map[string]bool, len(snapshot.Blobs))
		for _, h := range snapshot.Blobs {
			packaged[h] = true
		}
		for _, item := range snapshot.Exchanges {
			for _, h := range item.blobHashes() {
				if h != "" && !packaged[h] {
					return 0, errors.New("traffic archive references an unpackaged body")
				}
			}
		}
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	for _, hash := range snapshot.Blobs {
		if !blobHashRe.MatchString(hash) {
			return 0, fmt.Errorf("invalid archived traffic blob %q", hash)
		}
		data, err := os.ReadFile(filepath.Join(dir, "blobs", hash+".bin"))
		if err != nil {
			return 0, err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != hash {
			return 0, fmt.Errorf("traffic blob checksum mismatch: %s", hash)
		}
		if err := t.publishBlob(hash, data); err != nil {
			return 0, err
		}
	}
	tx, err := t.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	var imported int64
	for _, item := range snapshot.Exchanges {
		res, err := tx.Exec(`INSERT OR IGNORE INTO exchanges(id,task_id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES(?,?,?,?,?,?,?,?,?,?,?,'')`, item.ID, owner, item.TS, item.Host, item.Method, item.URLTemplate,
			item.URL, item.Status, item.ContentType, item.ReqLen, item.RespLen)
		if err != nil {
			return imported, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			if snapshot.Version == 2 {
				var existing sql.NullInt64
				if err := tx.QueryRow(`SELECT task_id FROM exchanges WHERE id=?`, item.ID).Scan(&existing); err != nil {
					return imported, err
				}
				if !existing.Valid || existing.Int64 != taskID {
					return imported, errors.New("restored exchange conflicts with existing ownership")
				}
			}
			continue
		}
		if snapshot.Version == 2 && item.AssignedBy != "" {
			if _, err := tx.Exec(`INSERT INTO exchange_assignments(exchange_id,operator,assigned_at) VALUES(?,?,?)`, item.ID, item.AssignedBy, item.AssignedAt); err != nil {
				return imported, err
			}
		}
		if _, err := tx.Exec(`INSERT INTO exchange_bodies(id,req_head,req_body,req_blob,resp_head,resp_body,resp_blob)
VALUES(?,?,?,?,?,?,?)`, item.ID, item.ReqHead, item.ReqBody, nullIfEmpty(item.ReqBlob),
			item.RespHead, item.RespBody, nullIfEmpty(item.RespBlob)); err != nil {
			return imported, err
		}
		if err := item.importDetails(tx); err != nil {
			return imported, err
		}
		for _, hash := range item.blobHashes() {
			if hash != "" {
				if _, err := tx.Exec(`INSERT OR IGNORE INTO blob_refs(hash,exchange_id) VALUES(?,?)`, hash, item.ID); err != nil {
					return imported, err
				}
			}
		}
		if t.fts {
			var rowID int64
			if err := tx.QueryRow(`SELECT rowid FROM exchanges WHERE id=?`, item.ID).Scan(&rowID); err != nil {
				return imported, err
			}
			content := strings.Join([]string{item.URL, item.ReqHead, string(item.ReqBody), item.RespHead, string(item.RespBody)}, "\n")
			if _, err := tx.Exec(`INSERT INTO ex_fts(rowid,content) VALUES(?,?)`, rowID, content); err != nil {
				return imported, err
			}
		}
		imported++
	}
	if err := tx.Commit(); err != nil {
		return imported, err
	}
	return imported, nil
}

func uniqueArchiveHosts(hosts []string) []string {
	seen := make(map[string]struct{}, len(hosts))
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			continue
		}
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	sort.Strings(out)
	return out
}
