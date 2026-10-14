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
	"runtime"
	"runtime/debug"

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

// Part of the vault format, not the product name: existing vaults were
// created with this value, so it stays when the product is renamed.
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
	mk := argon2Key([]byte(masterPw), salt, k.Time, k.MemoryKiB, uint8(k.Parallelism), k.KeyLen)
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
	return argon2Key([]byte(masterPw), salt, k.Time, k.MemoryKiB, uint8(k.Parallelism), k.KeyLen), nil
}

// argon2Key is argon2.IDKey, then hands its work area (memKiB) back to the
// OS; the long-running hub would otherwise keep it as idle heap after unlock.
// The released memory is not zeroed, same as any freed heap.
func argon2Key(pw, salt []byte, t, memKiB uint32, par uint8, keyLen uint32) []byte {
	// macOS: with more than ~4 Ps running, the OS often ignores the Go
	// runtime's release of the work area (measured: 30% released in the hub,
	// 93% with this cap). par Ps is all Argon2 can use anyway.
	if prev := runtime.GOMAXPROCS(0); runtime.GOOS == "darwin" && prev > int(par) {
		runtime.GOMAXPROCS(int(par))
		defer runtime.GOMAXPROCS(prev)
	}
	key := argon2.IDKey(pw, salt, t, memKiB, par, keyLen)
	debug.FreeOSMemory()
	return key
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
