package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Manager encrypts and decrypts blobs using AES-256-GCM while embedding a key identifier
// so future rotations can keep decrypting historical payloads.
type Manager struct {
	primaryKeyID string
	keys         map[string][]byte
	randSource   io.Reader
}

// Option allows customizing the manager.
type Option func(*Manager)

// WithRandSource overrides the randomness source (useful for tests).
func WithRandSource(r io.Reader) Option {
	return func(m *Manager) {
		if r != nil {
			m.randSource = r
		}
	}
}

// NewManager builds an AES-256-GCM manager from a key ring map where each value is
// a base64-encoded 32-byte key. The primary key must exist in the ring.
func NewManager(primaryKeyID string, keyRing map[string]string, opts ...Option) (*Manager, error) {
	if primaryKeyID == "" {
		return nil, errors.New("primary key id required")
	}
	if len(keyRing) == 0 {
		return nil, errors.New("key ring cannot be empty")
	}
	material := make(map[string][]byte, len(keyRing))
	for id, encoded := range keyRing {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode key %s: %w", id, err)
		}
		if len(decoded) != 32 {
			return nil, fmt.Errorf("key %s must be 32 bytes for AES-256", id)
		}
		material[id] = decoded
	}
	if _, ok := material[primaryKeyID]; !ok {
		return nil, fmt.Errorf("primary key %s missing from key ring", primaryKeyID)
	}
	mgr := &Manager{
		primaryKeyID: primaryKeyID,
		keys:         material,
		randSource:   rand.Reader,
	}
	for _, opt := range opts {
		opt(mgr)
	}
	return mgr, nil
}

type envelope struct {
	KeyID      string `json:"kid"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"cipher"`
}

// Encrypt seals plaintext and returns a JSON envelope that stores the key identifier,
// nonce, and ciphertext (base64 encoded) so it can be persisted as-is.
func (m *Manager) Encrypt(data []byte) ([]byte, error) {
	if m == nil {
		return nil, errors.New("encryption manager is nil")
	}
	key, ok := m.keys[m.primaryKeyID]
	if !ok {
		return nil, errors.New("active key not found")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(m.randSource, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, data, nil)
	env := envelope{
		KeyID:      m.primaryKeyID,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	return json.Marshal(env)
}

// Decrypt unseals the ciphertext envelope back into plaintext.
func (m *Manager) Decrypt(payload []byte) ([]byte, error) {
	if m == nil {
		return nil, errors.New("encryption manager is nil")
	}
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, fmt.Errorf("unmarshal envelope: %w", err)
	}
	key, ok := m.keys[env.KeyID]
	if !ok {
		return nil, fmt.Errorf("unknown key id %s", env.KeyID)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode cipher: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}

// ActiveKeyID exposes the key identifier used for new encrypt operations.
func (m *Manager) ActiveKeyID() string {
	if m == nil {
		return ""
	}
	return m.primaryKeyID
}

// KeyIDs lists all known key identifiers.
func (m *Manager) KeyIDs() []string {
	if m == nil {
		return nil
	}
	ids := make([]string, 0, len(m.keys))
	for id := range m.keys {
		ids = append(ids, id)
	}
	return ids
}
