// Package touchid unlocks the vault with Touch ID: it runs sshgate-touchid,
// a Swift helper that seals the master password to a Secure Enclave key and
// opens it behind a fingerprint, keeps the sealed blob in touchid.json, and
// sends the opened password to the hub's unlock itself, so it never reaches
// the views. A port of desktop/src/main/touchid.ts; the rules are in
// docs/superpowers/specs/2026-10-12-touchid-unlock-design.md.
package touchid

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lang315/sshgate/app/door"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// What the person is told, character for character as the Electron app says it.
var (
	ErrReset       = errors.New("Touch ID was turned off because your fingerprints changed or your master password is different. Unlock with your password, then turn it on again in Settings.")
	ErrUnavailable = errors.New("Touch ID is not available right now. Unlock with your password.")
	ErrFailed      = errors.New("Touch ID failed.")
	ErrBusy        = errors.New("Touch ID is already asking")
	ErrOff         = errors.New("Touch ID is not turned on")
	// ErrInterrupted is the reason Cancel gets when the hub stops under a dialog.
	ErrInterrupted = errors.New("Touch ID was interrupted because the app restarted its background process. Unlock again.")
)

// wrongPassword is the hub's answer to a wrong master password.
const wrongPassword = "wrong master password"

// Hub is the part of the UI door the manager calls.
type Hub interface {
	Status(ctx context.Context) (hubapi.Status, error)
	Unlock(ctx context.Context, password string) (hubapi.UnlockReply, error)
}

// Result is a Touch ID unlock: Cancelled when the person dismissed the
// dialog (nothing to show), else the hub's reply.
type Result struct {
	Cancelled bool
	Reply     hubapi.UnlockReply
}

// HelperPath is the helper to run, "" when there is none. Like the hub's
// launch, SSHGATE_TOUCHID_HELPER is a dev and test knob that a packaged app
// ignores: whoever sets the environment could otherwise swap the helper that
// receives the master password. Otherwise it is sshgate-touchid in
// Resources when packaged, beside the module root when not, on macOS only.
func HelperPath(env []string, packaged bool, resources, repoRoot, goos string, exists func(string) bool) string {
	if !packaged {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "SSHGATE_TOUCHID_HELPER="); ok && v != "" {
				return v
			}
		}
	}
	if goos != "darwin" {
		return ""
	}
	dir := repoRoot
	if packaged {
		dir = resources
	}
	if p := filepath.Join(dir, "sshgate-touchid"); exists(p) {
		return p
	}
	return ""
}

// Manager runs the helper. Helper "" means Touch ID is not supported here:
// every call answers so and nothing is spawned. File is touchid.json.
type Manager struct {
	Helper string
	File   string
	// Hub returns the hub's client now; it changes when the hub restarts.
	Hub func() Hub

	mu        sync.Mutex
	running   *exec.Cmd
	busy      bool
	gen       int   // bumped by Cancel: an unlock begun before it stops at its next step
	cancelErr error // why, when Cancel gave a reason
}

// run is one helper process. A helper that cannot start counts as 4 (not
// available), one killed by a signal as 1; Cancel is told apart by gen, not
// by the code. track makes Cancel able to kill it.
type run struct {
	code   int
	stdout []byte
	stderr string
}

func (m *Manager) run(args []string, input []byte, track bool) run {
	cmd := exec.Command(m.Helper, args...)
	var out, errb bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(input), &out, &errb
	if err := cmd.Start(); err != nil {
		return run{code: 4}
	}
	if track {
		m.mu.Lock()
		m.running = cmd
		m.mu.Unlock()
	}
	err := cmd.Wait()
	if track {
		m.mu.Lock()
		if m.running == cmd {
			m.running = nil
		}
		m.mu.Unlock()
	}
	code := 0
	if err != nil {
		code = 1
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() > 0 {
			code = ee.ExitCode()
		}
	}
	return run{code: code, stdout: out.Bytes(), stderr: errb.String()}
}

// wipe zeroes bytes that held the password. A Go string cannot be wiped; the
// bytes can.
func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// file is touchid.json: the sealed blob for one vault.
type file struct {
	StorePath string          `json:"storePath"`
	Sealed    json.RawMessage `json:"sealed"`
}

// read is the sealed blob for this vault, or nil: no file, a corrupt one, one
// for another store, or a blob that is not a version 1 object.
func (m *Manager) read(storePath string) json.RawMessage {
	b, err := os.ReadFile(m.File)
	if err != nil {
		return nil
	}
	var f file
	var v struct {
		V int `json:"v"`
	}
	if json.Unmarshal(b, &f) != nil || f.StorePath != storePath || json.Unmarshal(f.Sealed, &v) != nil || v.V != 1 {
		return nil
	}
	return f.Sealed
}

