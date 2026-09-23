// Package secrets implements Box, an AES-256-GCM encryption box keyed from
// the server's APP_SECRET.
//
// A Box derives one key per purpose with HKDF-SHA256, using APP_SECRET as
// the input key material, a fixed salt ("vantigo-secrets-v1"), and the
// purpose string as HKDF info. Purposes are constants owned by the callers
// — e.g. "identity/totp" and "identity/oidc-state" — never end-user input.
// Each derived key is computed once and cached for the life of the Box.
//
// Seal's output is:
//
//	0x01 || nonce(12) || ciphertext+tag
//
// Byte 0 is a key id — 1 is the only one that exists today — so a future
// key rotation can introduce key id 2 (and a newly derived key alongside
// it) without breaking Open on data sealed under key id 1. The purpose
// string is passed to AES-GCM as additional authenticated data, so a value
// sealed under one purpose cannot be opened under another.
//
// Seal refuses a plaintext larger than 64 MiB (maxPlaintextBytes) with
// ErrTooLarge, which is far above every purpose the box serves and keeps the
// arithmetic on the output's size trivially in range.
//
// Open never says why it failed: a short input, an unknown key id, a wrong
// purpose, and a tampered ciphertext all return ErrInvalid. SealString and
// OpenString are the same operations for text contexts such as cookies,
// encoded with unpadded base64url. The box never logs, and none of its
// errors carry plaintext or key material.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"golang.org/x/crypto/hkdf"
)

// ErrInvalid is returned by Open and OpenString for any failure — a short
// input, an unknown key id, a wrong purpose, or a failed authentication —
// without saying which, so it cannot be used as an oracle.
var ErrInvalid = errors.New("secrets: invalid")

// ErrTooLarge is returned by Seal and SealString for a plaintext above
// maxPlaintextBytes. Unlike Open's failures this one is a caller's bug, not
// an attacker's input, so the error says how large the value was — its
// length, never its bytes.
var ErrTooLarge = errors.New("secrets: plaintext too large")

// maxPlaintextBytes is the largest plaintext Seal will encrypt: 64 MiB.
// Every purpose the box serves is a cookie, a TOTP secret or a small JSON
// blob, orders of magnitude below this, so the bound refuses nothing a
// caller legitimately does. What it buys is that Seal's output size —
// 1 + nonceSize + len(plaintext) + the GCM tag — is a sum of known small
// terms that cannot overflow an int on any architecture Go supports,
// whatever produced the plaintext.
//
// It is a var rather than a const for exactly one reason: the test that proves
// the boundary has to seal a plaintext of precisely this size, and 64 MiB in,
// 64 MiB sealed and 64 MiB opened is a quarter of a gigabyte for a test about
// an off-by-one — more than a small CI runner should be asked for. The test
// lowers it and restores it under t.Cleanup (and must therefore not be
// parallel). Nothing outside that test ever writes to it.
var maxPlaintextBytes = 64 << 20

// hkdfSalt fixes the HKDF salt used to derive every purpose's key. It is
// not secret; it exists only to separate this derivation from any other use
// of APP_SECRET.
const hkdfSalt = "vantigo-secrets-v1"

const (
	keySize   = 32 // AES-256 key size, in bytes.
	nonceSize = 12 // AES-GCM standard nonce size, in bytes.
)

// currentKeyID is the key id Seal writes and the only one Open accepts. A
// future rotation adds a new key id (and a corresponding derived key)
// rather than changing this one.
const currentKeyID byte = 0x01

// Box seals and opens byte slices with AES-256-GCM, using a key derived per
// purpose from the process's APP_SECRET. A *Box is safe for concurrent use.
type Box struct {
	appSecret []byte
	keys      sync.Map // purpose string -> []byte (the derived 32-byte key)
}

