// Package kv defines a minimal transactional key-value abstraction used by
// every OpenSSM domain service. Keys are grouped into buckets; values are
// opaque bytes (domain packages store JSON). Implementations live in the
// memory, sqlite, and badger sub-packages and share a conformance suite in
// kvtest.
package kv

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrNotFound is returned by Get when the key does not exist.
var ErrNotFound = errors.New("kv: key not found")

// Entry is a single key/value pair returned by List.
type Entry struct {
	Key   string
	Value []byte
}

// Reader exposes read operations available in every transaction.
type Reader interface {
	// Get returns the value stored under bucket/key or ErrNotFound.
	Get(bucket, key string) ([]byte, error)
	// List returns all entries in bucket whose key starts with prefix,
	// sorted by key in ascending byte order.
	List(bucket, prefix string) ([]Entry, error)
}

// Tx is a read-write transaction.
type Tx interface {
	Reader
	// Put stores value under bucket/key, replacing any existing value.
	Put(bucket, key string, value []byte) error
	// Delete removes bucket/key. Deleting a missing key is not an error.
	Delete(bucket, key string) error
}

// Store is a transactional key-value store.
// Implementations must be safe for concurrent use. Update transactions are
// atomic: either all writes are applied or none are.
type Store interface {
	// View runs fn in a read-only transaction.
	View(fn func(r Reader) error) error
	// Update runs fn in a read-write transaction. If fn returns an error the
	// transaction is rolled back and the error is returned.
	Update(fn func(tx Tx) error) error
	// Close releases resources held by the store.
	Close() error
}

// GetJSON loads bucket/key and unmarshals it into out.
func GetJSON(r Reader, bucket, key string, out any) error {
	data, err := r.Get(bucket, key)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding %s/%s: %w", bucket, key, err)
	}
	return nil
}

// PutJSON marshals v and stores it under bucket/key.
func PutJSON(tx Tx, bucket, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding %s/%s: %w", bucket, key, err)
	}
	return tx.Put(bucket, key, data)
}

// ListJSON decodes every entry under bucket/prefix using newFn to allocate
// the destination value.
func ListJSON[T any](r Reader, bucket, prefix string) ([]T, error) {
	entries, err := r.List(bucket, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(entries))
	for _, e := range entries {
		var v T
		if err := json.Unmarshal(e.Value, &v); err != nil {
			return nil, fmt.Errorf("decoding %s/%s: %w", bucket, e.Key, err)
		}
		out = append(out, v)
	}
	return out, nil
}