// Status says whether Touch ID can be offered. A hub that is not running
// reads as not enabled.
func (m *Manager) Status(ctx context.Context) model.TouchStatus {
	if m.Helper == "" {
		return model.TouchStatus{}
	}
	st := model.TouchStatus{Supported: true, Available: m.run([]string{"available"}, nil, false).code == 0}
	if s, err := m.Hub().Status(ctx); err == nil {
		st.Enabled = m.read(s.StorePath) != nil
	}
	return st
}

// Enable checks the master password with the hub, seals it, and writes
// touchid.json (0600, atomically). A wrong password writes nothing.
func (m *Manager) Enable(ctx context.Context, password string) error {
	if password == "" {
		return errors.New("Type your master password")
	}
	if m.Helper == "" {
		return ErrUnavailable
	}
	hub := m.Hub()
	s, err := hub.Status(ctx)
	if err != nil {
		return err
	}
	// With no vault the hub's unlock accepts any password, so it proves nothing.
	if !s.HasVault {
		return errors.New("create a vault first")
	}
	if _, err := hub.Unlock(ctx, password); err != nil { // checks it; a wrong one leaves the hub as it was
		return err
	}
	input := []byte(password)
	r := m.run([]string{"enroll"}, input, false)
	wipe(input)
	if r.code == 4 {
		return ErrUnavailable
	}
	var sealed json.RawMessage
	if r.code != 0 || json.Unmarshal(r.stdout, &sealed) != nil || len(sealed) == 0 || sealed[0] != '{' {
		fmt.Fprintf(os.Stderr, "sshgate: touchid enroll failed: %d %s\n", r.code, r.stderr)
		return ErrFailed
	}
	b, err := json.Marshal(file{StorePath: s.StorePath, Sealed: sealed})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.File), 0o700); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", m.File, os.Getpid())
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.File)
}

// Disable deletes touchid.json; it needs no password.
func (m *Manager) Disable() error {
	if err := os.Remove(m.File); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Unlock opens the sealed password behind a fingerprint and sends it to the
// hub's unlock. The helper's exit codes: 2 the person cancelled (the file
// stays), 3 the key no longer works (the file goes), 4 Touch ID is not usable
// now, anything else failed. The hub saying wrong master password also
// deletes the file. One helper at a time.
func (m *Manager) Unlock(ctx context.Context) (Result, error) {
	m.mu.Lock()
	if m.busy {
		m.mu.Unlock()
		return Result{}, ErrBusy
	}
	if m.Helper == "" {
		m.mu.Unlock()
		return Result{}, ErrUnavailable
	}
	m.busy = true
	gen := m.gen
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.busy = false; m.mu.Unlock() }()

	hub := m.Hub()
	s, err := hub.Status(ctx)
	if err != nil {
		return Result{}, err
	}
	sealed := m.read(s.StorePath)
	if sealed == nil {
		return Result{}, ErrOff
	}
	if done, res, err := m.stopped(gen); done {
		return res, err
	}
	r := m.run([]string{"unlock"}, sealed, true)
	defer wipe(r.stdout)
	if done, res, err := m.stopped(gen); done {
		return res, err
	}
	switch r.code {
	case 0:
	case 2:
		return Result{Cancelled: true}, nil
	case 3:
		_ = m.Disable()
		return Result{}, ErrReset
	case 4:
		return Result{}, ErrUnavailable
	default:
		fmt.Fprintf(os.Stderr, "sshgate: touchid unlock failed: %d %s\n", r.code, r.stderr)
		return Result{}, ErrFailed
	}
	reply, err := hub.Unlock(ctx, string(r.stdout))
	var de *door.Error
	if errors.As(err, &de) && de.Message == wrongPassword {
		_ = m.Disable()
		return Result{}, ErrReset
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Reply: reply}, nil
}

// stopped is whether a Cancel landed since gen was read, and what it means:
// its reason, or a cancel the person is shown nothing for.
func (m *Manager) stopped(gen int) (bool, Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.gen == gen:
		return false, Result{}, nil
	case m.cancelErr != nil:
		return true, Result{}, m.cancelErr
	}
	return true, Result{Cancelled: true}, nil
}

// Cancel closes a Touch ID dialog still up and stops an unlock that has not
// started its helper yet: a typed password won (no reason: the person sees
// nothing), or the hub stopped (reason is the error shown).
func (m *Manager) Cancel(reason error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gen++
	m.cancelErr = reason
	if m.running != nil {
		_ = m.running.Process.Kill()
	}
}
