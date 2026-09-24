package sshx

import (
	"errors"
	"fmt"
	"net"

	"golang.org/x/crypto/ssh"
)

// ErrHostKeyMismatch is wrapped by the host key callback's error.
var ErrHostKeyMismatch = errors.New("host key mismatch")

func Fingerprint(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}

func HostKeyCallback(pinned string, insecure bool, onLearn func(fp string)) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if insecure {
			return nil
		}
		fp := Fingerprint(key)
		if pinned == "" {
			if onLearn != nil {
				onLearn(fp)
			}
			return nil
		}
		if fp != pinned {
			return fmt.Errorf("%w for %s: got %s, pinned %s", ErrHostKeyMismatch, hostname, fp, pinned)
		}
		return nil
	}
}
