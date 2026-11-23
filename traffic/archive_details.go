package traffic

import "database/sql"

// These rows keep the raw stored bytes. The UI preview may be truncated or
// base64-rendered and is not a lossless archive representation.
type archiveStreamRow struct {
	Seq, TS        int64
	Direction      string
	Kind           int
	Event, EventID string
	Retry, Size    int
	Payload        []byte
	Blob           string
	Truncated      bool
}

type archiveRewriteRow struct {
	Seq                         int
	Phase                       string
	RuleID                      *int64
	RuleSnapshot, Action, Field *string
	Outcome, Reason             string
	BeforeHash, AfterHash       *string
	Inline                      []byte
	Blob                        string
}

func (t *Traffic) exportDetails(e *ArchiveExchange) error {
	rows, err := t.db.Query(`SELECT seq,ts,direction,kind,event,event_id,retry,size,payload,COALESCE(payload_blob,''),truncated
		FROM stream_messages WHERE exchange_id=? ORDER BY seq`, e.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r archiveStreamRow
		if err := rows.Scan(&r.Seq, &r.TS, &r.Direction, &r.Kind, &r.Event, &r.EventID, &r.Retry, &r.Size, &r.Payload, &r.Blob, &r.Truncated); err != nil {
			return err
		}
		e.Stream = append(e.Stream, r)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	traces, err := t.db.Query(`SELECT seq,phase,rule_id,rule_snapshot,action,field,outcome,reason,before_hash,after_hash,after_inline,COALESCE(after_blob,'')
		FROM exchange_rewrites WHERE exchange_id=? ORDER BY seq`, e.ID)
	if err != nil {
		return err
	}
	defer traces.Close()
	for traces.Next() {
		var r archiveRewriteRow
		if err := traces.Scan(&r.Seq, &r.Phase, &r.RuleID, &r.RuleSnapshot, &r.Action, &r.Field, &r.Outcome, &r.Reason, &r.BeforeHash, &r.AfterHash, &r.Inline, &r.Blob); err != nil {
			return err
		}
		e.Rewrites = append(e.Rewrites, r)
	}
	return traces.Err()
}

func (e ArchiveExchange) blobHashes() []string {
	out := []string{e.ReqBlob, e.RespBlob}
	for _, r := range e.Stream {
		out = append(out, r.Blob)
	}
	for _, r := range e.Rewrites {
		out = append(out, r.Blob)
	}
	return out
}

func (e ArchiveExchange) importDetails(tx *sql.Tx) error {
	for _, r := range e.Stream {
		if _, err := tx.Exec(`INSERT INTO stream_messages(exchange_id,seq,ts,direction,kind,event,event_id,retry,size,payload,payload_blob,truncated)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, e.ID, r.Seq, r.TS, r.Direction, r.Kind, r.Event, r.EventID, r.Retry, r.Size, r.Payload, nullIfEmpty(r.Blob), r.Truncated); err != nil {
			return err
		}
	}
	for _, r := range e.Rewrites {
		if _, err := tx.Exec(`INSERT INTO exchange_rewrites(exchange_id,seq,phase,rule_id,rule_snapshot,action,field,outcome,reason,before_hash,after_hash,after_inline,after_blob)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, e.ID, r.Seq, r.Phase, r.RuleID, r.RuleSnapshot, r.Action, r.Field, r.Outcome, r.Reason, r.BeforeHash, r.AfterHash, r.Inline, nullIfEmpty(r.Blob)); err != nil {
			return err
		}
	}
	return nil
}
