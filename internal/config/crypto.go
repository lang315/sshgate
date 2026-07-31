package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
)

type KDF struct {
	Alg         string `json:"alg"`
	V           int    `json:"v"`
	Salt        string `json:"salt"`
	Time        uint32 `json:"time"`
	MemoryKiB   uint32 `json:"memoryKiB"`
	Parallelism uint32 `json:"parallelism"`
	KeyLen      uint32 `json:"keyLen"`
	Verifier    string `json:"verifier"`
}

const verifierConst = "ssh-mcp-verifier-v1"

func NewKDF(masterPw string) (KDF, []byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return KDF{}, nil, err
	}
	k := KDF{
		Alg: "argon2id", V: 1, Salt: base64.StdEncoding.EncodeToString(salt),
		Time: 3, MemoryKiB: 65536, Parallelism: 4, KeyLen: 32,
	}
	mk := argon2.IDKey([]byte(masterPw), salt, k.Time, k.MemoryKiB, uint8(k.Parallelism), k.KeyLen)
	blob, err := Encrypt(mk, "verifier", "verifier", verifierConst)
	if err != nil {
		return KDF{}, nil, err
	}
	k.Verifier = blob
	return k, mk, nil
}

func (k KDF) DeriveKey(masterPw string) ([]byte, error) {
	if k.Alg != "argon2id" || k.V != 1 {
		return nil, fmt.Errorf("unsupported kdf %q v%d", k.Alg, k.V)
	}
	if k.Time < 1 || k.Parallelism < 1 || k.Parallelism > 255 || k.KeyLen < 16 {
		return nil, fmt.Errorf("invalid kdf params")
	}
	salt, err := base64.StdEncoding.DecodeString(k.Salt)
	if err != nil {
		return nil, err
	}
	return argon2.IDKey([]byte(masterPw), salt, k.Time, k.MemoryKiB, uint8(k.Parallelism), k.KeyLen), nil
}

func (k KDF) Verify(masterKey []byte) bool {
	got, err := Decrypt(masterKey, "verifier", "verifier", k.Verifier)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(verifierConst)) == 1
}

func subKey(masterKey []byte, label string) []byte {
	r := hkdf.New(sha256.New, masterKey, nil, []byte(label))
	out := make([]byte, 32)
	io.ReadFull(r, out)
	return out
}

func gcm(key []byte) (cipher.AEAD, error) {
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}

func Encrypt(masterKey []byte, label, aad, plaintext string) (string, error) {
	aead, err := gcm(subKey(masterKey, label))
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := aead.Seal(nil, nonce, []byte(plaintext), []byte(aad))
	return base64.StdEncoding.EncodeToString(append(nonce, ct...)), nil
}

func Decrypt(masterKey []byte, label, aad, blob string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return "", err
	}
	aead, err := gcm(subKey(masterKey, label))
	if err != nil {
		return "", err
	}
	ns := aead.NonceSize()
	if len(raw) < ns {
		return "", fmt.Errorf("ciphertext too short")
	}
	pt, err := aead.Open(nil, raw[:ns], raw[ns:], []byte(aad))
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
