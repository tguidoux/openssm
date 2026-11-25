// Package kms implements a minimal, local KMS replacement used to encrypt
// SecureString parameters. Each customer key ("CMK") holds random 256-bit
// key material that is wrapped with a server master key and persisted in the
// kv store. Values are sealed with AES-256-GCM.
//
// Keys are addressed exactly like AWS KMS: key ID (UUID), key ARN, alias name
// ("alias/aws/ssm"), or alias ARN. The AWS-managed alias "alias/aws/ssm" is
// created automatically.
package kms

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/id"
	"github.com/tguidoux/openssm/pkgs/v1/kv"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

const (
	// DefaultSSMAlias is the AWS-managed key alias used for SecureString
	// parameters when no KeyId is supplied.
	DefaultSSMAlias = "alias/aws/ssm"

	bucketKeys    = "kms_keys"
	bucketAliases = "kms_aliases"

	// ciphertextVersion prefixes every sealed blob so the format can evolve.
	ciphertextVersion byte = 1
	keyIDLength            = 36
)

// Key is the persisted metadata for a customer key.
type Key struct {
	KeyID       string    `json:"keyId"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	// WrappedMaterial is the key material sealed with the master key.
	WrappedMaterial []byte `json:"wrappedMaterial"`
}

// KeyInfo describes a key for listing (no key material).
type KeyInfo struct {
	KeyID       string
	ARN         string
	Description string
	Aliases     []string
	CreatedAt   time.Time
}

// Service manages keys and performs envelope encryption.
type Service struct {
	store      kv.Store
	account    types.Account
	master     cipher.AEAD
	autoCreate bool

	mu    sync.RWMutex
	cache map[string]cipher.AEAD
}

// Options configures the KMS service.
type Options struct {
	// MasterKey is any secret string; it is stretched with SHA-256 into the
	// 256-bit key that wraps all CMK material. Changing it makes existing
	// SecureString values unreadable.
	MasterKey string
	// AutoCreateKeys creates unknown keys on first use instead of returning
	// InvalidKeyId. Useful when clients pass real AWS key ARNs.
	AutoCreateKeys bool
}

// New creates the KMS service and ensures the AWS-managed SSM key exists.
func New(store kv.Store, account types.Account, opts Options) (*Service, error) {
	if opts.MasterKey == "" {
		return nil, errors.New("kms: master key is required")
	}
	sum := sha256.Sum256([]byte("openssm-kms-master:" + opts.MasterKey))
	master, err := newAEAD(sum[:])
	if err != nil {
		return nil, err
	}
	s := &Service{
		store:      store,
		account:    account,
		master:     master,
		autoCreate: opts.AutoCreateKeys,
		cache:      make(map[string]cipher.AEAD),
	}
	if _, err := s.ensureAlias(DefaultSSMAlias, "Default key that protects my SSM parameters when no other key is defined"); err != nil {
		return nil, err
	}
	return s, nil
}

// CreateKey creates a new key, optionally attaching an alias.
func (s *Service) CreateKey(description, alias string) (*KeyInfo, error) {
	if alias != "" {
		if err := validateAlias(alias); err != nil {
			return nil, err
		}
	}
	var created *Key
	err := s.store.Update(func(tx kv.Tx) error {
		if alias != "" {
			if _, err := tx.Get(bucketAliases, alias); err == nil {
				return types.NewValidationError("An alias with the name %s already exists", alias)
			}
		}
		k, err := s.newKey(tx, description)
		if err != nil {
			return err
		}
		created = k
		if alias != "" {
			return tx.Put(bucketAliases, alias, []byte(k.KeyID))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	info := s.info(created, nil)
	if alias != "" {
		info.Aliases = []string{alias}
	}
	return info, nil
}

// ListKeys returns all keys with their aliases.
func (s *Service) ListKeys() ([]*KeyInfo, error) {
	var out []*KeyInfo
	err := s.store.View(func(r kv.Reader) error {
		keys, err := kv.ListJSON[Key](r, bucketKeys, "")
		if err != nil {
			return err
		}
		aliases, err := r.List(bucketAliases, "")
		if err != nil {
			return err
		}
		byKey := make(map[string][]string)
		for _, a := range aliases {
			byKey[string(a.Value)] = append(byKey[string(a.Value)], a.Key)
		}
		for i := range keys {
			out = append(out, s.info(&keys[i], byKey[keys[i].KeyID]))
		}
		return nil
	})
	return out, err
}

// ResolveKeyID returns the canonical key ID for any key reference.
func (s *Service) ResolveKeyID(ref string) (string, error) {
	var keyID string
	err := s.store.Update(func(tx kv.Tx) error {
		var err error
		keyID, err = s.ResolveKeyIDTx(tx, ref)
		return err
	})
	return keyID, err
}

// ResolveKeyIDTx resolves ref inside the caller's write transaction, creating
// the key when AutoCreateKeys is enabled. Domain services must use the Tx
// variants so KMS reads/writes join their transaction (the memory backend
// does not support nested transactions).
func (s *Service) ResolveKeyIDTx(tx kv.Tx, ref string) (string, error) {
	keyID, err := s.resolve(tx, ref)
	if err != nil && s.autoCreate && types.IsCode(err, types.ErrInvalidKeyID) {
		return s.autoCreateKey(tx, ref)
	}
	return keyID, err
}

// EncryptTx seals plaintext with keyID (a canonical ID from ResolveKeyIDTx)
// and binds it to aad (e.g. the parameter name). The result is base64-encoded.
func (s *Service) EncryptTx(r kv.Reader, keyID string, plaintext, aad []byte) (string, error) {
	aead, err := s.keyAEAD(r, keyID)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", types.NewInternalError("generating nonce: %v", err)
	}
	blob := make([]byte, 0, 1+keyIDLength+len(nonce)+len(plaintext)+aead.Overhead())
	blob = append(blob, ciphertextVersion)
	blob = append(blob, keyID...)
	blob = append(blob, nonce...)
	blob = aead.Seal(blob, nonce, plaintext, aad)
	return base64.StdEncoding.EncodeToString(blob), nil
}

// Encrypt resolves keyRef and seals plaintext in its own transaction.
func (s *Service) Encrypt(keyRef string, plaintext, aad []byte) (string, error) {
	var out string
	err := s.store.Update(func(tx kv.Tx) error {
		keyID, err := s.ResolveKeyIDTx(tx, keyRef)
		if err != nil {
			return err
		}
		out, err = s.EncryptTx(tx, keyID, plaintext, aad)
		return err
	})
	return out, err
}

// DecryptTx opens a value produced by EncryptTx.
func (s *Service) DecryptTx(r kv.Reader, ciphertext string, aad []byte) ([]byte, error) {
	blob, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil || len(blob) < 1+keyIDLength || blob[0] != ciphertextVersion {
		return nil, types.NewInternalError("invalid ciphertext")
	}
	keyID := string(blob[1 : 1+keyIDLength])
	aead, err := s.keyAEAD(r, keyID)
	if err != nil {
		return nil, err
	}
	rest := blob[1+keyIDLength:]
	if len(rest) < aead.NonceSize() {
		return nil, types.NewInternalError("invalid ciphertext")
	}
	plain, err := aead.Open(nil, rest[:aead.NonceSize()], rest[aead.NonceSize():], aad)
	if err != nil {
		return nil, types.NewInternalError("decryption failed for key %s", keyID)
	}
	return plain, nil
}

// Decrypt opens a value in its own transaction.
func (s *Service) Decrypt(ciphertext string, aad []byte) ([]byte, error) {
	var out []byte
	err := s.store.View(func(r kv.Reader) error {
		var err error
		out, err = s.DecryptTx(r, ciphertext, aad)
		return err
	})
	return out, err
}

func (s *Service) info(k *Key, aliases []string) *KeyInfo {
	return &KeyInfo{
		KeyID:       k.KeyID,
		ARN:         s.account.KeyARN(k.KeyID),
		Description: k.Description,
		Aliases:     aliases,
		CreatedAt:   k.CreatedAt,
	}
}

// resolve maps a key ID, key ARN, alias, or alias ARN to a key ID.
func (s *Service) resolve(r kv.Reader, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = DefaultSSMAlias
	}
	if strings.HasPrefix(ref, "arn:") {
		parts := strings.SplitN(ref, ":", 6)
		if len(parts) != 6 || parts[2] != "kms" {
			return "", types.NewInvalidKeyID(ref)
		}
		ref = parts[5] // "key/<id>" or "alias/<name>"
		ref = strings.TrimPrefix(ref, "key/")
	}
	if strings.HasPrefix(ref, "alias/") {
		v, err := r.Get(bucketAliases, ref)
		if errors.Is(err, kv.ErrNotFound) {
			return "", types.NewInvalidKeyID(ref)
		}
		if err != nil {
			return "", err
		}
		return string(v), nil
	}
	if _, err := r.Get(bucketKeys, ref); err != nil {
		if errors.Is(err, kv.ErrNotFound) {
			return "", types.NewInvalidKeyID(ref)
		}
		return "", err
	}
	return ref, nil
}

func (s *Service) autoCreateKey(tx kv.Tx, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	alias := ""
	switch {
	case strings.HasPrefix(ref, "alias/"):
		alias = ref
	case strings.HasPrefix(ref, "arn:") && strings.Contains(ref, ":alias/"):
		alias = ref[strings.Index(ref, ":alias/")+1:]
	}
	if alias != "" {
		return s.ensureAliasTx(tx, alias, "Auto-created by OpenSSM")
	}
	keyID := strings.TrimPrefix(ref[strings.LastIndex(ref, ":")+1:], "key/")
	if len(keyID) != keyIDLength {
		return "", types.NewInvalidKeyID(ref)
	}
	if _, err := tx.Get(bucketKeys, keyID); err == nil {
		return keyID, nil
	}
	_, err := s.putKey(tx, keyID, "Auto-created by OpenSSM")
	return keyID, err
}

// ensureAlias returns the key behind alias, creating both if missing.
func (s *Service) ensureAlias(alias, description string) (string, error) {
	var keyID string
	err := s.store.Update(func(tx kv.Tx) error {
		var err error
		keyID, err = s.ensureAliasTx(tx, alias, description)
		return err
	})
	return keyID, err
}

func (s *Service) ensureAliasTx(tx kv.Tx, alias, description string) (string, error) {
	if v, err := tx.Get(bucketAliases, alias); err == nil {
		return string(v), nil
	}
	k, err := s.newKey(tx, description)
	if err != nil {
		return "", err
	}
	return k.KeyID, tx.Put(bucketAliases, alias, []byte(k.KeyID))
}

func (s *Service) newKey(tx kv.Tx, description string) (*Key, error) {
	return s.putKey(tx, id.NewUUID(), description)
}

func (s *Service) putKey(tx kv.Tx, keyID, description string) (*Key, error) {
	material := make([]byte, 32)
	if _, err := rand.Read(material); err != nil {
		return nil, types.NewInternalError("generating key material: %v", err)
	}
	nonce := make([]byte, s.master.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, types.NewInternalError("generating nonce: %v", err)
	}
	k := &Key{
		KeyID:           keyID,
		Description:     description,
		CreatedAt:       types.Now(),
		WrappedMaterial: s.master.Seal(nonce, nonce, material, []byte(keyID)),
	}
	if err := kv.PutJSON(tx, bucketKeys, keyID, k); err != nil {
		return nil, err
	}
	return k, nil
}

// keyAEAD returns the (cached) AEAD for keyID, unwrapping its material.
func (s *Service) keyAEAD(r kv.Reader, keyID string) (cipher.AEAD, error) {
	s.mu.RLock()
	aead, ok := s.cache[keyID]
	s.mu.RUnlock()
	if ok {
		return aead, nil
	}
	var k Key
	err := kv.GetJSON(r, bucketKeys, keyID, &k)
	if errors.Is(err, kv.ErrNotFound) {
		return nil, types.NewInvalidKeyID(keyID)
	}
	if err != nil {
		return nil, err
	}
	ns := s.master.NonceSize()
	if len(k.WrappedMaterial) < ns {
		return nil, types.NewInternalError("corrupt key %s", keyID)
	}
	material, err := s.master.Open(nil, k.WrappedMaterial[:ns], k.WrappedMaterial[ns:], []byte(keyID))
	if err != nil {
		return nil, types.NewInternalError("cannot unwrap key %s: master key mismatch", keyID)
	}
	aead, err = newAEAD(material)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cache[keyID] = aead
	s.mu.Unlock()
	return aead, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("kms: creating cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

func validateAlias(alias string) error {
	if !strings.HasPrefix(alias, "alias/") || len(alias) <= len("alias/") || len(alias) > 256 {
		return types.NewValidationError("Alias must start with 'alias/' and be at most 256 characters")
	}
	if strings.HasPrefix(alias, "alias/aws/") {
		return types.NewValidationError("Alias names starting with 'alias/aws/' are reserved")
	}
	for _, c := range alias[len("alias/"):] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("/_-", c)) {
			return types.NewValidationError("Alias contains invalid character %q", c)
		}
	}
	return nil
}
