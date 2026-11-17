// Package sekey drives the sshgate-touchid helper's key-create and key-sign
// commands: P-256 keys that live in the Secure Enclave, as an ssh.Signer.
// The key blob goes to the helper on stdin only, and no error carries stderr.
package sekey

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"os/exec"
	"time"

	"golang.org/x/crypto/ssh"
)

var (
	ErrWrongMac    = errors.New("the key no longer works on this Mac")
	ErrUnavailable = errors.New("the Secure Enclave is not available")
	errHelper      = errors.New("secure enclave helper failed")
)

const signTimeout = 10 * time.Second

func run(ctx context.Context, helper, cmd string, stdin []byte) ([]byte, error) {
	c := exec.CommandContext(ctx, helper, cmd)
	c.Stdin = bytes.NewReader(stdin)
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = io.Discard // never surfaced: it may echo input
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			switch ee.ExitCode() {
			case 3:
				return nil, ErrWrongMac
			case 5:
				return nil, ErrUnavailable
			}
		}
		return nil, errHelper
	}
	return out.Bytes(), nil
}

// Create makes a new Secure Enclave key and returns its opaque blob and public key.
func Create(ctx context.Context, helper string) ([]byte, *ecdsa.PublicKey, error) {
	out, err := run(ctx, helper, "key-create", nil)
	if err != nil {
		return nil, nil, err
	}
	var r struct {
		V    int    `json:"v"`
		Blob string `json:"blob"`
		Pub  string `json:"pub"`
	}
	if json.Unmarshal(out, &r) != nil || r.V != 1 {
		return nil, nil, errHelper
	}
	blob, err1 := base64.StdEncoding.DecodeString(r.Blob)
	raw, err2 := base64.StdEncoding.DecodeString(r.Pub)
	if err1 != nil || err2 != nil || len(blob) == 0 {
		return nil, nil, errHelper
	}
	ek, err := ecdh.P256().NewPublicKey(raw) // validates the point is on the curve
	if err != nil {
		return nil, nil, errHelper
	}
	return blob, &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(raw[1:33]), Y: new(big.Int).SetBytes(ek.Bytes()[33:65])}, nil
}

// Sign signs data (SHA-256 inside the helper) with the key behind blob.
func Sign(ctx context.Context, helper string, blob, data []byte) (r, s *big.Int, err error) {
	in, _ := json.Marshal(struct {
		Blob string `json:"blob"`
		Data string `json:"data"`
	}{base64.StdEncoding.EncodeToString(blob), base64.StdEncoding.EncodeToString(data)})
	out, err := run(ctx, helper, "key-sign", in)
	if err != nil {
		return nil, nil, err
	}
	var resp struct {
		Sig string `json:"sig"`
	}
	if json.Unmarshal(out, &resp) != nil {
		return nil, nil, errHelper
	}
	sig, err := base64.StdEncoding.DecodeString(resp.Sig)
	if err != nil || len(sig) != 64 {
		return nil, nil, errHelper
	}
	return new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]), nil
}

type signer struct {
	helper string
	blob   []byte
	pub    ssh.PublicKey
}

// NewSigner returns an ssh.Signer whose every signature runs the helper.
func NewSigner(helper string, blob []byte, pub ssh.PublicKey) ssh.Signer {
	return &signer{helper, blob, pub}
}

func (s *signer) PublicKey() ssh.PublicKey { return s.pub }

func (s *signer) Sign(_ io.Reader, data []byte) (*ssh.Signature, error) {
	ctx, cancel := context.WithTimeout(context.Background(), signTimeout)
	defer cancel()
	r, sv, err := Sign(ctx, s.helper, s.blob, data)
	if err != nil {
		return nil, err
	}
	return &ssh.Signature{Format: "ecdsa-sha2-nistp256", Blob: ssh.Marshal(struct{ R, S *big.Int }{r, sv})}, nil
}

// AuthorizedKey is the authorized_keys line for pub.
func AuthorizedKey(pub *ecdsa.PublicKey, comment string) string {
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		return ""
	}
	line := string(bytes.TrimSpace(ssh.MarshalAuthorizedKey(k)))
	if comment != "" {
		line += " " + comment
	}
	return line
}
