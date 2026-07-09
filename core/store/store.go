// Package store persists raw activity samples durably so history survives across
// daemon restarts and reboots.
//
// It uses modernc.org/sqlite (pure-Go, no cgo) in WAL mode, so a running daemon
// appending samples and an ad-hoc `report` reading them can touch the DB
// concurrently. Only content-free samples (timestamp + state) are stored.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/nlink-jp/active-lens/core/activity"
)

// Store is the persistence boundary.
type Store interface {
	// Append records one sample.
	Append(s activity.Sample) error
	// Query returns samples with ts in [since, until], ordered ascending. A zero
	// since or until means unbounded on that end.
	Query(since, until time.Time) ([]activity.Sample, error)
	// Last returns the most recently recorded sample, if any.
	Last() (sample activity.Sample, ok bool, err error)
	Close() error
}

const schema = `
CREATE TABLE IF NOT EXISTS samples (
  ts    INTEGER NOT NULL,
  state TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_samples_ts ON samples(ts);
`

type sqliteStore struct {
	db *sql.DB
}

// Open opens (creating if needed) the sample DB at path. The parent dir is
// created 0700 and the DB file kept 0600 — the timestamps are personal.
func Open(path string) (Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	// Best-effort owner-only file perms (no-op semantics differ on Windows, but
	// this tool is darwin-only).
	_ = os.Chmod(path, 0o600)
	return &sqliteStore{db: db}, nil
}

func (s *sqliteStore) Append(sample activity.Sample) error {
	_, err := s.db.Exec(`INSERT INTO samples(ts, state) VALUES(?, ?)`,
		sample.TS.Unix(), string(sample.State))
	return err
}

func (s *sqliteStore) Query(since, until time.Time) ([]activity.Sample, error) {
	q := `SELECT ts, state FROM samples`
	var args []any
	var where string
	if !since.IsZero() {
		where = " WHERE ts >= ?"
		args = append(args, since.Unix())
	}
	if !until.IsZero() {
		if where == "" {
			where = " WHERE ts <= ?"
		} else {
			where += " AND ts <= ?"
		}
		args = append(args, until.Unix())
	}
	q += where + " ORDER BY ts ASC"

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []activity.Sample
	for rows.Next() {
		var unix int64
		var state string
		if err := rows.Scan(&unix, &state); err != nil {
			return nil, err
		}
		out = append(out, activity.Sample{TS: time.Unix(unix, 0), State: activity.State(state)})
	}
	return out, rows.Err()
}

func (s *sqliteStore) Last() (activity.Sample, bool, error) {
	var unix int64
	var state string
	err := s.db.QueryRow(`SELECT ts, state FROM samples ORDER BY ts DESC LIMIT 1`).Scan(&unix, &state)
	if err == sql.ErrNoRows {
		return activity.Sample{}, false, nil
	}
	if err != nil {
		return activity.Sample{}, false, err
	}
	return activity.Sample{TS: time.Unix(unix, 0), State: activity.State(state)}, true, nil
}

func (s *sqliteStore) Close() error { return s.db.Close() }
