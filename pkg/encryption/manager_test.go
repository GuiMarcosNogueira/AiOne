package encryption

import (
	"bytes"
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

type deterministicReader struct {
	data []byte
}

func (d *deterministicReader) Read(p []byte) (int, error) {
	if len(d.data) < len(p) {
		return 0, io.EOF
	}
	copy(p, d.data[:len(p)])
	return len(p), nil
}

func TestManagerEncryptDecrypt(t *testing.T) {
	key := bytes.Repeat([]byte{0x10}, 32)
	encoded := base64.StdEncoding.EncodeToString(key)
	mgr, err := NewManager("key1", map[string]string{"key1": encoded}, WithRandSource(&deterministicReader{data: bytes.Repeat([]byte{0xAB}, 32)}))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	ciphertext, err := mgr.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	plaintext, err := mgr.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(plaintext) != "secret" {
		t.Fatalf("unexpected plaintext: %s", plaintext)
	}
}

func TestManagerRejectsUnknownKey(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 32))
	mgr, err := NewManager("primary", map[string]string{"primary": key})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	envelope := []byte(`{"kid":"missing","nonce":"AAAAAAAAAAAAAAAA","cipher":"BBBB"}`)
	if _, err := mgr.Decrypt(envelope); err == nil || !strings.Contains(err.Error(), "unknown key id") {
		if err == nil {
			t.Fatal("expected error for unknown key id")
		}
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewManagerValidatesKeyLengths(t *testing.T) {
	if _, err := NewManager("p", map[string]string{"p": base64.StdEncoding.EncodeToString([]byte("short"))}); err == nil {
		t.Fatalf("expected error for short key")
	}
}
