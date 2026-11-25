// Package postgres provides a PostgreSQL-backed kv.Store using a single table.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/tguidoux/openssm/pkgs/v1/kv"
)

// Store is a kv.Store persisted in a PostgreSQL "kv" table.
// Write transactions are serialized with a mutex so Update behaves like the
// SQLite backend; reads run concurrently through the pgx connection pool.
type Store struct {
	db      *sql.DB
	writeMu sync.Mutex
}

// Open opens (or creates) a PostgreSQL database at dsn, verifies the
// connection, and returns both the kv store and the underlying *sql.DB so
// other components (e.g. credentials) can share the connection.
func Open(dsn string) (*Store, *sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("opening postgres database: %w", err)
	}
	s, err := New(db)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return s, db, nil
}

// New wraps an existing *sql.DB and creates the kv table if needed.
//
// Keys are stored as BYTEA, not TEXT: domain packages use NUL bytes as key
// separators, which PostgreSQL TEXT cannot hold, and BYTEA comparisons are
// memcmp-based, matching the SQLite backend's BINARY semantics without
// depending on the database collation.
func New(db *sql.DB) (*Store, error) {
	const schema = `CREATE TABLE IF NOT EXISTS kv (
		bucket TEXT NOT NULL,
		key    BYTEA NOT NULL,
		value  BYTEA NOT NULL,
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
	ctx := context.Background()
	sqlTx, err := s.db.BeginTx(ctx, nil)
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
	err := r.q.QueryRow(`SELECT value FROM kv WHERE bucket = $1 AND key = $2`, bucket, []byte(key)).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, kv.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s/%s: %w", bucket, key, err)
	}
	return v, nil
}

// List uses a byte-range scan (key >= prefix AND key < prefixEnd); BYTEA
// comparison is memcmp-based, so keys containing NUL bytes — used as
// separators by domain packages — behave exactly like the SQLite backend.
func (r *reader) List(bucket, prefix string) ([]kv.Entry, error) {
	query := `SELECT key, value FROM kv WHERE bucket = $1 AND key >= $2 ORDER BY key`
	args := []any{bucket, []byte(prefix)}
	if end, ok := prefixEnd(prefix); ok {
		query = `SELECT key, value FROM kv WHERE bucket = $1 AND key >= $2 AND key < $3 ORDER BY key`
		args = append(args, []byte(end))
	}
	rows, err := r.q.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing %s/%s: %w", bucket, prefix, err)
	}
	defer rows.Close()
	var out []kv.Entry
	for rows.Next() {
		var e kv.Entry
		var key []byte
		if err := rows.Scan(&key, &e.Value); err != nil {
			return nil, fmt.Errorf("scanning %s: %w", bucket, err)
		}
		e.Key = string(key)
		out = append(out, e)
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
		`INSERT INTO kv (bucket, key, value) VALUES ($1, $2, $3)
		 ON CONFLICT(bucket, key) DO UPDATE SET value = excluded.value`,
		bucket, []byte(key), value)
	if err != nil {
		return fmt.Errorf("writing %s/%s: %w", bucket, key, err)
	}
	return nil
}

func (t *tx) Delete(bucket, key string) error {
	if _, err := t.tx.Exec(`DELETE FROM kv WHERE bucket = $1 AND key = $2`, bucket, []byte(key)); err != nil {
		return fmt.Errorf("deleting %s/%s: %w", bucket, key, err)
	}
	return nil
}

// prefixEnd returns the smallest string greater than every string with the
// given prefix, or false when no such bound exists (empty or all 0xff).
func prefixEnd(prefix string) (string, bool) {
	b := []byte(prefix)
	i := len(b) - 1
	for i >= 0 && b[i] == 0xff {
		i--
	}
	if i < 0 {
		return "", false
	}
	b[i]++
	return string(b[:i+1]), true
}
