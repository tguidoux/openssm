// Package memory provides an in-memory kv.Store. Data is lost on exit.
package memory

import (
	"bytes"
	"sort"
	"strings"
	"sync"

	"github.com/tguidoux/openssm/pkgs/v1/kv"
)

// Store is an in-memory kv.Store guarded by a single RWMutex.
type Store struct {
	mu      sync.RWMutex
	buckets map[string]map[string][]byte
}

// New creates an empty in-memory store.
func New() *Store {
	return &Store{buckets: make(map[string]map[string][]byte)}
}

// View implements kv.Store.
func (s *Store) View(fn func(r kv.Reader) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fn(&tx{store: s})
}

// Update implements kv.Store. Writes are buffered and applied only if fn
// succeeds, which gives rollback semantics without copying the whole map.
func (s *Store) Update(fn func(t kv.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := &tx{store: s, writes: make(map[string]map[string][]byte)}
	if err := fn(t); err != nil {
		return err
	}
	for bucket, keys := range t.writes {
		b := s.buckets[bucket]
		if b == nil {
			b = make(map[string][]byte)
			s.buckets[bucket] = b
		}
		for k, v := range keys {
			if v == nil {
				delete(b, k)
			} else {
				b[k] = v
			}
		}
	}
	return nil
}

// Close implements kv.Store.
func (s *Store) Close() error { return nil }

// tx overlays buffered writes (nil value = tombstone) on top of the store.
type tx struct {
	store  *Store
	writes map[string]map[string][]byte
}

func (t *tx) Get(bucket, key string) ([]byte, error) {
	if w, ok := t.writes[bucket]; ok {
		if v, ok := w[key]; ok {
			if v == nil {
				return nil, kv.ErrNotFound
			}
			return bytes.Clone(v), nil
		}
	}
	v, ok := t.store.buckets[bucket][key]
	if !ok {
		return nil, kv.ErrNotFound
	}
	return bytes.Clone(v), nil
}

func (t *tx) List(bucket, prefix string) ([]kv.Entry, error) {
	merged := make(map[string][]byte)
	for k, v := range t.store.buckets[bucket] {
		if strings.HasPrefix(k, prefix) {
			merged[k] = v
		}
	}
	for k, v := range t.writes[bucket] {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if v == nil {
			delete(merged, k)
		} else {
			merged[k] = v
		}
	}
	out := make([]kv.Entry, 0, len(merged))
	for k, v := range merged {
		out = append(out, kv.Entry{Key: k, Value: bytes.Clone(v)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (t *tx) Put(bucket, key string, value []byte) error {
	w := t.writes[bucket]
	if w == nil {
		w = make(map[string][]byte)
		t.writes[bucket] = w
	}
	if value == nil {
		value = []byte{}
	}
	w[key] = bytes.Clone(value)
	return nil
}

func (t *tx) Delete(bucket, key string) error {
	w := t.writes[bucket]
	if w == nil {
		w = make(map[string][]byte)
		t.writes[bucket] = w
	}
	w[key] = nil
	return nil
}
