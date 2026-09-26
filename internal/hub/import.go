package hub

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/sshconfig"
	"github.com/lang315/sshgate/internal/sshx"
)

var errNoSSH = errors.New("OpenSSH client (ssh) not found")

type importScan struct {
	Candidates []sshconfig.Candidate `json:"candidates"`
	Note       string                `json:"note,omitempty"`
}

type importSkip struct {
	Alias  string `json:"alias"`
	Reason string `json:"reason"`
}

func (h *Hub) sshConfigPath() string {
	if h.o.SSHConfigPath != "" {
		return h.o.SSHConfigPath
	}
	return filepath.Join(sshconfig.HomeDir(), ".ssh", "config")
}

func (h *Hub) serverExists(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.deps.File == nil {
		return false
	}
	_, ok := h.deps.File.FindServer(name)
	return ok
}

// candidates resolves each alias with ssh -G and looks a ready one's key up
// in the known_hosts files ssh itself would read. Nothing here dials.
func (h *Hub) candidates(ctx context.Context, aliases []string) ([]sshconfig.Candidate, error) {
	// LookPath, never a shell: a shell function or alias named ssh is not the client.
	bin, err := exec.LookPath("ssh")
	if err != nil {
		return nil, errNoSSH
	}
	out := []sshconfig.Candidate{}
	for _, a := range aliases {
		r, err := sshconfig.Resolve(ctx, bin, h.o.SSHConfigPath, a)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hub: ssh -G %q: %v\n", a, err)
			out = append(out, sshconfig.Candidate{Alias: a, Status: "skipped", Reason: "ssh -G failed"})
			continue
		}
		c := sshconfig.Check(r, h.serverExists)
		if c.Status == "ready" {
			name, port := r.KnownHostsName()
			c.HostKeyAlgo, c.HostKey, _ = sshx.KnownHostKey(r.KnownHostsFiles, name, port)
		}
		out = append(out, c)
	}
	return out, nil
}

// ImportScan lists every alias in the SSH config as an import candidate. It
// needs an unlocked vault, like every host write.
func (h *Hub) ImportScan(ctx context.Context) (importScan, error) {
	key, err := h.writeKey()
	if err != nil {
		return importScan{}, err
	}
	clear(key)
	path := h.sshConfigPath()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return importScan{Candidates: []sshconfig.Candidate{}, Note: "No ~/.ssh/config"}, nil
	}
	aliases, err := sshconfig.Aliases(path)
	if err != nil {
		return importScan{}, err
	}
	cs, err := h.candidates(ctx, aliases)
	return importScan{Candidates: cs}, err
}

// ImportApply adds the named aliases that are ready now. The renderer sends
// names only; host, user, key path and pin are recomputed here. Every server
// lands in one write, or none does.
func (h *Hub) ImportApply(ctx context.Context, names []string) (imported []string, skipped []importSkip, err error) {
	imported, skipped = []string{}, []importSkip{}
	key, err := h.writeKey()
	if err != nil {
		return nil, nil, err
	}
	clear(key) // taken again for the write: the vault may lock while ssh -G runs
	all, err := sshconfig.Aliases(h.sshConfigPath())
	if err != nil {
		return nil, nil, err
	}
	var want []string
	for _, n := range names {
		switch {
		case !slices.Contains(all, n):
			skipped = append(skipped, importSkip{n, "not in the SSH config"})
		case !slices.Contains(want, n):
			want = append(want, n)
		}
	}
	cs, err := h.candidates(ctx, want)
	if err != nil {
		return nil, nil, err
	}
	var ready []sshconfig.Candidate
	for _, c := range cs {
		if c.Status == "ready" {
			ready = append(ready, c)
		} else {
			skipped = append(skipped, importSkip{c.Alias, c.Reason})
		}
	}
	if len(ready) == 0 {
		return imported, skipped, nil
	}
	if key, err = h.writeKey(); err != nil {
		return nil, nil, err
	}
	defer clear(key)
	var done []sshconfig.Candidate
	var late []importSkip
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		done, late = nil, nil
		for _, c := range ready {
			if _, dup := f.FindServer(c.Alias); dup { // added since the scan
				late = append(late, importSkip{c.Alias, "Already in vault"})
				continue
			}
			in := config.ServerInput{Name: c.Alias, Host: c.Host, Port: c.Port, User: c.User, Auth: c.Auth, KeyPath: c.KeyPath}
			if _, _, err := config.ApplyServer(f, "", in, key); err != nil {
				return err
			}
			s := &f.Servers[slices.IndexFunc(f.Servers, func(s config.Server) bool { return s.Name == c.Alias })]
			s.HostKey, s.HostKeyAlgo = c.HostKey, c.HostKeyAlgo
			done = append(done, c)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	skipped = append(skipped, late...)
	reloadErr := h.Reload()
	for _, c := range done {
		imported = append(imported, c.Alias)
		h.auditConfig(broker.ConfigRecord{Action: "import", Server: c.Alias, Host: c.Host, Port: c.Port, Fingerprint: c.HostKey, Algo: c.HostKeyAlgo})
	}
	return imported, skipped, reloadErr // the write itself succeeded
}
