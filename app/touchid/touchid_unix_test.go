//go:build unix

package touchid

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/door"
	"github.com/lang315/sshgate/app/hubapi"
)

// fakeHub is the hub's status and unlock: password is the right one.
type fakeHub struct {
	password string
	mu       sync.Mutex
	status   hubapi.Status
	statusFn func() error // runs before status answers
	unlocks  []string
	reply    hubapi.UnlockReply
	down     bool
}

func (h *fakeHub) Status(context.Context) (hubapi.Status, error) {
	if h.statusFn != nil {
		if err := h.statusFn(); err != nil {
			return hubapi.Status{}, err
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.down {
		return hubapi.Status{}, errors.New("the hub is not running")
	}
	return h.status, nil
}

func (h *fakeHub) Unlock(_ context.Context, pw string) (hubapi.UnlockReply, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.unlocks = append(h.unlocks, pw)
	if pw != h.password {
		return hubapi.UnlockReply{}, &door.Error{Code: -32000, Message: "wrong master password"}
	}
	return h.reply, nil
}

func (h *fakeHub) unlocked() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.unlocks...)
}

// rig is a Manager on the fake helper, with its own touchid.json and mode file.
type rig struct {
	m    *Manager
	hub  *fakeHub
	mode string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	helper, err := filepath.Abs("testdata/fake-touchid.sh")
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{hub: &fakeHub{password: "pw", status: hubapi.Status{HasVault: true, StorePath: "/vault/servers.json"}}, mode: filepath.Join(dir, "mode")}
	t.Setenv("FAKE_TOUCHID_MODE", r.mode)
	r.m = &Manager{Helper: helper, File: filepath.Join(dir, "userdata", "touchid.json"), Hub: func() Hub { return r.hub }}
	return r
}

func (r *rig) setMode(t *testing.T, mode string) {
	t.Helper()
	if err := os.WriteFile(r.mode, []byte(mode), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) enable(t *testing.T) {
	t.Helper()
	if err := r.m.Enable(context.Background(), r.hub.password); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) fileExists() bool { _, err := os.Stat(r.m.File); return err == nil }

func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); !cond(); {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func TestUnsupportedSpawnsNothingAndCallsNoHub(t *testing.T) {
	m := &Manager{File: filepath.Join(t.TempDir(), "touchid.json"), Hub: func() Hub { t.Fatal("the hub was called"); return nil }}
	if st := m.Status(context.Background()); st.Supported || st.Available || st.Enabled {
		t.Errorf("status %+v", st)
	}
	if err := m.Enable(context.Background(), "pw"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Enable: %v", err)
	}
	if _, err := m.Unlock(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Unlock: %v", err)
	}
}

func TestStatus(t *testing.T) {
	r := newRig(t)
	if st := r.m.Status(context.Background()); !st.Supported || !st.Available || st.Enabled {
		t.Errorf("fresh: %+v", st)
	}
	r.setMode(t, "unavailable")
	if st := r.m.Status(context.Background()); !st.Supported || st.Available {
		t.Errorf("unavailable: %+v", st)
	}
	r.setMode(t, "ok")
	r.enable(t)
	if st := r.m.Status(context.Background()); !st.Enabled {
		t.Errorf("enabled: %+v", st)
	}
	r.hub.down = true
	if st := r.m.Status(context.Background()); !st.Supported || st.Enabled {
		t.Errorf("hub down: %+v", st)
	}
}

func TestEnableChecksThePasswordAndWritesTheFile(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if err := r.m.Enable(ctx, "wrong"); err == nil || err.Error() != "wrong master password" {
		t.Fatalf("a wrong password: %v", err)
	}
	if r.fileExists() {
		t.Fatal("a wrong password wrote the file")
	}
	if err := r.m.Enable(ctx, ""); err == nil || err.Error() != "Type your master password" {
		t.Errorf("an empty password: %v", err)
	}
	r.hub.status.HasVault = false
	if err := r.m.Enable(ctx, "pw"); err == nil || err.Error() != "create a vault first" {
		t.Errorf("no vault: %v", err)
	}
	if got := r.hub.unlocked(); len(got) != 1 || got[0] != "wrong" {
		t.Errorf("the hub was asked %q; an empty password and no vault must not reach it", got)
	}
	r.hub.status.HasVault = true
	r.enable(t)
	fi, err := os.Stat(r.m.File)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file %v, %v", fi, err)
	}
	if st := r.m.Status(ctx); !st.Enabled {
		t.Errorf("status %+v", st)
	}
}

