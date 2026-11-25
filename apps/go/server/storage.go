package main

import (
	"fmt"

	"github.com/tguidoux/openssm/pkgs/v1/credentials"
	"github.com/tguidoux/openssm/pkgs/v1/kv"
	kvbadger "github.com/tguidoux/openssm/pkgs/v1/kv/badger"
	"github.com/tguidoux/openssm/pkgs/v1/kv/memory"
	kvsqlite "github.com/tguidoux/openssm/pkgs/v1/kv/sqlite"
	_ "modernc.org/sqlite"
)

// storage bundles the kv store and credential store for the configured
// backend. Close releases the underlying database.
type storage struct {
	kv          kv.Store
	credentials credentials.CredentialStore
	close       func() error
}

// openStorage opens the backend selected by cfg.StorageType. All SSM domain
// services share the returned kv store; credentials live in the same database.
func openStorage(cfg SSMConfig) (*storage, error) {
	switch cfg.StorageType {
	case "sqlite":
		store, db, err := kvsqlite.Open(cfg.SQLitePath)
		if err != nil {
			return nil, err
		}
		creds, err := credentials.NewSQLiteCredentialStore(db)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("creating SQLite credential store: %w", err)
		}
		return &storage{kv: store, credentials: creds, close: db.Close}, nil

	case "badger":
		store, err := kvbadger.Open(cfg.BadgerPath)
		if err != nil {
			return nil, err
		}
		return &storage{kv: store, credentials: credentials.NewBadgerCredentialStore(store.DB()), close: store.Close}, nil

	default:
		return &storage{kv: memory.New(), credentials: credentials.NewMemoryCredentialStore(), close: func() error { return nil }}, nil
	}
}
