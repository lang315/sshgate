package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/lang315/sshgate/internal/config"
)

// runInbound is the forced command of an authorized_keys line the hub wrote
// (spec 2026-11-20-inbound-ssh-design.md): `sshgate inbound <name>`. sshd runs
// it in place of the session. It never reads the vault: it appends one login
// record to audit.jsonl and replaces itself with what the client asked for.
// When the record cannot be written, no session runs.
func runInbound(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: sshgate inbound <device>")
	}
	return inbound(args[0], os.Getenv, os.Environ(), execProc)
}

// inboundAuditPath is audit.jsonl next to the store; SSHGATE_STORE moves the store,
// as for the hub's --store. sshd gives a session a minimal environment, so the
// default is the normal one.
func inboundAuditPath(getenv func(string) string) string {
	store := storePath()
	if p := getenv("SSHGATE_STORE"); p != "" {
		store = p
	}
	return filepath.Join(filepath.Dir(store), "audit.jsonl")
}

func inbound(device string, getenv func(string) string, environ []string, exec func(path string, argv, env []string) error) error {
	if err := config.ValidKeyName(device); err != nil {
		return errors.New("bad device name")
	}
	// SSH_CONNECTION is "<client ip> <client port> <server ip> <server port>".
	conn := strings.Fields(getenv("SSH_CONNECTION"))
	if len(conn) != 4 {
		return errors.New("inbound is run by sshd, not by hand")
	}
	if _, err := netip.ParseAddr(conn[0]); err != nil {
		return errors.New("inbound is run by sshd, not by hand")
	}
	orig := getenv("SSH_ORIGINAL_COMMAND")
	session := "command"
	switch base := path.Base(strings.Fields(orig + " x")[0]); {
	case orig == "":
		session = "shell"
	case base == "sftp-server" || base == "internal-sftp":
		session = "sftp"
	}
	rec, _ := json.Marshal(map[string]any{
		"time": time.Now().UTC().Format(time.RFC3339Nano), "kind": "login", "device": device,
		"from": net.JoinHostPort(conn[0], conn[1]), "session": session, "tty": getenv("SSH_TTY") != "",
	})
	f, err := os.OpenFile(inboundAuditPath(getenv), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err == nil {
		_, err = f.Write(append(rec, '\n')) // one Write, so a concurrent hub append cannot interleave
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		return errors.New("could not record this login")
	}

	shell := getenv("SHELL")
	if !filepath.IsAbs(shell) {
		shell = "/bin/sh"
	}
	argv := []string{shell, "-l"}
	if session != "shell" {
		argv = []string{shell, "-c", orig}
	}
	return exec(shell, argv, environ)
}
