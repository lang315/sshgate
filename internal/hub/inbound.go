package hub

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"golang.org/x/crypto/ssh"
)

// Inbound SSH (spec 2026-11-20-inbound-ssh-design.md): the vault holds the
// devices, and the hub renders them into a marked block of authorized_keys,
// one forced-command line per device that has an allowed IP. Lines outside the
// block are never read into the result or changed.
const (
	inboundBegin = "# sshgate inbound begin"
	inboundEnd   = "# sshgate inbound end"
)

type uiInbound struct {
	Name        string   `json:"name"`
	PublicKey   string   `json:"publicKey"`
	Fingerprint string   `json:"fingerprint"`
	Allowed     []string `json:"allowed"`
	Blocked     []string `json:"blocked"`
	Created     string   `json:"created"`
}

type inboundList struct {
	Devices        []uiInbound `json:"devices"`
	AuthorizedKeys string      `json:"authorizedKeys"`
	FileError      string      `json:"fileError,omitempty"` // the last render failed; the file does not match the list
}

func (h *Hub) authorizedKeysPath() string {
	if h.o.AuthorizedKeysPath != "" {
		return h.o.AuthorizedKeysPath
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", "authorized_keys")
}

// inboundCommand is the forced command's program, as written into the line.
func (h *Hub) inboundCommand() (string, error) {
	p := h.o.InboundBinary
	if p == "" {
		var err error
		if p, err = os.Executable(); err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("the sshgate binary path is not absolute")
	}
	// The line is parsed by sshd, then the command by the login shell.
	if strings.ContainsAny(p, "\"\\'$`") || strings.ContainsFunc(p, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return "", errors.New("the sshgate binary path has a character that cannot go in an authorized_keys line")
	}
	return "'" + p + "'", nil
}

func inboundLines(devs []config.InboundDevice, command string) []string {
	var out []string
	for _, d := range devs {
		if len(d.Allowed) == 0 {
			continue // no allowed IP, no line: the device cannot log in
		}
		from := slices.Clone(d.Allowed)
		for _, b := range d.Blocked {
			from = append(from, "!"+b) // sshd: a negated match wins over any positive one
		}
		out = append(out, fmt.Sprintf(`restrict,pty,from="%s",command="%s inbound %s" %s`,
			strings.Join(from, ","), command, d.Name, d.PublicKey))
	}
	return out
}

// ourLineRE matches what the hub writes inside the block.
var ourLineRE = regexp.MustCompile(`^restrict,pty,from="[^"]*",command="[^"]* inbound [^ "]+" `)

func ourLine(l string) bool {
	t := strings.TrimSpace(l)
	return t == "" || strings.HasPrefix(t, "#") || ourLineRE.MatchString(t)
}

// outsideKeyConflicts names the devices whose key is on a line outside the block.
func outsideKeyConflicts(data []byte, devs []config.InboundDevice) error {
	keys := map[string]string{}
	for _, d := range devs {
		if k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(d.PublicKey)); err == nil {
			keys[string(k.Marshal())] = d.Name
		}
	}
	var names []string
	in := false
	for _, l := range strings.Split(string(data), "\n") {
		switch t := strings.TrimSpace(l); {
		case t == inboundBegin:
			in = true
		case t == inboundEnd:
			in = false
		case !in:
			if k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(l)); err == nil {
				if n, ok := keys[string(k.Marshal())]; ok && !slices.Contains(names, n) {
					names = append(names, n)
				}
			}
		}
	}
	var msgs []string
	for _, n := range names {
		msgs = append(msgs, fmt.Sprintf("the key of %s is also on a line outside sshgate's block; remove that line", n))
	}
	if len(msgs) == 0 {
		return nil
	}
	return errors.New(strings.Join(msgs, "; "))
}

// spliceInbound replaces the marked block in existing with lines, or appends
// the block when there is none. Everything outside it stays byte for byte.
// With no block and no lines it returns existing unchanged.
func spliceInbound(existing []byte, lines []string) ([]byte, error) {
	var block bytes.Buffer
	block.WriteString(inboundBegin + "\n")
	for _, l := range lines {
		block.WriteString(l + "\n")
	}
	block.WriteString(inboundEnd + "\n")

	var begin, end = -1, -1
	for off := 0; off < len(existing); {
		nl := bytes.IndexByte(existing[off:], '\n')
		next := len(existing)
		if nl >= 0 {
			next = off + nl + 1
		}
		switch line := strings.TrimRight(string(existing[off:next]), "\r\n"); {
		case begin >= 0 && end < 0 && line != inboundEnd && !ourLine(line):
			return nil, errors.New("authorized_keys has a line of yours inside sshgate's block; move it out of the block")
		case line == inboundBegin:
			if begin >= 0 {
				return nil, errors.New("authorized_keys has two sshgate begin markers")
			}
			begin = off
		case line == inboundEnd:
			if begin < 0 || end >= 0 {
				return nil, errors.New("authorized_keys has a stray sshgate end marker")
			}
			end = next
		}
		off = next
	}
	switch {
	case begin >= 0 && end < 0:
		return nil, errors.New("authorized_keys has a begin marker with no end marker")
	case begin >= 0:
		out := append(bytes.Clone(existing[:begin]), block.Bytes()...)
		return append(out, existing[end:]...), nil
	case len(lines) == 0:
		return existing, nil
	}
	out := bytes.Clone(existing)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, block.Bytes()...), nil
}

