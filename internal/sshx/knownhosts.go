package sshx

import (
	"crypto/ed25519"
	"errors"
	"io/fs"
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
	cb, _, err := loadKnownHosts([]string{file})
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
	cb, caLines, err := loadKnownHosts(files)
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
		if r < 0 || caLines[k.Line] || pick != nil && r >= keyRank(pick.Type()) {
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

// loadKnownHosts builds one callback from every readable file, dropping each
// line knownhosts cannot parse, as OpenSSH skips it. caLines holds the line
// numbers of @cert-authority entries, as KnownKey.Line numbers them.
func loadKnownHosts(files []string) (cb ssh.HostKeyCallback, caLines map[int]bool, err error) {
	var lines []string
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			lines = append(lines, strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")...)
		}
	}
	if len(lines) == 0 {
		return nil, nil, fs.ErrNotExist
	}
	// knownhosts reads only files, and fails on the first bad line, naming it.
	tmp, err := os.CreateTemp("", "sshgate-known_hosts-")
	if err != nil {
		return nil, nil, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	prefix := "knownhosts: " + tmp.Name() + ":"
	for {
		body := []byte(strings.Join(lines, "\n"))
		if err := tmp.Truncate(0); err != nil {
			return nil, nil, err
		}
		if _, err := tmp.WriteAt(body, 0); err != nil {
			return nil, nil, err
		}
		if cb, err = knownhosts.New(tmp.Name()); err == nil {
			break
		}
		rest, found := strings.CutPrefix(err.Error(), prefix)
		num, _, _ := strings.Cut(rest, ":")
		n, _ := strconv.Atoi(num)
		if !found || n < 1 || n > len(lines) || lines[n-1] == "" {
			return nil, nil, err
		}
		lines[n-1] = ""
	}
	caLines = map[int]bool{}
	for i, l := range lines {
		if w := strings.Fields(l); len(w) > 0 && w[0] == "@cert-authority" {
			caLines[i+1] = true
		}
	}
	return cb, caLines, nil
}
