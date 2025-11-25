package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Hasher implements Argon2id password hashing.
type Hasher struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	saltLength  uint32
	keyLength   uint32
}

// NewHasher configures an Argon2id hasher.
func NewHasher(memory, iterations, saltLength, keyLength uint32, parallelism uint8) *Hasher {
	return &Hasher{
		memory:      memory,
		iterations:  iterations,
		parallelism: parallelism,
		saltLength:  saltLength,
		keyLength:   keyLength,
	}
}

// GenerateSalt returns a random salt of the configured length.
func (h *Hasher) GenerateSalt() ([]byte, error) {
	if h == nil {
		return nil, errors.New("hasher not configured")
	}
	salt := make([]byte, h.saltLength)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return salt, nil
}

// Hash turns a plaintext password into a PHC formatted string.
func (h *Hasher) Hash(password string, salt []byte) (string, error) {
	if h == nil {
		return "", errors.New("hasher not configured")
	}
	if len(salt) == 0 {
		return "", errors.New("missing salt")
	}
	digest := argon2.IDKey([]byte(password), salt, h.iterations, h.memory, h.parallelism, h.keyLength)
	encodedSalt := base64.RawStdEncoding.EncodeToString(salt)
	encodedHash := base64.RawStdEncoding.EncodeToString(digest)
	return fmt.Sprintf("argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", h.memory, h.iterations, h.parallelism, encodedSalt, encodedHash), nil
}

// Verify compares a password with the encoded hash.
func (h *Hasher) Verify(password, encoded string) (bool, error) {
	if h == nil {
		return false, errors.New("hasher not configured")
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" {
		return false, errors.New("invalid hash format")
	}
	versionStr := strings.TrimPrefix(parts[1], "v=")
	if versionStr != "19" {
		return false, fmt.Errorf("unsupported version %s", versionStr)
	}
	params := strings.Split(parts[2], ",")
	if len(params) != 3 {
		return false, errors.New("invalid hash parameters")
	}
	parseUint := func(s, prefix string) (uint64, error) {
		if !strings.HasPrefix(s, prefix) {
			return 0, errors.New("invalid hash format")
		}
		return strconv.ParseUint(strings.TrimPrefix(s, prefix), 10, 32)
	}
	memVal, err := parseUint(params[0], "m=")
	if err != nil {
		return false, err
	}
	iterVal, err := parseUint(params[1], "t=")
	if err != nil {
		return false, err
	}
	parVal, err := parseUint(params[2], "p=")
	if err != nil {
		return false, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, err
	}
	existingHash, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	digest := argon2.IDKey([]byte(password), salt, uint32(iterVal), uint32(memVal), uint8(parVal), uint32(len(existingHash)))
	return subtle.ConstantTimeCompare(existingHash, digest) == 1, nil
}
