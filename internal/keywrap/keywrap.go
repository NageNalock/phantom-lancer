// Package keywrap provides authenticated symmetric encryption for small
// secrets (tokens, keys) that must be retrievable. It uses AES-256-GCM
// with a key derived from a 32-byte master secret via HKDF-SHA256.
//
// Persisted format: 12-byte nonce || ciphertext || 16-byte GCM tag
// (standard crypto/cipher output). Values up to ~16KB are supported.
package keywrap

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// MinMasterKeyBytes is the minimum acceptable master key length.
const MinMasterKeyBytes = 32

// Keeper wraps and unwraps secrets using AES-256-GCM with a key derived
// from a stable master secret. A Keeper is safe for concurrent use once
// constructed.
type Keeper struct {
	gcm cipher.AEAD
}

// NewKeeper returns a Keeper derived from master.
// info is an optional, stable context label (e.g. "codex-gateway-tokens")
// that binds derived keys to a specific domain; different info values
// produce independent keys.
func NewKeeper(master []byte, info string) (*Keeper, error) {
	if len(master) < MinMasterKeyBytes {
		return nil, fmt.Errorf("keywrap: master key must be >= %d bytes", MinMasterKeyBytes)
	}
	derived := make([]byte, 32)
	r := hkdf.New(sha256.New, master, nil, []byte(info))
	if _, err := io.ReadFull(r, derived); err != nil {
		return nil, fmt.Errorf("keywrap: hkdf: %w", err)
	}
	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, fmt.Errorf("keywrap: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("keywrap: gcm: %w", err)
	}
	return &Keeper{gcm: gcm}, nil
}

// Wrap encrypts plaintext and returns a base64-encoded blob suitable for
// storage in a VARCHAR column. Empty plaintext is passed through verbatim.
func (k *Keeper) Wrap(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, k.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("keywrap: nonce: %w", err)
	}
	out := k.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawURLEncoding.EncodeToString(out), nil
}

// Unwrap decrypts a blob previously produced by Wrap.
func (k *Keeper) Unwrap(blob string) (string, error) {
	if blob == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(blob)
	if err != nil {
		return "", fmt.Errorf("keywrap: base64: %w", err)
	}
	ns := k.gcm.NonceSize()
	if len(raw) <= ns+k.gcm.Overhead() {
		return "", errors.New("keywrap: ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	pt, err := k.gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("keywrap: decrypt: %w", err)
	}
	return string(pt), nil
}

// GenerateMasterKey returns a cryptographically random 32-byte key, encoded
// in base64 so it can be persisted as a string.
func GenerateMasterKey() (string, error) {
	buf := make([]byte, MinMasterKeyBytes)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashForEquality was a legacy equality-hashing helper that compared
// wrapped values by HMAC without decrypting. The initial implementation
// used a constant HMAC key, which violated the docstring's "keyed by the
// master" claim and provided no real security beyond a raw hash. Since
// equality comparisons are currently done by decrypting both sides (the
// only pattern callers use), this function has been removed. If a
// plaintext-less comparison is needed in the future, it MUST derive an
// HMAC key from the Keeper's master via HKDF during NewKeeper and store
// it as a Keeper field — never use a constant key.
//
// This placeholder is intentionally left empty to keep doc references
// searchable; the actual body was removed in favour of explicit
// decrypt-then-compare semantics.
