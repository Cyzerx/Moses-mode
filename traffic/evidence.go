package traffic

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// EvidenceExchange exposes complete captured bodies only during the callback.
// Readers are closed before releasing the writer lock, so host deletion cannot
// remove a blob between its index lookup and its evidence copy.
type EvidenceExchange struct {
	ID, URL, Method, ContentType string
	TS                           int64
	Status                       int
	ReqHead, RespHead            string
	ReqLen, RespLen              int64
	ReqHash, RespHash            string
	Request, Response            io.Reader
}

// checkTaskAccess is called under the traffic writer lock so that a row cannot
// be archived or replaced between authorization and reading its body.
func (t *Traffic) checkTaskAccess(ctx context.Context, taskID int64, ref string, blob bool) error {
	if taskID <= 0 {
		return sql.ErrNoRows
	}
	q := `SELECT 1 FROM exchanges WHERE task_id=? AND id=?`
	if blob {
		// Body and stream references are explicit. Internal rewrite-trace blobs
		// share storage but may contain sensitive changes hidden by traffic_get.
		q = `SELECT 1 FROM exchanges e JOIN exchange_bodies b ON b.id=e.id
			WHERE e.task_id=?1 AND (b.req_blob=?2 OR b.resp_blob=?2)
			UNION ALL SELECT 1 FROM exchanges e JOIN stream_messages m ON m.exchange_id=e.id
			WHERE e.task_id=?1 AND m.payload_blob=?2 LIMIT 1`
	}
	var present int
	return t.db.QueryRowContext(ctx, q, taskID, ref).Scan(&present)
}

// TaskExchange reads an authorized preview and stream metadata in one locked operation.
func (t *Traffic) TaskExchange(ctx context.Context, taskID int64, id string) (req, resp string, count int, kind string, err error) {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	if err = t.checkTaskAccess(ctx, taskID, id, false); err != nil {
		return
	}
	if req, resp, err = t.Get(id); err == nil {
		count, kind, err = t.StreamInfo(id)
	}
	return
}

func (t *Traffic) ReadEvidence(ctx context.Context, taskID int64, ids []string, consume func(EvidenceExchange) error) error {
	if t == nil {
		return errors.New("traffic recording store unavailable")
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := t.checkTaskAccess(ctx, taskID, id, false); err != nil {
			return fmt.Errorf("traffic %s: %w", id, err)
		}
		if err := t.readEvidence(id, consume); err != nil {
			return fmt.Errorf("traffic %s: %w", id, err)
		}
	}
	return nil
}

func (t *Traffic) readEvidence(id string, consume func(EvidenceExchange) error) error {
	e := EvidenceExchange{ID: id}
	var legacy string
	if err := t.db.QueryRow(`SELECT ts,url,method,status,content_type,req_len,resp_len,path FROM exchanges WHERE id=?`, id).
		Scan(&e.TS, &e.URL, &e.Method, &e.Status, &e.ContentType, &e.ReqLen, &e.RespLen, &legacy); err != nil {
		return err
	}
	var req, resp []byte
	var reqBlob, respBlob sql.NullString
	err := t.db.QueryRow(`SELECT req_head,req_body,req_blob,resp_head,resp_body,resp_blob FROM exchange_bodies WHERE id=?`, id).
		Scan(&e.ReqHead, &req, &reqBlob, &e.RespHead, &resp, &respBlob)
	if errors.Is(err, sql.ErrNoRows) && legacy != "" {
		dir, err := t.legacyExchangeDirectory(legacy)
		if err != nil {
			return err
		}
		r, err := os.Open(filepath.Join(dir, "request.http"))
		if err != nil {
			return err
		}
		defer r.Close()
		p, err := os.Open(filepath.Join(dir, "response.http"))
		if err != nil {
			return err
		}
		defer p.Close()
		e.ReqHead, e.Request, err = splitLegacyEvidence(r)
		if err != nil {
			return err
		}
		e.RespHead, e.Response, err = splitLegacyEvidence(p)
		if err != nil {
			return err
		}
		return consume(e)
	}
	if err != nil {
		return err
	}
	e.Request = bytes.NewReader(req)
	e.Response = bytes.NewReader(resp)
	if reqBlob.Valid && reqBlob.String != "" {
		f, _, err := t.Blob(reqBlob.String)
		if err != nil {
			return err
		}
		defer f.Close()
		e.Request = f
		e.ReqHash = reqBlob.String
	}
	if respBlob.Valid && respBlob.String != "" {
		f, _, err := t.Blob(respBlob.String)
		if err != nil {
			return err
		}
		defer f.Close()
		e.Response = f
		e.RespHash = respBlob.String
	}
	return consume(e)
}

func splitLegacyEvidence(r io.Reader) (string, io.Reader, error) {
	b := bufio.NewReader(r)
	var head strings.Builder
	for {
		line, err := b.ReadString('\n')
		if line == "\n" || line == "\r\n" {
			return head.String(), b, nil
		}
		head.WriteString(line)
		if head.Len() > 1<<20 {
			return "", nil, errors.New("legacy traffic header too large")
		}
		if errors.Is(err, io.EOF) {
			return head.String(), b, nil
		}
		if err != nil {
			return "", nil, err
		}
	}
}