func TestUnlockPassesThePasswordToTheHubByteForByte(t *testing.T) {
	r := newRig(t)
	const pw = "p\u00e4ss w\"or\\d\n" // quotes, a backslash, a non-ASCII rune and a trailing newline survive the helper
	r.hub.password = pw
	r.hub.reply = hubapi.UnlockReply{RanWhileLocked: []hubapi.RanLocked{{Server: "box", Count: 2}}}
	r.enable(t)
	res, err := r.m.Unlock(context.Background())
	if err != nil || res.Cancelled {
		t.Fatalf("%+v, %v", res, err)
	}
	if got := r.hub.unlocked(); len(got) != 2 || got[1] != pw {
		t.Errorf("the hub was asked %q", got)
	}
	if len(res.Reply.RanWhileLocked) != 1 || res.Reply.RanWhileLocked[0].Server != "box" {
		t.Errorf("the hub's reply was not returned as is: %+v", res.Reply)
	}
}

func TestUnlockOutcomes(t *testing.T) {
	for _, tc := range []struct {
		mode      string
		want      error
		cancelled bool
		keepsFile bool
	}{
		{"invalid", ErrReset, false, false},
		{"cancel", nil, true, true},
		{"unavailable", ErrUnavailable, false, true},
		{"fail", ErrFailed, false, true},
		{"crash", ErrFailed, false, true}, // killed by a signal nobody sent: a failure, not a cancel
	} {
		r := newRig(t)
		r.enable(t)
		r.setMode(t, tc.mode)
		res, err := r.m.Unlock(context.Background())
		if !errors.Is(err, tc.want) || res.Cancelled != tc.cancelled || r.fileExists() != tc.keepsFile {
			t.Errorf("%s: %+v, %v, file %v", tc.mode, res, err, r.fileExists())
		}
	}
}

func TestTheHubSayingWrongPasswordTurnsTouchIDOff(t *testing.T) {
	r := newRig(t)
	r.enable(t)
	// The vault's password changed since: the sealed one is now wrong.
	r.hub.status.StorePath = "/vault/servers.json"
	old := r.m.Hub
	r.m.Hub = func() Hub { return rejecting{old()} }
	if _, err := r.m.Unlock(context.Background()); !errors.Is(err, ErrReset) {
		t.Fatalf("got %v", err)
	}
	if r.fileExists() {
		t.Error("the file stayed")
	}
}

// rejecting answers every unlock with the hub's wrong master password.
type rejecting struct{ Hub }

func (rejecting) Unlock(context.Context, string) (hubapi.UnlockReply, error) {
	return hubapi.UnlockReply{}, &door.Error{Message: "wrong master password"}
}

func TestOtherHubErrorsPassThroughAndKeepTheFile(t *testing.T) {
	r := newRig(t)
	r.enable(t)
	r.m.Hub = func() Hub { return failing{r.hub} }
	if _, err := r.m.Unlock(context.Background()); err == nil || err.Error() != "vault is locked by another process" || !r.fileExists() {
		t.Fatalf("%v, file %v", err, r.fileExists())
	}
}

type failing struct{ Hub }

func (failing) Unlock(context.Context, string) (hubapi.UnlockReply, error) {
	return hubapi.UnlockReply{}, errors.New("vault is locked by another process")
}

