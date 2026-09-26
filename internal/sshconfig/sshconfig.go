// Package sshconfig reads the hosts an OpenSSH client config names and
// resolves each one with `ssh -G`, which applies Host *, Match and %-tokens
// exactly as ssh does, without connecting.
package sshconfig

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/lang315/sshgate/internal/config"
)

// maxDepth is OpenSSH's own Include limit.
const maxDepth = 16

// Aliases lists the names on Host lines in path and every file it Includes,
// first appearance first. A name with * or ? or a leading ! is a pattern, not
// a host. A missing path is an empty list.
func Aliases(path string) ([]string, error) {
	var out []string
	err := collect(path, 0, map[string]bool{}, &out)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return out, err
}

func collect(path string, depth int, seen map[string]bool, out *[]string) error {
	if depth > maxDepth {
		return fmt.Errorf("%s: Include nested too deep", path)
	}
	b, err := os.ReadFile(path)
	if depth > 0 && errors.Is(err, fs.ErrNotExist) {
		return nil // ssh skips an included file that is gone, e.g. a dangling symlink
	}
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		kw, args := fields(line)
		switch strings.ToLower(kw) {
		case "host":
			for _, a := range args {
				if !seen[a] && !strings.ContainsAny(a, "*?") && !strings.HasPrefix(a, "!") {
					seen[a] = true
					*out = append(*out, a)
				}
			}
		case "include":
			for _, a := range args {
				p := expandHome(a)
				if !filepath.IsAbs(p) {
					home, _ := os.UserHomeDir()
					p = filepath.Join(home, ".ssh", p)
				}
				matches, err := filepath.Glob(p)
				if err != nil {
					return err
				}
				for _, m := range matches {
					if err := collect(m, depth+1, seen, out); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// fields splits a config line into its keyword and arguments. The keyword
// ends at whitespace or '='; a double-quoted argument may hold spaces; an
// unquoted argument starting with # begins a comment.
func fields(line string) (string, []string) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] == '#' {
		return "", nil
	}
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return line, nil
	}
	kw, rest := line[:i], strings.TrimLeft(line[i:], " \t")
	rest = strings.TrimLeft(strings.TrimPrefix(rest, "="), " \t")
	var args []string
	for rest != "" {
		var a string
		switch j := strings.IndexAny(rest, " \t"); {
		case rest[0] == '"':
			if k := strings.IndexByte(rest[1:], '"'); k >= 0 {
				a, rest = rest[1:k+1], rest[k+2:]
			} else {
				a, rest = rest[1:], ""
			}
		case rest[0] == '#':
			return kw, args
		case j < 0:
			a, rest = rest, ""
		default:
			a, rest = rest[:j], rest[j:]
		}
		args = append(args, a)
		rest = strings.TrimLeft(rest, " \t")
	}
	return kw, args
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// Resolved is what `ssh -G` says ssh would use for one alias.
type Resolved struct {
	Alias, HostName, User, ProxyJump, ProxyCommand, HostKeyAlias string
	Port                                                         int
	IdentityFiles, KnownHostsFiles                               []string
}

// Resolve runs `ssh -G`, which prints the final options for alias and exits
// without connecting. configPath "" lets ssh read its default config.
func Resolve(ctx context.Context, sshBin, configPath, alias string) (Resolved, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{"-G"}
	if configPath != "" {
		args = append(args, "-F", configPath)
	}
	cmd := exec.CommandContext(ctx, sshBin, append(args, "--", alias)...)
	cmd.WaitDelay = time.Second // a Match exec child can hold the pipes open after ssh is killed
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return Resolved{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
	}
	if err != nil {
		return Resolved{}, err
	}
	r := Resolved{Alias: alias}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch strings.ToLower(k) {
		case "hostname":
			r.HostName = v
		case "port":
			r.Port, _ = strconv.Atoi(v)
		case "user":
			r.User = v
		case "identityfile":
			r.IdentityFiles = append(r.IdentityFiles, v)
		case "proxyjump":
			r.ProxyJump = v
		case "proxycommand":
			r.ProxyCommand = v
		case "hostkeyalias":
			r.HostKeyAlias = v
		case "userknownhostsfile":
			for _, f := range strings.Fields(v) {
				r.KnownHostsFiles = append(r.KnownHostsFiles, expandHome(f))
			}
		}
	}
	return r, nil
}

func set(v string) bool { return v != "" && v != "none" }

// KnownHostsName is the name and port ssh looks up in known_hosts: a
// HostKeyAlias replaces the host name, and then the port is not used.
func (r Resolved) KnownHostsName() (string, int) {
	if set(r.HostKeyAlias) {
		return r.HostKeyAlias, 22
	}
	return r.HostName, r.Port
}

// Candidate is one alias as the import sheet shows it. Status is ready,
// exists (a vault server has this name), or skipped (Reason says why).
type Candidate struct {
	Alias           string `json:"alias"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	User            string `json:"user"`
	Auth            string `json:"auth"`
	KeyPath         string `json:"keyPath,omitempty"`
	NeedsPassphrase bool   `json:"needsPassphrase,omitempty"`
	HostKey         string `json:"hostKey,omitempty"`
	HostKeyAlgo     string `json:"hostKeyAlgo,omitempty"`
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
}

// Check turns a resolved alias into a candidate. The first IdentityFile that
// holds a private key gives key auth; ssh -G lists its own defaults when none
// is set, so no default list is needed here. None (a .pub for an agent-held
// key does not count) means agent auth.
func Check(r Resolved, exists func(string) bool) Candidate {
	c := Candidate{Alias: r.Alias, Host: r.HostName, Port: r.Port, User: r.User, Auth: "agent", Status: "ready"}
	for _, f := range r.IdentityFiles {
		if ok, locked := privateKey(expandHome(f)); ok {
			c.Auth, c.KeyPath, c.NeedsPassphrase = "key", f, locked
			break
		}
	}
	in := config.ServerInput{Name: c.Alias, Host: c.Host, Port: c.Port, User: c.User, Auth: c.Auth, KeyPath: c.KeyPath}
	switch err := in.Validate(); {
	case set(r.ProxyJump):
		c.Status, c.Reason = "skipped", "needs ProxyJump"
	case set(r.ProxyCommand):
		c.Status, c.Reason = "skipped", "needs ProxyCommand"
	case err != nil:
		c.Status, c.Reason = "skipped", err.Error()
	case exists(c.Alias):
		c.Status, c.Reason = "exists", "Already in vault"
	}
	return c
}

// privateKey says whether path holds a private key, and whether that key
// needs a passphrase.
func privateKey(path string) (ok, locked bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	_, err = ssh.ParseRawPrivateKey(b)
	var pm *ssh.PassphraseMissingError
	locked = errors.As(err, &pm)
	return err == nil || locked, locked
}
