package envvars

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestCipherUsesAssociatedDataAndDoesNotExposePlaintext(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	cipher, err := NewCipher(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal([]byte("secret-value"), "workspace:key")
	if err != nil {
		t.Fatal(err)
	}
	if string(sealed) == "secret-value" {
		t.Fatal("ciphertext exposed plaintext")
	}
	if _, err := cipher.Open(sealed, "other-workspace:key"); err == nil {
		t.Fatal("expected associated data mismatch to fail")
	}
	opened, err := cipher.Open(sealed, "workspace:key")
	if err != nil || string(opened) != "secret-value" {
		t.Fatalf("unexpected plaintext %q: %v", opened, err)
	}
}

func TestValidateEnvironmentVariable(t *testing.T) {
	for _, name := range []string{"API_KEY", "token_2", "_PRIVATE"} {
		if err := Validate(name, "value"); err != nil {
			t.Fatalf("expected %q to be valid: %v", name, err)
		}
	}
	for _, name := range []string{"", "2KEY", "BAD-KEY", "WITH SPACE"} {
		if err := Validate(name, "value"); err == nil {
			t.Fatalf("expected %q to be invalid", name)
		}
	}
}
