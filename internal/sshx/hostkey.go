package sshx

import (
	"errors"
	"fmt"
	"net"

	"golang.org/x/crypto/ssh"
)

// ErrHostKeyMismatch is wrapped by *HostKeyMismatchError.
var ErrHostKeyMismatch = errors.New("host key mismatch")

// HostKeyUnknownError is a strict dial's refusal of a host with no pin. Key
// is the key the server presented.
type HostKeyUnknownError struct {
	Fingerprint, KeyType string
	Key                  ssh.PublicKey
}

func (e *HostKeyUnknownError) Error() string {
	return fmt.Sprintf("host key not pinned: server presented %s %s", e.KeyType, e.Fingerprint)
}

// HostKeyMismatchError: the server presented a key other than the pinned one.
type HostKeyMismatchError struct {
	Pinned, Presented, KeyType string
	Key                        ssh.PublicKey
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("%v: got %s, pinned %s", ErrHostKeyMismatch, e.Presented, e.Pinned)
}

func (e *HostKeyMismatchError) Unwrap() error { return ErrHostKeyMismatch }

func Fingerprint(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}

// HostKeyCallback checks the presented key against pinned. With no pin it
// learns the key through onLearn (TOFU: --host mode only) or,
// when onLearn is nil, refuses with *HostKeyUnknownError (strict).
func HostKeyCallback(pinned string, insecure bool, onLearn func(fp string)) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if insecure {
			return nil
		}
		fp := Fingerprint(key)
		switch {
		case pinned == "" && onLearn == nil:
			return &HostKeyUnknownError{Fingerprint: fp, KeyType: key.Type(), Key: key}
		case pinned == "":
			onLearn(fp)
		case fp != pinned:
			return &HostKeyMismatchError{Pinned: pinned, Presented: fp, KeyType: key.Type(), Key: key}
		}
		return nil
	}
}

// hostKeyAlgorithms limits negotiation to a pinned key's family, so a server
// that adds a key of another type still presents the pinned one.
func hostKeyAlgorithms(algo string) []string {
	switch algo {
	case "":
		return nil
	case ssh.KeyAlgoRSA:
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{algo}
}
