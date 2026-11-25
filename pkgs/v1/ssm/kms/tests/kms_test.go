package kms_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tguidoux/openssm/pkgs/v1/kv"
	"github.com/tguidoux/openssm/pkgs/v1/kv/memory"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/kms"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

var account = types.Account{AccountID: "123456789012", Region: "us-east-1"}

func newService(t *testing.T, opts kms.Options) *kms.Service {
	t.Helper()
	if opts.MasterKey == "" {
		opts.MasterKey = "master"
	}
	s, err := kms.New(memory.New(), account, opts)
	require.NoError(t, err)
	return s
}

func TestNewRequiresMasterKey(t *testing.T) {
	_, err := kms.New(memory.New(), account, kms.Options{})
	assert.Error(t, err)
}

func TestDefaultAliasExists(t *testing.T) {
	s := newService(t, kms.Options{})
	id, err := s.ResolveKeyID("")
	require.NoError(t, err)
	id2, err := s.ResolveKeyID(kms.DefaultSSMAlias)
	require.NoError(t, err)
	assert.Equal(t, id, id2)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	s := newService(t, kms.Options{})
	ct, err := s.Encrypt("", []byte("secret"), []byte("/app/pw"))
	require.NoError(t, err)
	assert.NotContains(t, ct, "secret")

	pt, err := s.Decrypt(ct, []byte("/app/pw"))
	require.NoError(t, err)
	assert.Equal(t, "secret", string(pt))
}

func TestDecryptWithWrongAADFails(t *testing.T) {
	s := newService(t, kms.Options{})
	ct, err := s.Encrypt("", []byte("secret"), []byte("/a"))
	require.NoError(t, err)
	_, err = s.Decrypt(ct, []byte("/b"))
	assert.Error(t, err)
}

func TestKeyReferenceForms(t *testing.T) {
	s := newService(t, kms.Options{})
	k, err := s.CreateKey("app key", "alias/app")
	require.NoError(t, err)

	for _, ref := range []string{k.KeyID, k.ARN, "alias/app", account.AliasARN("alias/app")} {
		id, err := s.ResolveKeyID(ref)
		require.NoError(t, err, ref)
		assert.Equal(t, k.KeyID, id, ref)
	}
}

func TestUnknownKeyIsInvalidKeyID(t *testing.T) {
	s := newService(t, kms.Options{})
	for _, ref := range []string{"alias/missing", "00000000-0000-0000-0000-000000000000", "arn:aws:s3:::bucket"} {
		_, err := s.ResolveKeyID(ref)
		assert.True(t, types.IsCode(err, types.ErrInvalidKeyID), ref)
	}
}

func TestAutoCreateKeys(t *testing.T) {
	s := newService(t, kms.Options{AutoCreateKeys: true})
	id, err := s.ResolveKeyID("alias/auto")
	require.NoError(t, err)
	again, err := s.ResolveKeyID("alias/auto")
	require.NoError(t, err)
	assert.Equal(t, id, again)

	arnID := "11111111-2222-3333-4444-555555555555"
	got, err := s.ResolveKeyID("arn:aws:kms:us-east-1:123456789012:key/" + arnID)
	require.NoError(t, err)
	assert.Equal(t, arnID, got)
}

func TestCreateKeyValidation(t *testing.T) {
	s := newService(t, kms.Options{})
	_, err := s.CreateKey("", "alias/aws/reserved")
	assert.True(t, types.IsCode(err, types.ErrValidation))
	_, err = s.CreateKey("", "noprefix")
	assert.True(t, types.IsCode(err, types.ErrValidation))
	_, err = s.CreateKey("", "alias/dup")
	require.NoError(t, err)
	_, err = s.CreateKey("", "alias/dup")
	assert.True(t, types.IsCode(err, types.ErrValidation))
}

func TestMasterKeyMismatchCannotDecrypt(t *testing.T) {
	store := memory.New()
	s1, err := kms.New(store, account, kms.Options{MasterKey: "one"})
	require.NoError(t, err)
	ct, err := s1.Encrypt("", []byte("v"), nil)
	require.NoError(t, err)

	s2, err := kms.New(store, account, kms.Options{MasterKey: "two"})
	require.NoError(t, err)
	_, err = s2.Decrypt(ct, nil)
	assert.Error(t, err)
}

func TestListKeysIncludesAliases(t *testing.T) {
	s := newService(t, kms.Options{})
	_, err := s.CreateKey("x", "alias/x")
	require.NoError(t, err)
	keys, err := s.ListKeys()
	require.NoError(t, err)
	var aliases []string
	for _, k := range keys {
		aliases = append(aliases, k.Aliases...)
	}
	assert.ElementsMatch(t, []string{kms.DefaultSSMAlias, "alias/x"}, aliases)
}

func TestRolledBackAutoCreatedKeyIsNotReused(t *testing.T) {
	store := memory.New()
	s, err := kms.New(store, account, kms.Options{MasterKey: "master", AutoCreateKeys: true})
	require.NoError(t, err)
	const ref = "11111111-2222-3333-4444-555555555555"

	// Auto-create a key and encrypt with it, then roll the transaction back.
	rollback := errors.New("rollback")
	err = store.Update(func(tx kv.Tx) error {
		keyID, err := s.ResolveKeyIDTx(tx, ref)
		require.NoError(t, err)
		_, err = s.EncryptTx(tx, keyID, []byte("x"), nil)
		require.NoError(t, err)
		return rollback
	})
	require.ErrorIs(t, err, rollback)

	ct, err := s.Encrypt(ref, []byte("secret"), []byte("/a"))
	require.NoError(t, err)

	// A fresh service (as after a restart) must decrypt with committed material.
	s2, err := kms.New(store, account, kms.Options{MasterKey: "master"})
	require.NoError(t, err)
	pt, err := s2.Decrypt(ct, []byte("/a"))
	require.NoError(t, err)
	assert.Equal(t, "secret", string(pt))
}
