// Command fakehelper speaks the sshgate-touchid key-create/key-sign contract
// with a software P-256 key; its blob is the key's PKCS#8 DER. Test only.
// FAKE_EXIT=<n> exits with n (after writing the blob to stderr, on purpose);
// FAKE_MARKER=<path> creates that file whenever it runs.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
)

func main() {
	if m := os.Getenv("FAKE_MARKER"); m != "" {
		os.WriteFile(m, nil, 0o600)
	}
	var in struct{ Blob, Data string }
	if len(os.Args) == 2 && os.Args[1] == "key-sign" {
		b, _ := io.ReadAll(os.Stdin)
		json.Unmarshal(b, &in)
	}
	if v := os.Getenv("FAKE_EXIT"); v != "" {
		n, _ := strconv.Atoi(v)
		fmt.Fprintln(os.Stderr, "fake failure, blob="+in.Blob)
		os.Exit(n)
	}
	if len(os.Args) != 2 {
		os.Exit(1)
	}
	e := base64.StdEncoding
	switch os.Args[1] {
	case "key-create":
		k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		der, _ := x509.MarshalPKCS8PrivateKey(k)
		pub, _ := k.PublicKey.ECDH()
		json.NewEncoder(os.Stdout).Encode(map[string]any{"v": 1, "blob": e.EncodeToString(der), "pub": e.EncodeToString(pub.Bytes())})
	case "key-sign":
		der, err1 := e.DecodeString(in.Blob)
		data, err2 := e.DecodeString(in.Data)
		k, err3 := x509.ParsePKCS8PrivateKey(der)
		if err1 != nil || err2 != nil || err3 != nil {
			os.Exit(3)
		}
		h := sha256.Sum256(data)
		r, s, _ := ecdsa.Sign(rand.Reader, k.(*ecdsa.PrivateKey), h[:])
		sig := make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
		json.NewEncoder(os.Stdout).Encode(map[string]string{"sig": e.EncodeToString(sig)})
	default:
		os.Exit(1)
	}
}