// New builds a Box from appSecret, the process-wide key material.
// appSecret must be at least 32 bytes. config.Config.AppSecret already
// enforces this at load time, but New checks it too since it is the
// security boundary for every purpose derived from it.
func New(appSecret []byte) (*Box, error) {
	if len(appSecret) < keySize {
		return nil, fmt.Errorf("secrets: app secret must be at least %d bytes, got %d", keySize, len(appSecret))
	}
	return &Box{appSecret: append([]byte(nil), appSecret...)}, nil
}

// keyFor returns the 32-byte AES-256 key for purpose, deriving it with
// HKDF-SHA256 on first use and caching it for subsequent calls.
func (b *Box) keyFor(purpose string) ([]byte, error) {
	if cached, ok := b.keys.Load(purpose); ok {
		return cached.([]byte), nil
	}

	key := make([]byte, keySize)
	r := hkdf.New(sha256.New, b.appSecret, []byte(hkdfSalt), []byte(purpose))
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, err
	}

	actual, _ := b.keys.LoadOrStore(purpose, key)
	return actual.([]byte), nil
}

// aeadFor builds the AES-256-GCM AEAD for purpose's derived key.
func (b *Box) aeadFor(purpose string) (cipher.AEAD, error) {
	key, err := b.keyFor(purpose)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plaintext under purpose's derived key with a fresh random
// nonce. The result is 0x01 || nonce(12) || ciphertext+tag; purpose is
// passed to AES-GCM as additional data, so it must match on Open. A
// plaintext above maxPlaintextBytes is ErrTooLarge, checked before any key
// is derived or any buffer allocated.
func (b *Box) Seal(purpose string, plaintext []byte) ([]byte, error) {
	if len(plaintext) > maxPlaintextBytes {
		return nil, fmt.Errorf("%w: %d bytes, maximum %d", ErrTooLarge, len(plaintext), maxPlaintextBytes)
	}

	gcm, err := b.aeadFor(purpose)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	out := make([]byte, 0, 1+nonceSize+len(plaintext)+gcm.Overhead())
	out = append(out, currentKeyID)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plaintext, []byte(purpose))
	return out, nil
}

// Open decrypts sealed, which must have been produced by Seal under the
// same purpose. Any failure — sealed is too short, its key id is not
// currentKeyID, or authentication fails (wrong purpose, tampered bytes, or
// a key derived from a different APP_SECRET) — returns ErrInvalid without
// saying which.
func (b *Box) Open(purpose string, sealed []byte) ([]byte, error) {
	if len(sealed) < 1+nonceSize {
		return nil, ErrInvalid
	}
	if sealed[0] != currentKeyID {
		return nil, ErrInvalid
	}

	gcm, err := b.aeadFor(purpose)
	if err != nil {
		return nil, ErrInvalid
	}

	nonce := sealed[1 : 1+nonceSize]
	ciphertext := sealed[1+nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(purpose))
	if err != nil {
		return nil, ErrInvalid
	}
	return plaintext, nil
}

// SealString is Seal for string plaintexts, encoded as unpadded base64url
// (base64.RawURLEncoding) for use in cookies and other text contexts.
func (b *Box) SealString(purpose, s string) (string, error) {
	sealed, err := b.Seal(purpose, []byte(s))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// OpenString is Open for strings produced by SealString. A malformed
// base64url encoding, like any other failure, returns ErrInvalid.
func (b *Box) OpenString(purpose, s string) (string, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", ErrInvalid
	}
	plaintext, err := b.Open(purpose, sealed)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// Format writes exactly "secrets.Box{redacted}" for every verb, preventing
// fmt's reflection from printing the appSecret.
func (b *Box) Format(f fmt.State, verb rune) {
	_, _ = fmt.Fprintf(f, "secrets.Box{redacted}")
}

// LogValue returns a slog.Value that prevents log/slog handlers from
// reflecting into the Box's appSecret field.
func (b *Box) LogValue() slog.Value {
	return slog.StringValue("secrets.Box{redacted}")
}
