// Package badger provides a BadgerDB-backed kv.Store.
package badger

import (
	"errors"
	"fmt"

	"github.com/dgraph-io/badger/v4"
	"github.com/tguidoux/openssm/pkgs/v1/kv"
)

// keyPrefix namespaces kv data so other components (e.g. credentials) can
// share the same Badger database.
const keyPrefix = "kv:"

// Store is a kv.Store persisted in BadgerDB.
type Store struct {
	db   *badger.DB
	owns bool
}

// Open opens (or creates) a Badger database in dir. The returned store owns
// the database and closes it on Close.
func Open(dir string) (*Store, error) {
	opts := badger.DefaultOptions(dir).WithLogger(nil).WithCompression(0)
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("opening badger database %s: %w", dir, err)
	}
	return &Store{db: db, owns: true}, nil
}

// New wraps an existing Badger database. Close does not close db.
func New(db *badger.DB) *Store {
	return &Store{db: db}
}

// DB returns the underlying Badger database for sharing.
func (s *Store) DB() *badger.DB { return s.db }

// View implements kv.Store.
func (s *Store) View(fn func(r kv.Reader) error) error {
	return s.db.View(func(txn *badger.Txn) error {
		return fn(&tx{txn: txn})
	})
}

// Update implements kv.Store. Conflicting concurrent transactions are
// retried a few times, matching Badger's optimistic concurrency model.
func (s *Store) Update(fn func(t kv.Tx) error) error {
	var err error
	for range 5 {
		err = s.db.Update(func(txn *badger.Txn) error {
			return fn(&tx{txn: txn})
		})
		if !errors.Is(err, badger.ErrConflict) {
			return err
		}
	}
	return err
}

// Close implements kv.Store.
func (s *Store) Close() error {
	if s.owns {
		return s.db.Close()
	}
	return nil
}

func fullKey(bucket, key string) []byte {
	return []byte(keyPrefix + bucket + "\x00" + key)
}

type tx struct {
	txn *badger.Txn
}

func (t *tx) Get(bucket, key string) ([]byte, error) {
	item, err := t.txn.Get(fullKey(bucket, key))
	if errors.Is(err, badger.ErrKeyNotFound) {
		return nil, kv.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s/%s: %w", bucket, key, err)
	}
	return item.ValueCopy(nil)
}

func (t *tx) List(bucket, prefix string) ([]kv.Entry, error) {
	bucketPrefix := []byte(keyPrefix + bucket + "\x00")
	scan := append(append([]byte{}, bucketPrefix...), prefix...)
	it := t.txn.NewIterator(badger.IteratorOptions{Prefix: scan, PrefetchValues: true, PrefetchSize: 100})
	defer it.Close()
	var out []kv.Entry
	for it.Seek(scan); it.ValidForPrefix(scan); it.Next() {
		item := it.Item()
		v, err := item.ValueCopy(nil)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", bucket, err)
		}
		out = append(out, kv.Entry{Key: string(item.Key()[len(bucketPrefix):]), Value: v})
	}
	return out, nil
}

func (t *tx) Put(bucket, key string, value []byte) error {
	if value == nil {
		value = []byte{}
	}
	return t.txn.Set(fullKey(bucket, key), value)
}

func (t *tx) Delete(bucket, key string) error {
	return t.txn.Delete(fullKey(bucket, key))
}
