// Package kvtest provides a conformance suite that every kv.Store
// implementation must pass, plus helpers to build stores for domain tests.
package kvtest

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tguidoux/openssm/pkgs/v1/kv"
)

// Run executes the conformance suite against stores built by newStore.
func Run(t *testing.T, newStore func(t *testing.T) kv.Store) {
	t.Run("GetMissing", func(t *testing.T) {
		s := newStore(t)
		err := s.View(func(r kv.Reader) error {
			_, err := r.Get("b", "missing")
			return err
		})
		assert.ErrorIs(t, err, kv.ErrNotFound)
	})

	t.Run("PutGetDelete", func(t *testing.T) {
		s := newStore(t)
		require.NoError(t, s.Update(func(tx kv.Tx) error { return tx.Put("b", "k", []byte("v")) }))
		var got []byte
		require.NoError(t, s.View(func(r kv.Reader) error {
			var err error
			got, err = r.Get("b", "k")
			return err
		}))
		assert.Equal(t, "v", string(got))

		require.NoError(t, s.Update(func(tx kv.Tx) error { return tx.Delete("b", "k") }))
		err := s.View(func(r kv.Reader) error { _, err := r.Get("b", "k"); return err })
		assert.ErrorIs(t, err, kv.ErrNotFound)
	})

	t.Run("BucketsAreIsolated", func(t *testing.T) {
		s := newStore(t)
		require.NoError(t, s.Update(func(tx kv.Tx) error { return tx.Put("a", "k", []byte("1")) }))
		err := s.View(func(r kv.Reader) error { _, err := r.Get("b", "k"); return err })
		assert.ErrorIs(t, err, kv.ErrNotFound)
	})

	t.Run("ListPrefixSorted", func(t *testing.T) {
		s := newStore(t)
		require.NoError(t, s.Update(func(tx kv.Tx) error {
			for _, k := range []string{"/app/c", "/app/a", "/other", "/app/b/x"} {
				if err := tx.Put("p", k, []byte(k)); err != nil {
					return err
				}
			}
			return tx.Put("q", "/app/z", []byte("z"))
		}))
		var entries []kv.Entry
		require.NoError(t, s.View(func(r kv.Reader) error {
			var err error
			entries, err = r.List("p", "/app/")
			return err
		}))
		keys := make([]string, len(entries))
		for i, e := range entries {
			keys[i] = e.Key
			assert.Equal(t, e.Key, string(e.Value))
		}
		assert.Equal(t, []string{"/app/a", "/app/b/x", "/app/c"}, keys)
	})

	t.Run("RollbackOnError", func(t *testing.T) {
		s := newStore(t)
		require.NoError(t, s.Update(func(tx kv.Tx) error { return tx.Put("b", "keep", []byte("1")) }))
		boom := errors.New("boom")
		err := s.Update(func(tx kv.Tx) error {
			if err := tx.Put("b", "new", []byte("2")); err != nil {
				return err
			}
			if err := tx.Delete("b", "keep"); err != nil {
				return err
			}
			return boom
		})
		assert.ErrorIs(t, err, boom)
		require.NoError(t, s.View(func(r kv.Reader) error {
			_, err := r.Get("b", "keep")
			assert.NoError(t, err)
			_, err = r.Get("b", "new")
			assert.ErrorIs(t, err, kv.ErrNotFound)
			return nil
		}))
	})

	t.Run("ReadYourWritesInTx", func(t *testing.T) {
		s := newStore(t)
		require.NoError(t, s.Update(func(tx kv.Tx) error {
			require.NoError(t, tx.Put("b", "x/1", []byte("1")))
			v, err := tx.Get("b", "x/1")
			require.NoError(t, err)
			assert.Equal(t, "1", string(v))
			entries, err := tx.List("b", "x/")
			require.NoError(t, err)
			assert.Len(t, entries, 1)
			require.NoError(t, tx.Delete("b", "x/1"))
			_, err = tx.Get("b", "x/1")
			assert.ErrorIs(t, err, kv.ErrNotFound)
			return nil
		}))
	})

	t.Run("ConcurrentUpdates", func(t *testing.T) {
		s := newStore(t)
		const n = 20
		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				assert.NoError(t, s.Update(func(tx kv.Tx) error {
					return tx.Put("c", fmt.Sprintf("k%02d", i), []byte("v"))
				}))
			}(i)
		}
		wg.Wait()
		require.NoError(t, s.View(func(r kv.Reader) error {
			entries, err := r.List("c", "")
			assert.Len(t, entries, n)
			return err
		}))
	})
}
