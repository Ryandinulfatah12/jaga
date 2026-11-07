// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package camera

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// MinSecretLen is the shortest secret_key accepted. `jaga init` generates 32
// random bytes, base64-encoded.
const MinSecretLen = 32

// sealVersion prefixes every ciphertext so the key derivation or cipher can
// change later without guessing what an old row holds.
const sealVersion byte = 1

// Sealer encrypts camera URLs at rest with AES-256-GCM.
type Sealer struct {
	aead cipher.AEAD
}

// NewSealer derives the URL key from secret_key. Losing the secret makes the
// stored URLs unreadable, so it must be backed up (docs/06-risks.md).
func NewSealer(secret string) (*Sealer, error) {
	if len(secret) < MinSecretLen {
		return nil, fmt.Errorf("secret_key must be at least %d characters", MinSecretLen)
	}
	// HKDF with a purpose label keeps this key separate from any other key
	// derived from the same secret later (sessions, CSRF).
	key, err := hkdf.Key(sha256.New, []byte(secret), nil, "jaga camera url v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts plain. ad (the column name) binds the ciphertext to its
// column, so a main_url value copied into sub_url fails to open. Rows are not
// bound: the id is unknown before INSERT, and moving ciphertext between rows
// already needs write access to the database.
func (s *Sealer) Seal(plain, ad string) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	_, _ = rand.Read(nonce) // crypto/rand.Read never returns an error
	out := append([]byte{sealVersion}, nonce...)
	return s.aead.Seal(out, nonce, []byte(plain), []byte(ad))
}

// ErrSealed means a stored URL cannot be decrypted, usually because
// secret_key changed.
var ErrSealed = errors.New("cannot decrypt camera URL (wrong secret_key?)")

// Open decrypts a value produced by Seal with the same ad.
func (s *Sealer) Open(sealed []byte, ad string) (string, error) {
	n := s.aead.NonceSize()
	if len(sealed) < 1+n+s.aead.Overhead() || sealed[0] != sealVersion {
		return "", ErrSealed
	}
	plain, err := s.aead.Open(nil, sealed[1:1+n], sealed[1+n:], []byte(ad))
	if err != nil {
		return "", ErrSealed
	}
	return string(plain), nil
}
