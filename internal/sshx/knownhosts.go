package sshx

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"

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

// KnownHostKey picks the key the known_hosts files record for host:port,
// for pinning at import: ed25519, then ecdsa, then rsa. A revoked key is
// never returned, nor is an @cert-authority key. Unreadable files are skipped.
func KnownHostKey(files []string, host string, port int) (algo, fingerprint string, ok bool) {
	type pos struct {
		file string
		line int
	}
	var have []string
	caLines := map[pos]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		have = append(have, f)
		// Numbered as knownhosts numbers them, so KnownKey.Line matches.
		sc := bufio.NewScanner(bytes.NewReader(b))
		for n := 1; sc.Scan(); n++ {
			if w := strings.Fields(sc.Text()); len(w) > 0 && w[0] == "@cert-authority" {
				caLines[pos{f, n}] = true
			}
		}
	}
	if len(have) == 0 {
		return "", "", false
	}
	cb, err := knownhosts.New(have...)
	if err != nil {
		return "", "", false
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	remote := &net.TCPAddr{IP: net.IPv4zero, Port: port}
	// A throwaway key never matches, so the KeyError lists every key on record.
	probe, _, _ := ed25519.GenerateKey(nil)
	pk, _ := ssh.NewPublicKey(probe)
	var ke *knownhosts.KeyError
	if !errors.As(cb(addr, remote, pk), &ke) {
		return "", "", false
	}
	var pick ssh.PublicKey
	for _, k := range ke.Want {
		r := keyRank(k.Key.Type())
		if r < 0 || caLines[pos{k.Filename, k.Line}] || pick != nil && r >= keyRank(pick.Type()) {
			continue
		}
		if cb(addr, remote, k.Key) == nil { // not revoked
			pick = k.Key
		}
	}
	if pick == nil {
		return "", "", false
	}
	return pick.Type(), Fingerprint(pick), true
}

func keyRank(t string) int {
	switch {
	case t == ssh.KeyAlgoED25519:
		return 0
	case strings.HasPrefix(t, "ecdsa-sha2-"):
		return 1
	case t == ssh.KeyAlgoRSA:
		return 2
	}
	return -1
}
