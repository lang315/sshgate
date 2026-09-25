package mcpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/sshx"
)

func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

type Deps struct {
	CLI       *config.CLIConfig
	File      *config.File
	MasterKey []byte
	Insecure  bool
	Path      string
}

func (d *Deps) ServerNames() []string {
	var out []string
	if d.CLI != nil && d.CLI.HasHost {
		out = append(out, "(default)")
	}
	if d.File != nil {
		for _, s := range d.File.Servers {
			out = append(out, s.Name)
		}
	}
	return out
}

func (d *Deps) IsLocked(name string) bool {
	if d.File == nil {
		return false
	}
	s, ok := d.File.FindServer(name)
	if !ok {
		return false
	}
	enc := s.EncPassword != "" || s.EncSuPassword != "" || s.EncSudoPassword != "" || s.EncKeyPassphrase != ""
	return enc && d.MasterKey == nil
}

func (d *Deps) Resolve(name string) (sshx.DialConfig, error) {
	if name == "" || name == "(default)" {
		if d.CLI == nil || !d.CLI.HasHost {
			return sshx.DialConfig{}, fmt.Errorf("no default server; pass a 'server' name")
		}
		c := d.CLI
		auth := "password"
		if c.Password == "" && c.Key != "" {
			auth = "key"
		}
		dc := sshx.DialConfig{
			Host: c.Host, Port: c.Port, User: c.User, Password: c.Password,
			SuPassword: c.SuPassword, SudoPassword: c.SudoPassword,
			Auth: auth, Insecure: d.Insecure, TimeoutMs: c.TimeoutMs,
		}
		if auth == "key" && c.Key != "" {
			data, err := os.ReadFile(expandPath(c.Key))
			if err != nil {
				return sshx.DialConfig{}, fmt.Errorf("reading key file %q: %w", c.Key, err)
			}
			dc.PrivateKey = string(data)
		}
		return dc, nil
	}
	if d.File == nil {
		return sshx.DialConfig{}, fmt.Errorf("server %q not found", name)
	}
	s, ok := d.File.FindServer(name)
	if !ok {
		return sshx.DialConfig{}, fmt.Errorf("server %q not found", name)
	}
	dec := func(field, blob string) (string, error) {
		if blob == "" {
			return "", nil
		}
		if d.MasterKey == nil {
			return "", fmt.Errorf("vault locked; unlock it in the app")
		}
		return config.Decrypt(d.MasterKey, s.Name+"/"+field, config.AADFor(d.File, s, field), blob)
	}
	pw, err := dec("encPassword", s.EncPassword)
	if err != nil {
		return sshx.DialConfig{}, err
	}
	su, err := dec("encSuPassword", s.EncSuPassword)
	if err != nil {
		return sshx.DialConfig{}, err
	}
	sudo, err := dec("encSudoPassword", s.EncSudoPassword)
	if err != nil {
		return sshx.DialConfig{}, err
	}
	passphrase, err := dec("encKeyPassphrase", s.EncKeyPassphrase)
	if err != nil {
		return sshx.DialConfig{}, err
	}
	dc := sshx.DialConfig{
		Host: s.Host, Port: s.Port, User: s.User, Password: pw, Auth: s.Auth,
		SuPassword: su, SudoPassword: sudo, Passphrase: passphrase, HostKey: s.HostKey, Insecure: d.Insecure,
		TimeoutMs: 60000,
	}
	if s.KeyPath != "" {
		data, err := os.ReadFile(expandPath(s.KeyPath))
		if err != nil {
			return sshx.DialConfig{}, fmt.Errorf("reading key file %q: %w", s.KeyPath, err)
		}
		dc.PrivateKey = string(data)
	}
	host, port := dc.Host, dc.Port
	dc.OnLearnHostKey = func(fp string) {
		_ = config.RecordHostKey(d.Path, name, host, port, fp, "", d.MasterKey)
	}
	return dc, nil
}
