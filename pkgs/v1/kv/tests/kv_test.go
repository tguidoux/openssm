package kv_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tguidoux/openssm/pkgs/v1/kv"
	kvbadger "github.com/tguidoux/openssm/pkgs/v1/kv/badger"
	"github.com/tguidoux/openssm/pkgs/v1/kv/kvtest"
	"github.com/tguidoux/openssm/pkgs/v1/kv/memory"
	kvpostgres "github.com/tguidoux/openssm/pkgs/v1/kv/postgres"
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

func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping postgres kv conformance test")
	}
	kvtest.Run(t, func(t *testing.T) kv.Store {
		s, db, err := kvpostgres.Open(dsn)
		require.NoError(t, err)
		t.Cleanup(func() { db.Close() })
		return s
	})
}
