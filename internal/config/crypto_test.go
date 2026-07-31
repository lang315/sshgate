package config

import (
	"strings"
	"testing"
)

func TestKDFVerify(t *testing.T) {
	k, mk, err := NewKDF("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if !k.Verify(mk) {
		t.Fatal("verify own key")
	}
	wrong, _ := k.DeriveKey("nope")
	if k.Verify(wrong) {
		t.Fatal("wrong password must not verify")
	}
}

func TestEncryptRoundtrip(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	aad := "1|prod|encPassword|host|22|root|password"
	blob, err := Encrypt(mk, "prod/encPassword", aad, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decrypt(mk, "prod/encPassword", aad, blob)
	if err != nil || got != "s3cret" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestAADMismatchFails(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	blob, _ := Encrypt(mk, "prod/encPassword", "aad-A", "s3cret")
	if _, err := Decrypt(mk, "prod/encPassword", "aad-B", blob); err == nil {
		t.Fatal("changed AAD must fail decryption")
	}
}

func TestNonceUnique(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	a, _ := Encrypt(mk, "l", "aad", "same")
	b, _ := Encrypt(mk, "l", "aad", "same")
	if a == b {
		t.Fatal("same plaintext must yield different ciphertext (fresh nonce)")
	}
	if strings.HasPrefix(a, b[:8]) {
		t.Fatal("nonce reused")
	}
}

func TestDeriveKeyRejectsBadParams(t *testing.T) {
	k, _, _ := NewKDF("pw")
	bad := k
	bad.Parallelism = 0
	if _, err := bad.DeriveKey("pw"); err == nil {
		t.Fatal("parallelism=0 must return an error, not panic")
	}
	bad2 := k
	bad2.Time = 0
	if _, err := bad2.DeriveKey("pw"); err == nil {
		t.Fatal("time=0 must return an error")
	}
	bad3 := k
	bad3.Parallelism = 256
	if _, err := bad3.DeriveKey("pw"); err == nil {
		t.Fatal("parallelism=256 (truncates to 0) must return an error")
	}
}
