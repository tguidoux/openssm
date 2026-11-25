// Package sqlite provides a SQLite-backed kv.Store using a single table.
package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/tguidoux/openssm/pkgs/v1/kv"
)

// Store is a kv.Store persisted in a SQLite "kv" table.
// Write transactions are serialized with a mutex to avoid SQLITE_BUSY under
// concurrent writers; reads run concurrently thanks to WAL mode.
type Store struct {
	db      *sql.DB
	writeMu sync.Mutex
}

// Open opens (or creates) a SQLite database at path, enables WAL mode, and
// returns both the kv store and the underlying *sql.DB so other components
// (e.g. credentials) can share the connection.
func Open(path string) (*Store, *sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, nil, fmt.Errorf("opening sqlite database %s: %w", path, err)
	}
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000"} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, nil, fmt.Errorf("applying %q: %w", pragma, err)
		}
	}
	s, err := New(db)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return s, db, nil
}

// New wraps an existing *sql.DB and creates the kv table if needed.
func New(db *sql.DB) (*Store, error) {
	const schema = `CREATE TABLE IF NOT EXISTS kv (
		bucket TEXT NOT NULL,
		key    TEXT NOT NULL,
		value  BLOB NOT NULL,
		PRIMARY KEY (bucket, key)
	)`
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("creating kv table: %w", err)
	}
	return &Store{db: db}, nil
}

// View implements kv.Store.
func (s *Store) View(fn func(r kv.Reader) error) error {
	return fn(&reader{q: s.db})
}

// Update implements kv.Store.
func (s *Store) Update(fn func(t kv.Tx) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	sqlTx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	if err := fn(&tx{reader: reader{q: sqlTx}, tx: sqlTx}); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

// Close is a no-op: the *sql.DB is owned by the caller of Open/New.
func (s *Store) Close() error { return nil }

type querier interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

type reader struct {
	q querier
}

func (r *reader) Get(bucket, key string) ([]byte, error) {
	var v []byte
	err := r.q.QueryRow(`SELECT value FROM kv WHERE bucket = ? AND key = ?`, bucket, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, kv.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s/%s: %w", bucket, key, err)
	}
	return v, nil
}

func (r *reader) List(bucket, prefix string) ([]kv.Entry, error) {
	rows, err := r.q.Query(
		`SELECT key, value FROM kv WHERE bucket = ? AND substr(key, 1, ?) = ? ORDER BY key`,
		bucket, len(prefix), prefix)
	if err != nil {
		return nil, fmt.Errorf("listing %s/%s: %w", bucket, prefix, err)
	}
	defer rows.Close()
	var out []kv.Entry
	for rows.Next() {
		var e kv.Entry
		if err := rows.Scan(&e.Key, &e.Value); err != nil {
			return nil, fmt.Errorf("scanning %s: %w", bucket, err)
		}
		// substr() counts characters, not bytes; double-check the prefix.
		if strings.HasPrefix(e.Key, prefix) {
			out = append(out, e)
		}
	}
	return out, rows.Err()
}

type tx struct {
	reader
	tx *sql.Tx
}

func (t *tx) Put(bucket, key string, value []byte) error {
	if value == nil {
		value = []byte{}
	}
	_, err := t.tx.Exec(
		`INSERT INTO kv (bucket, key, value) VALUES (?, ?, ?)
		 ON CONFLICT(bucket, key) DO UPDATE SET value = excluded.value`,
		bucket, key, value)
	if err != nil {
		return fmt.Errorf("writing %s/%s: %w", bucket, key, err)
	}
	return nil
}

func (t *tx) Delete(bucket, key string) error {
	if _, err := t.tx.Exec(`DELETE FROM kv WHERE bucket = ? AND key = ?`, bucket, key); err != nil {
		return fmt.Errorf("deleting %s/%s: %w", bucket, key, err)
	}
	return nil
}
