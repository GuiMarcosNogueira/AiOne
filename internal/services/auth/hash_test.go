package auth

import "testing"

func TestHasherHashVerify(t *testing.T) {
	h := NewHasher(64*1024, 3, 16, 32, 2)
	salt, err := h.GenerateSalt()
	if err != nil {
		t.Fatalf("generate salt: %v", err)
	}
	hash, err := h.Hash("super-secret", salt)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	ok, err := h.Verify("super-secret", hash)
	if err != nil || !ok {
		t.Fatalf("expected hash verify success, err=%v", err)
	}
	ok, err = h.Verify("bad-pass", hash)
	if err != nil {
		t.Fatalf("verify bad pass: %v", err)
	}
	if ok {
		t.Fatalf("expected failure for invalid password")
	}
}
