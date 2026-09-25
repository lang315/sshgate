package sshx

import (
	"errors"
	"net"
	"strconv"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// KnownHostsHint says whether an OpenSSH known_hosts file already trusts key
// for host:port: "match", "different" (it lists another key, or revoked this
// one), or "absent" (no entry, or no readable file). It is a hint for the
// human only: the file is outside the vault's MAC and is never pinned from.
func KnownHostsHint(file, host string, port int, key ssh.PublicKey) string {
	cb, err := knownhosts.New(file)
	if err != nil {
		return "absent"
	}
	var ke *knownhosts.KeyError
	var re *knownhosts.RevokedError
	switch err := cb(net.JoinHostPort(host, strconv.Itoa(port)), &net.TCPAddr{IP: net.IPv4zero, Port: port}, key); {
	case err == nil:
		return "match"
	case errors.As(err, &ke) && len(ke.Want) > 0, errors.As(err, &re):
		return "different"
	}
	return "absent"
}