func TestAnotherStoreACorruptFileOrABlobWithoutVersionOneIsNotEnabled(t *testing.T) {
	r := newRig(t)
	r.enable(t)
	for name, content := range map[string]string{
		"another store": `{"storePath":"/other/servers.json","sealed":{"v":1}}`,
		"corrupt":       `{not json`,
		"no version":    `{"storePath":"/vault/servers.json","sealed":{"fake":"x"}}`,
		"version two":   `{"storePath":"/vault/servers.json","sealed":{"v":2}}`,
		"null blob":     `{"storePath":"/vault/servers.json","sealed":null}`,
		"string blob":   `{"storePath":"/vault/servers.json","sealed":"v"}`,
	} {
		if err := os.WriteFile(r.m.File, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if st := r.m.Status(context.Background()); st.Enabled {
			t.Errorf("%s: enabled", name)
		}
		if _, err := r.m.Unlock(context.Background()); !errors.Is(err, ErrOff) {
			t.Errorf("%s: Unlock %v", name, err)
		}
		if b, _ := os.ReadFile(r.m.File); string(b) != content {
			t.Errorf("%s: the file was touched", name)
		}
	}
}

func TestUnlockWithTheHubDownFails(t *testing.T) {
	r := newRig(t)
	r.enable(t)
	r.hub.down = true
	if _, err := r.m.Unlock(context.Background()); err == nil || errors.Is(err, ErrOff) {
		t.Errorf("got %v", err)
	}
}

func TestDisableIsIdempotent(t *testing.T) {
	r := newRig(t)
	r.enable(t)
	for i := 0; i < 2; i++ {
		if err := r.m.Disable(); err != nil || r.fileExists() {
			t.Fatalf("Disable %d: %v, file %v", i, err, r.fileExists())
		}
	}
}

// A second unlock while one asks is refused; Cancel ends the first as a
// cancel and kills its helper.
func TestOneHelperAtATimeAndCancelKillsIt(t *testing.T) {
	r := newRig(t)
	r.enable(t)
	r.setMode(t, "slow")
	type out struct {
		res Result
		err error
	}
	done := make(chan out, 1)
	go func() { res, err := r.m.Unlock(context.Background()); done <- out{res, err} }()
	var pid int
	until(t, "the helper to start", func() bool {
		b, err := os.ReadFile(r.mode + ".pid")
		if err != nil || len(b) == 0 {
			return false
		}
		pid = atoi(string(b))
		return pid > 0
	})
	if _, err := r.m.Unlock(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("a second unlock: %v", err)
	}
	r.m.Cancel(nil)
	got := <-done
	if got.err != nil || !got.res.Cancelled {
		t.Errorf("the first: %+v", got)
	}
	until(t, "the helper to die", func() bool { return !alive(pid) })
}

func TestCancelWithAReasonRejectsWithIt(t *testing.T) {
	r := newRig(t)
	r.enable(t)
	r.setMode(t, "slow")
	done := make(chan error, 1)
	go func() { _, err := r.m.Unlock(context.Background()); done <- err }()
	until(t, "the helper to start", func() bool { _, err := os.Stat(r.mode + ".pid"); return err == nil })
	r.m.Cancel(ErrInterrupted)
	if err := <-done; !errors.Is(err, ErrInterrupted) {
		t.Errorf("got %v", err)
	}
}

// A Cancel that lands while the hub's status is still being read stops the
// unlock before its helper starts.
func TestCancelBeforeTheHelperStartsStopsIt(t *testing.T) {
	r := newRig(t)
	r.enable(t)
	r.setMode(t, "slow")
	asked, release := make(chan struct{}), make(chan struct{})
	r.hub.statusFn = func() error { close(asked); <-release; return nil }
	done := make(chan Result, 1)
	go func() { res, _ := r.m.Unlock(context.Background()); done <- res }()
	<-asked
	r.m.Cancel(nil)
	r.hub.statusFn = nil
	close(release)
	if res := <-done; !res.Cancelled {
		t.Errorf("got %+v", res)
	}
	if _, err := os.Stat(r.mode + ".pid"); err == nil {
		t.Error("the helper ran")
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// A Cancel that lands after the unlock's last stopped() check, just before the
// helper starts, must keep the helper from starting at all: run is handed the
// generation it began under.
func TestRunDoesNotStartAHelperAfterACancel(t *testing.T) {
	r := newRig(t)
	r.setMode(t, "slow")
	gen := r.m.gen
	r.m.Cancel(nil)
	if got := r.m.run([]string{"unlock"}, nil, true, gen); got.code == 0 {
		t.Errorf("got %+v", got)
	}
	if _, err := os.Stat(r.mode + ".pid"); err == nil {
		t.Error("the helper ran")
	}
	if r.m.running != nil {
		t.Error("a helper is registered")
	}
}