// writeAuthorizedKeys renders lines into path atomically (0600 temp file in
// the same folder, then rename). A link or a file of another user is refused.
// A device key found on a line outside the block is reported after the block
// is written; that line is never touched.
func writeAuthorizedKeys(path string, lines []string, devs []config.InboundDevice) error {
	var existing []byte
	fi, err := os.Lstat(path)
	switch {
	case err == nil:
		if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
			return errors.New("authorized_keys is a link or not a regular file")
		}
		if !ownedByUs(fi) {
			return errors.New("authorized_keys is not owned by you")
		}
		if existing, err = os.ReadFile(path); err != nil {
			return err
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	out, err := spliceInbound(existing, lines)
	if err != nil {
		return err
	}
	conflict := outsideKeyConflicts(existing, devs)
	if fi != nil && bytes.Equal(out, existing) || fi == nil && len(lines) == 0 {
		return conflict
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".authorized_keys.sshgate-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // fails harmlessly after the rename
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return conflict
}

// renderInboundLocked rewrites the file from devs and remembers the outcome
// for inbound.list. Callers hold h.mu.
func (h *Hub) renderInboundLocked(devs []config.InboundDevice) error {
	var lines []string
	cmd, err := h.inboundCommand()
	if err == nil {
		lines = inboundLines(devs, cmd)
	} else if slices.ContainsFunc(devs, func(d config.InboundDevice) bool { return len(d.Allowed) > 0 }) {
		h.inboundErr = err.Error()
		return err
	}
	if err = writeAuthorizedKeys(h.authorizedKeysPath(), lines, devs); err != nil {
		h.inboundErr = err.Error()
		return err
	}
	h.inboundErr = ""
	return nil
}

// ListInbound needs an unlocked vault.
func (h *Hub) ListInbound() (inboundList, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	k, err := h.writeKeyLocked()
	if err != nil {
		return inboundList{}, err
	}
	clear(k)
	_ = h.renderInboundLocked(h.deps.File.Inbound) // reconcile; a failure shows as fileError
	out := inboundList{Devices: []uiInbound{}, AuthorizedKeys: h.authorizedKeysPath(), FileError: h.inboundErr}
	for _, d := range h.deps.File.Inbound {
		out.Devices = append(out.Devices, uiInbound{d.Name, d.PublicKey, config.InboundFingerprint(d.PublicKey),
			slices.Clone(nonNil(d.Allowed)), slices.Clone(nonNil(d.Blocked)), d.Created})
	}
	return out, nil
}

// writeInbound holds h.mu across the store write, the file render and the
// reload, so they serialize with every other writer. fn returns the audit
// record. A render failure after a good write is reported with the
// "saved, but" prefix; the write stands.
func (h *Hub) writeInbound(fn func(f *config.File) (broker.ConfigRecord, error)) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	mk, err := h.writeKeyLocked()
	if err != nil {
		return err
	}
	defer clear(mk)
	var rec broker.ConfigRecord
	var devs []config.InboundDevice
	err = config.Update(h.o.StorePath, mk, func(f *config.File) (err error) {
		if rec, err = fn(f); err == nil {
			devs = f.Inbound
		}
		return err
	})
	if err != nil {
		return err
	}
	renderErr := h.renderInboundLocked(devs)
	reloadErr := h.reloadLocked()
	h.auditConfig(rec)
	if renderErr != nil {
		return fmt.Errorf("saved, but authorized_keys was not updated: %w", renderErr)
	}
	return reloadErr
}

func (h *Hub) AddInbound(name, publicKey, ip string) error {
	return h.writeInbound(func(f *config.File) (broker.ConfigRecord, error) {
		if err := config.AddInbound(f, name, publicKey, ip, time.Now()); err != nil {
			return broker.ConfigRecord{}, err
		}
		d := f.Inbound[len(f.Inbound)-1]
		return broker.ConfigRecord{Action: "inboundAdd", Device: name, IP: d.Allowed[0], Fingerprint: config.InboundFingerprint(d.PublicKey)}, nil
	})
}

func (h *Hub) DeleteInbound(name string) error {
	return h.writeInbound(func(f *config.File) (broker.ConfigRecord, error) {
		return broker.ConfigRecord{Action: "inboundDelete", Device: name}, config.DeleteInbound(f, name)
	})
}

var inboundActions = map[string]string{"allowed": "inboundAllow", "blocked": "inboundBlock", "removed": "inboundForget"}

func (h *Hub) SetInboundIP(name, ip, state string) error {
	return h.writeInbound(func(f *config.File) (broker.ConfigRecord, error) {
		canon, err := config.SetInboundIP(f, name, ip, state)
		return broker.ConfigRecord{Action: inboundActions[state], Device: name, IP: canon}, err
	})
}
