package sshx

import (
	"fmt"
	"net"

	"golang.org/x/crypto/ssh"
)

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
			return fmt.Errorf("host key mismatch for %s: got %s, pinned %s", hostname, fp, pinned)
		}
		return nil
	}
}
