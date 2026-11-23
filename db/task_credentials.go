package db

import (
	"context"
	"errors"
	"strings"
	"time"
)

// TaskCredential is a per-task test account (#3): either declared by the user at
// task creation (source=user, e.g. a program-provided alias account) or registered
// by the agent itself and recorded so it is not forgotten (source=agent).
//
// Passwords are encrypted at rest (secrets.go) and masked by the HTTP API; the
// model-facing queries return only TaskCredentialRef metadata.
type TaskCredential struct {
	ID        int64     `json:"id"`
	TaskID    int64     `json:"task_id"`
	Domain    string    `json:"domain"`
	Username  string    `json:"username"`
	Password  string    `json:"password"`
	Source    string    `json:"source"` // "user" | "agent"
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TaskCredentialInput validates and normalizes one upsert. Empty username or
// domain is rejected. An operator can store an account with no password.
type TaskCredentialInput struct {
	Domain   string
	Username string
	Password string
	Source   string
	Note     string
}

var ErrTaskCredentialInvalid = errors.New("credential domain and username are required")

func (in TaskCredentialInput) normalize() (TaskCredentialInput, error) {
	in.Domain = strings.TrimSpace(in.Domain)
	in.Username = strings.TrimSpace(in.Username)
	in.Password = strings.TrimSpace(in.Password)
	in.Note = strings.TrimSpace(in.Note)
	if in.Source != "agent" {
		in.Source = "user"
	}
	if in.Domain == "" || in.Username == "" {
		return in, ErrTaskCredentialInvalid
	}
	if in.Password == "" && in.Source == "agent" {
		return in, errors.New("agent-recorded credentials need the password that was set")
	}
	return in, nil
}

// UpsertTaskCredential inserts or updates the (task, domain, username) entry: an
// agent re-registering the same account overwrites the password in place instead
// of piling up a second row.
func (d *DB) UpsertTaskCredential(taskID int64, in TaskCredentialInput) (TaskCredential, error) {
	in, err := in.normalize()
	if err != nil {
		return TaskCredential{}, err
	}
	pass, err := sealSecret(colCredPass, in.Password)
	if err != nil {
		return TaskCredential{}, err
	}
	var c TaskCredential
	err = d.QueryRow(`
		INSERT INTO task_credentials (task_id, domain, username, password, source, note)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (task_id, domain, username) DO UPDATE
		SET password = EXCLUDED.password,
		    source   = EXCLUDED.source,
		    note     = CASE WHEN EXCLUDED.note = '' THEN task_credentials.note ELSE EXCLUDED.note END,
		    updated_at = now()
		RETURNING id, task_id, domain, username, password, source, note, created_at, updated_at`,
		taskID, in.Domain, in.Username, pass, in.Source, in.Note).
		Scan(&c.ID, &c.TaskID, &c.Domain, &c.Username, &c.Password, &c.Source, &c.Note, &c.CreatedAt, &c.UpdatedAt)
	c.Password = in.Password
	return c, err
}

// TaskCredentials lists a task's credentials, oldest first (the user-declared
// ones keep their position ahead of agent-discovered ones).
func (d *DB) TaskCredentials(taskID int64) ([]TaskCredential, error) {
	rows, err := d.Query(`
		SELECT id, task_id, domain, username, password, source, note, created_at, updated_at
		FROM task_credentials WHERE task_id = $1 ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskCredential
	for rows.Next() {
		var c TaskCredential
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Domain, &c.Username, &c.Password, &c.Source, &c.Note, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		if c.Password, err = openSecret(colCredPass, c.Password); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteTaskCredential removes one entry; taskID scopes the delete so a stale
// request cannot cross tasks.
func (d *DB) DeleteTaskCredential(taskID, id int64) error {
	tag, err := d.Exec(`DELETE FROM task_credentials WHERE id = $1 AND task_id = $2`, id, taskID)
	if err != nil {
		return err
	}
	if n, _ := tag.RowsAffected(); n == 0 {
		return errors.New("credential not found")
	}
	return nil
}

// TaskCredentialRef is the only representation of a saved account sent to a model.
// Usernames, domains and notes are free text and may themselves contain secrets.
type TaskCredentialRef struct {
	ID          int64 `json:"id"`
	HasPassword bool  `json:"has_password"`
}

func (s *ExplorationStore) TaskCredentialRefs(taskID int64) ([]TaskCredentialRef, error) {
	rows, err := s.db.Query(`SELECT id, password <> '' FROM task_credentials WHERE task_id=$1 ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskCredentialRef{}
	for rows.Next() {
		var ref TaskCredentialRef
		if err := rows.Scan(&ref.ID, &ref.HasPassword); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// RecordTaskAccount records metadata only. A repeated observation preserves the
// operator's password, notes and provenance under the same unique key.
func (s *ExplorationStore) RecordTaskAccount(taskID int64, domain, username string) (TaskCredentialRef, error) {
	domain, username = strings.TrimSpace(domain), strings.TrimSpace(username)
	if domain == "" || username == "" {
		return TaskCredentialRef{}, ErrTaskCredentialInvalid
	}
	var ref TaskCredentialRef
	err := s.db.QueryRow(`INSERT INTO task_credentials (task_id,domain,username,password,source)
VALUES ($1,$2,$3,'','agent') ON CONFLICT (task_id,domain,username)
DO UPDATE SET username=EXCLUDED.username RETURNING id, password <> ''`, taskID, domain, username).
		Scan(&ref.ID, &ref.HasPassword)
	return ref, err
}

// ModelPasswordPatterns reads only the current task's passwords for the local
// pre-transmission filter. Callers must never log or persist the returned text.
func (d *DB) ModelPasswordPatterns(ctx context.Context, taskIDs []int64) ([]string, error) {
	rows, err := d.QueryContext(ctx, `SELECT password FROM task_credentials WHERE task_id=ANY($1::bigint[]) AND password<>''`, taskIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		clear, err := openSecret(colCredPass, value)
		if err != nil {
			return nil, err
		}
		if clear != "" {
			out = append(out, clear)
		}
	}
	return out, rows.Err()
}
