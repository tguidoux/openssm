package kv_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tguidoux/openssm/pkgs/v1/kv"
	kvbadger "github.com/tguidoux/openssm/pkgs/v1/kv/badger"
	"github.com/tguidoux/openssm/pkgs/v1/kv/kvtest"
	"github.com/tguidoux/openssm/pkgs/v1/kv/memory"
	kvsqlite "github.com/tguidoux/openssm/pkgs/v1/kv/sqlite"
	_ "modernc.org/sqlite"
)

func TestMemoryStore(t *testing.T) {
	kvtest.Run(t, func(t *testing.T) kv.Store { return memory.New() })
}

func TestSQLiteStore(t *testing.T) {
	kvtest.Run(t, func(t *testing.T) kv.Store {
		s, db, err := kvsqlite.Open(filepath.Join(t.TempDir(), "kv.db"))
		require.NoError(t, err)
		t.Cleanup(func() { db.Close() })
		return s
	})
}

func TestBadgerStore(t *testing.T) {
	kvtest.Run(t, func(t *testing.T) kv.Store {
		s, err := kvbadger.Open(t.TempDir())
		require.NoError(t, err)
		t.Cleanup(func() { s.Close() })
		return s
	})
}
