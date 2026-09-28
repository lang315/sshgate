package hub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/sshx"
)

// blockExec's Exec blocks until its ctx is cancelled (returning
// sshx.ErrCancelled) or release is closed (returning a zero result). Calls
// is appended under a mutex since TestAutoExecCap drives it concurrently.
type blockExec struct {
	release chan struct{}

	mu    sync.Mutex
	calls []string
}

func newBlockExec() *blockExec { return &blockExec{release: make(chan struct{})} }

func (b *blockExec) Exec(ctx context.Context, cmd string) (sshx.ExecResult, error) {
	b.mu.Lock()
	b.calls = append(b.calls, cmd)
	b.mu.Unlock()
	select {
	case <-ctx.Done():
		return sshx.ExecResult{}, sshx.ErrCancelled
	case <-b.release:
		return sshx.ExecResult{}, nil
	}
}

func (b *blockExec) ExecSudo(ctx context.Context, cmd string) (sshx.ExecResult, error) {
	return b.Exec(ctx, cmd)
}

// decideExec runs h.Exec on its own goroutine, waits for it to reach the
// broker, denies it, and returns its error. Used by the auto-path tests
// whose autoStart refuses the grant (expired, mismatch, cap, sudo) so the
// request falls through to ordinary approval.
func decideExec(t *testing.T, h *Hub, r ExecRequest) error {
	t.Helper()
	errc := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), r)
		errc <- err
	}()
	waitPending(t, h.Broker(), 1)
	h.Broker().Decide(h.Broker().Pending()[0].ID, broker.Decision{Outcome: broker.Denied, Reason: "no"})
	return <-errc
}

// waitInflight waits for name's grant to have exactly n auto runs in flight.
func waitInflight(t *testing.T, h *Hub, name string, n int) {
	t.Helper()
	waitFor(t, "inflight count", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		g := h.grants[name]
		return g != nil && len(g.inflight) == n
	})
}

// addServer adds s to the vault via config.Update, then reloads h. Used for
// servers Task 1's newHub fixture doesn't have (root login, su/sudo
// passwords): only presence of Enc*Password is checked by autoRefusal, so
// any non-empty string does for those fields.
func addServer(t *testing.T, h *Hub, path string, s config.Server) {
	t.Helper()
	if err := config.Update(path, testMK, func(f *config.File) error {
		f.Servers = append(f.Servers, s)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
}

func grantOf(h *Hub, name string) *grant {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.grants[name]
}

func hasAutoAllowOff(recs []map[string]any, reason string) bool {
	for _, r := range recs {
		if r["action"] == "autoAllowOff" && r["reason"] == reason {
			return true
		}
	}
	return false
}

// findAutoRecord returns the exec audit record for an auto-allowed run (no
// "kind" field: that's config/file/tunnel records only; approval "auto").
// A test whose grant ends in the same window (Lock, sweepGrants) can't just
// take the last record: the grant's own "autoAllowOff" config record races
// the exec's record, so either can land last.
func findAutoRecord(t *testing.T, recs []map[string]any) map[string]any {
	t.Helper()
	for _, r := range recs {
		if r["kind"] == nil && r["approval"] == "auto" {
			return r
		}
	}
	t.Fatalf("no auto-allow exec record found: %v", recs)
	return nil
}

func TestSetAutoAllowTimed(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	before := time.Now()
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	g := grantOf(h, "vis")
	if g == nil {
		t.Fatal("no grant")
	}
	want := before.Add(15 * time.Minute)
	if diff := g.until.Sub(want); diff < -5*time.Second || diff > 5*time.Second {
		t.Fatalf("until = %v, want ~%v", g.until, want)
	}
	if len(g.inflight) != 0 {
		t.Fatalf("inflight = %v", g.inflight)
	}
	_, recs := readAudit(t, path)
	last := recs[len(recs)-1]
	if last["kind"] != "config" || last["action"] != "autoAllowOn" || last["server"] != "vis" {
		t.Fatalf("audit: %v", last)
	}
	if _, ok := last["until"]; !ok {
		t.Fatalf("audit missing until: %v", last)
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := f.FindServer("vis")
	if s.AutoAllow {
		t.Fatal("store flag set for a timed grant")
	}
}

func TestSetAutoAllowForeverAndResume(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	s, ok := h.Deps().File.FindServer("vis")
	if !ok || !s.AutoAllow {
		t.Fatalf("flag not set: %+v", s)
	}
	g := grantOf(h, "vis")
	if g == nil || !g.until.IsZero() {
		t.Fatalf("grant = %+v", g)
	}
	_, recs := readAudit(t, path)
	last := recs[len(recs)-1]
	if last["action"] != "autoAllowOn" || last["forever"] != true {
		t.Fatalf("audit: %v", last)
	}

	h.Lock()
	unlockForTest(h)
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant survived lock/unlock")
	}
	s, _ = h.Deps().File.FindServer("vis")
	if !s.AutoAllow {
		t.Fatal("flag cleared by lock")
	}

	revBefore := h.Deps().File.Revision
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	g = grantOf(h, "vis")
	if g == nil || !g.until.IsZero() {
		t.Fatal("resume did not arm a grant")
	}
	if h.Deps().File.Revision != revBefore {
		t.Fatal("resume wrote the store")
	}
	_, recs = readAudit(t, path)
	last = recs[len(recs)-1]
	if last["action"] != "autoAllowResume" {
		t.Fatalf("audit: %v", last)
	}
}

func TestSetAutoAllowTimedClearsForeverFlag(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	if err := h.SetAutoAllow("vis", "30m"); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	s, _ := h.Deps().File.FindServer("vis")
	if s.AutoAllow {
		t.Fatal("flag still set")
	}
	g := grantOf(h, "vis")
	if g == nil || g.until.IsZero() {
		t.Fatal("grant not timed")
	}
}

func TestSetAutoAllowRefusals(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	addServer(t, h, path, config.Server{Name: "root", Host: "h", Port: 22, User: "root", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true})
	addServer(t, h, path, config.Server{Name: "suPw", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true, EncSuPassword: "x"})
	addServer(t, h, path, config.Server{Name: "sudoPw", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true, EncSudoPassword: "x"})

	cases := []struct {
		name   string
		locked bool
		server string
		want   string
	}{
		{"locked", true, "vis", ErrLocked.Error()},
		{"nokey", false, "nokey", ErrNoHostKey.Error()},
		{"hidden", false, "hid", serverNotFound("hid").Error()},
		{"ghost", false, "ghost", serverNotFound("ghost").Error()},
		{"root", false, "root", "auto-allow refused: root login"},
		{"suPassword", false, "suPw", "auto-allow refused: has an su password"},
		{"sudoPassword", false, "sudoPw", "auto-allow refused: has a sudo password"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.locked {
				h.Lock()
				defer unlockForTest(h)
			}
			err := h.SetAutoAllow(c.server, "15m")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want containing %q", err, c.want)
			}
			if g := grantOf(h, c.server); g != nil {
				t.Fatal("grant created")
			}
		})
	}
}

func TestAutoAllowOff(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	if err := h.SetAutoAllow("vis", "off"); err != nil {
		t.Fatal(err)
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant survived off")
	}
	_, recs := readAudit(t, path)
	last := recs[len(recs)-1]
	if last["action"] != "autoAllowOff" || last["reason"] != "turned off" {
		t.Fatalf("audit: %v", last)
	}

	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	if err := h.SetAutoAllow("vis", "off"); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	s, _ := h.Deps().File.FindServer("vis")
	if s.AutoAllow {
		t.Fatal("flag not cleared")
	}

	_, recs = readAudit(t, path)
	before := len(recs)
	if err := h.SetAutoAllow("vis", "off"); err != nil {
		t.Fatal(err)
	}
	_, recs = readAudit(t, path)
	if len(recs) != before {
		t.Fatal("off on nothing wrote an audit line")
	}
}

func TestAutoAllowEndsOnLock(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	h.Lock()
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant survived Lock")
	}
	_, recs := readAudit(t, path)
	last := recs[len(recs)-1]
	if last["action"] != "autoAllowOff" || last["reason"] != "locked" {
		t.Fatalf("audit: %v", last)
	}

	// Idle lock too; forever does not hold it off (the timed hold-off is Task 3).
	unlockForTest(h)
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.lastActivity = time.Now().Add(-time.Hour)
	h.mu.Unlock()
	h.lockIfIdle(time.Minute)
	if !h.Locked() {
		t.Fatal("idle lock did not fire")
	}
	h.mu.Lock()
	n := len(h.grants)
	h.mu.Unlock()
	if n != 0 {
		t.Fatal("grants survived idle lock")
	}
}

func TestAutoAllowEndsOnServerWrites(t *testing.T) {
	t.Run("save", func(t *testing.T) {
		h, path := newHub(t, &fakeExec{})
		if err := h.SetAutoAllow("vis", "forever"); err != nil {
			t.Fatal(err)
		}
		in := config.ServerInput{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "agent"}
		if err := h.SaveServer("vis", in); err != nil {
			t.Fatal(err)
		}
		if g := grantOf(h, "vis"); g != nil {
			t.Fatal("grant survived save")
		}
		s, _ := h.Deps().File.FindServer("vis")
		if s.AutoAllow {
			t.Fatal("flag survived save")
		}
		_, recs := readAudit(t, path)
		if !hasAutoAllowOff(recs, "saved") {
			t.Fatalf("no autoAllowOff/saved: %v", recs)
		}
	})
	// Final-fixes item 10: a rename must not leave a grant or flag stranded
	// under either the old or the new name.
	t.Run("rename", func(t *testing.T) {
		h, path := newHub(t, &fakeExec{})
		if err := h.SetAutoAllow("vis", "forever"); err != nil {
			t.Fatal(err)
		}
		in := config.ServerInput{Name: "vis2", Host: "h", Port: 22, User: "u", Auth: "agent"}
		if err := h.SaveServer("vis", in); err != nil {
			t.Fatal(err)
		}
		if g := grantOf(h, "vis"); g != nil {
			t.Fatal("grant survived rename under the old name")
		}
		if g := grantOf(h, "vis2"); g != nil {
			t.Fatal("grant created under the new name")
		}
		s, ok := h.Deps().File.FindServer("vis2")
		if !ok || s.AutoAllow {
			t.Fatalf("vis2 = %+v, ok=%v", s, ok)
		}
		_, recs := readAudit(t, path)
		var found bool
		for _, r := range recs {
			if r["action"] == "autoAllowOff" && r["reason"] == "saved" && r["server"] == "vis" {
				found = true
			}
		}
		if !found {
			t.Fatalf("no autoAllowOff/saved for vis: %v", recs)
		}
	})
	t.Run("delete", func(t *testing.T) {
		h, path := newHub(t, &fakeExec{})
		if err := h.SetAutoAllow("vis", "forever"); err != nil {
			t.Fatal(err)
		}
		if err := h.DeleteServer("vis"); err != nil {
			t.Fatal(err)
		}
		if g := grantOf(h, "vis"); g != nil {
			t.Fatal("grant survived delete")
		}
		_, recs := readAudit(t, path)
		if !hasAutoAllowOff(recs, "deleted") {
			t.Fatalf("no autoAllowOff/deleted: %v", recs)
		}
	})
	t.Run("forgetHostKey", func(t *testing.T) {
		h, path := newHub(t, &fakeExec{})
		if err := h.SetAutoAllow("vis", "forever"); err != nil {
			t.Fatal(err)
		}
		if err := h.ForgetHostKey("vis"); err != nil {
			t.Fatal(err)
		}
		if g := grantOf(h, "vis"); g != nil {
			t.Fatal("grant survived forgetHostKey")
		}
		s, _ := h.Deps().File.FindServer("vis")
		if s.AutoAllow {
			t.Fatal("flag survived forgetHostKey")
		}
		_, recs := readAudit(t, path)
		if !hasAutoAllowOff(recs, "server changed") {
			t.Fatalf("no autoAllowOff/server changed: %v", recs)
		}
	})
}

func TestAutoAllowSink(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	type call struct {
		method string
		params any
	}
	calls := make(chan call, 4)
	release := h.setAutoSink(func(method string, params any) {
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "audit.jsonl"))
		if err != nil {
			t.Error(err)
		}
		if !strings.Contains(string(raw), `"autoAllowOff"`) {
			t.Errorf("sink fired before the audit line was written: %s", raw)
		}
		calls <- call{method, params}
	})
	defer release()
	if err := h.SetAutoAllow("vis", "off"); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-calls:
		if c.method != "autoAllow.off" {
			t.Fatalf("method = %q", c.method)
		}
		p, ok := c.params.(map[string]string)
		if !ok || p["server"] != "vis" || p["reason"] != "turned off" {
			t.Fatalf("params = %+v", c.params)
		}
	case <-time.After(time.Second):
		t.Fatal("sink not called")
	}
}

func TestServersForUIAutoAllow(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	find := func(t *testing.T, name string) uiServer {
		t.Helper()
		for _, s := range h.serversForUI() {
			if s.Name == name {
				return s
			}
		}
		t.Fatalf("%s not found", name)
		return uiServer{}
	}

	vis := find(t, "vis")
	if vis.AutoAllow == nil || vis.AutoAllow.Until == "" || vis.AutoAllow.Forever {
		t.Fatalf("vis = %+v", vis.AutoAllow)
	}
	if _, err := time.Parse(time.RFC3339, vis.AutoAllow.Until); err != nil {
		t.Fatalf("until not RFC3339: %v", err)
	}

	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	vis = find(t, "vis")
	if vis.AutoAllow == nil || !vis.AutoAllow.Forever || vis.AutoAllow.Paused {
		t.Fatalf("vis = %+v", vis.AutoAllow)
	}

	h.Lock()
	unlockForTest(h)
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	vis = find(t, "vis")
	if vis.AutoAllow == nil || !vis.AutoAllow.Forever || !vis.AutoAllow.Paused {
		t.Fatalf("vis after lock/unlock = %+v", vis.AutoAllow)
	}

	hid := find(t, "hid")
	if hid.AutoAllowRefused != "not visible to AI" {
		t.Fatalf("hid refused = %q", hid.AutoAllowRefused)
	}
	nokey := find(t, "nokey")
	if nokey.AutoAllowRefused != "no pinned host key" {
		t.Fatalf("nokey refused = %q", nokey.AutoAllowRefused)
	}
}

// TestSetAutoAllowRaceWindow covers the fix-round-1 finding: the UI door runs
// requests concurrently, so a Lock or a save landing between SetAutoAllow's
// first check and its arming step must void the call instead of racing it.
// beforeArm is the test seam that forces the window.
func TestSetAutoAllowRaceWindow(t *testing.T) {
	t.Run("lock", func(t *testing.T) {
		h, path := newHub(t, &fakeExec{})
		_, before := readAudit(t, path)
		beforeArm = func() { h.Lock() }
		defer func() { beforeArm = nil }()
		err := h.SetAutoAllow("vis", "15m")
		if !errors.Is(err, ErrLocked) {
			t.Fatalf("got %v, want ErrLocked", err)
		}
		if g := grantOf(h, "vis"); g != nil {
			t.Fatal("grant installed on a locked hub")
		}
		_, after := readAudit(t, path)
		if len(after) != len(before) {
			t.Fatalf("audit line written on a raced call: %v", after)
		}
	})

	t.Run("save", func(t *testing.T) {
		h, path := newHub(t, &fakeExec{})
		beforeArm = func() {
			in := config.ServerInput{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "agent", AIVisible: true}
			if err := h.SaveServer("vis", in); err != nil {
				t.Fatal(err)
			}
		}
		defer func() { beforeArm = nil }()
		err := h.SetAutoAllow("vis", "forever")
		if err == nil {
			t.Fatal("want an error when a save lands in the gap")
		}
		if g := grantOf(h, "vis"); g != nil {
			t.Fatal("grant installed after a concurrent save")
		}
		s, _ := h.Deps().File.FindServer("vis")
		if s.AutoAllow {
			t.Fatal("flag written back on after the concurrent save cleared it")
		}
		_, recs := readAudit(t, path)
		if !hasAutoAllowOff(recs, "saved") {
			t.Fatalf("the concurrent save's own autoAllowOff is missing: %v", recs)
		}
	})
}

// TestSetAutoAllowWriteRaceWindow covers final-fixes item 9: a concurrent
// write landing between SetAutoAllow's first section (where rev is read) and
// its own writeAutoAllow call must not be clobbered by a stale forever flag.
// beforeWrite is the test seam that forces the window.
func TestSetAutoAllowWriteRaceWindow(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	beforeWrite = func() {
		// A concurrent write (any change) bumps Revision without h reloading.
		if err := config.Update(path, testMK, func(f *config.File) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { beforeWrite = nil }()

	err := h.SetAutoAllow("vis", "forever")
	if !errors.Is(err, errAutoAllowRace) {
		t.Fatalf("got %v, want errAutoAllowRace", err)
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant installed despite a raced write")
	}
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	s, _ := h.Deps().File.FindServer("vis")
	if s.AutoAllow {
		t.Fatal("flag written despite a concurrent write racing it")
	}
}

// TestAutoAllowOffDoesNotAuditAFailedWrite covers the fix-round-1 finding: a
// paused forever flag that fails to clear (vault locked) must not audit or
// notify autoAllowOff, and the flag must be left untouched.
func TestAutoAllowOffDoesNotAuditAFailedWrite(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	h.Lock() // ends the grant (audited "locked"); the flag stays true
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant survived Lock")
	}
	_, before := readAudit(t, path)

	if err := h.SetAutoAllow("vis", "off"); !errors.Is(err, ErrLocked) {
		t.Fatalf("got %v, want ErrLocked", err)
	}

	_, after := readAudit(t, path)
	if len(after) != len(before) {
		t.Fatalf("off wrote an audit line despite a failed write: %v", after[len(before):])
	}
	s, _ := h.Deps().File.FindServer("vis")
	if !s.AutoAllow {
		t.Fatal("flag cleared despite a failed write")
	}
}

// TestSetAutoAllowForeverIdempotent covers the fix-round-1 finding: forever
// on an already-armed forever grant (not paused) is a no-op, not a Resume.
func TestSetAutoAllowForeverIdempotent(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	before := grantOf(h, "vis")
	_, recs := readAudit(t, path)
	nBefore := len(recs)

	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	_, recs = readAudit(t, path)
	if len(recs) != nBefore {
		t.Fatalf("already-armed forever wrote an audit line: %v", recs[nBefore:])
	}
	after := grantOf(h, "vis")
	if after == nil || !after.until.IsZero() {
		t.Fatalf("grant changed: %+v", after)
	}
	if before != after {
		t.Fatal("already-armed forever replaced the grant")
	}
}

func TestAutoExecRunsWithoutApproval(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHub(t, fe)
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	type call struct {
		method string
		params map[string]any
	}
	calls := make(chan call, 1)
	release := h.setAutoSink(func(method string, params any) {
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "audit.jsonl"))
		if err != nil {
			t.Error(err)
		}
		if !strings.Contains(string(raw), `"outcome":"allowed"`) {
			t.Errorf("sink fired before the audit line was written: %s", raw)
		}
		calls <- call{method, params.(map[string]any)}
	})
	defer release()

	res, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "echo hi", Description: "d", Client: "t"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if len(h.Broker().Pending()) != 0 {
		t.Fatal("request reached the broker")
	}
	if len(fe.calls) != 1 || fe.calls[0] != "echo hi" {
		t.Fatalf("calls = %v", fe.calls)
	}
	if res.ExitCode != 0 {
		t.Fatalf("res = %+v", res)
	}

	_, recs := readAudit(t, path)
	last := recs[len(recs)-1]
	if last["outcome"] != "allowed" || last["approval"] != "auto" {
		t.Fatalf("audit: %v", last)
	}
	if _, ok := last["waitMs"]; ok {
		t.Fatalf("waitMs present on an auto run: %v", last)
	}

	select {
	case c := <-calls:
		if c.method != "autoAllow.ran" {
			t.Fatalf("method = %q", c.method)
		}
		if c.params["server"] != "vis" || c.params["command"] != "echo hi" || c.params["exitCode"] != 0 {
			t.Fatalf("params = %+v", c.params)
		}
	case <-time.After(time.Second):
		t.Fatal("autoAllow.ran not sent")
	}
}

func TestAutoExecSudoStillAsks(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	if err := decideExec(t, h, ExecRequest{Server: "vis", Command: "id", Sudo: true}); err == nil {
		t.Fatal("want a denial")
	}
}

func TestAutoExecExpired(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.grants["vis"].until = time.Now().Add(-time.Second)
	h.mu.Unlock()

	if err := decideExec(t, h, ExecRequest{Server: "vis", Command: "ls"}); err == nil {
		t.Fatal("want a denial")
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("expired grant still present")
	}
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "expired") {
		t.Fatalf("no autoAllowOff/expired: %v", recs)
	}
}

func TestAutoExecSnapshotMismatch(t *testing.T) {
	t.Run("port changed", func(t *testing.T) {
		h, path := newHub(t, &fakeExec{})
		if err := h.SetAutoAllow("vis", "15m"); err != nil {
			t.Fatal(err)
		}
		if err := config.Update(path, testMK, func(f *config.File) error {
			for i := range f.Servers {
				if f.Servers[i].Name == "vis" {
					f.Servers[i].Port = 23
					f.Servers[i].HostKey = "SHA256:abc" // pin stays
					return nil
				}
			}
			return serverNotFound("vis")
		}); err != nil {
			t.Fatal(err)
		}
		if err := h.Reload(); err != nil {
			t.Fatal(err)
		}
		if err := decideExec(t, h, ExecRequest{Server: "vis", Command: "ls"}); err == nil {
			t.Fatal("want a denial")
		}
		if g := grantOf(h, "vis"); g != nil {
			t.Fatal("grant survived a port change")
		}
		_, recs := readAudit(t, path)
		if !hasAutoAllowOff(recs, "server changed") {
			t.Fatalf("no autoAllowOff/server changed: %v", recs)
		}
	})

	t.Run("auth changed", func(t *testing.T) {
		h, path := newHub(t, &fakeExec{})
		if err := h.SetAutoAllow("vis", "15m"); err != nil {
			t.Fatal(err)
		}
		if err := config.Update(path, testMK, func(f *config.File) error {
			var kept []config.Server
			for _, s := range f.Servers {
				if s.Name != "vis" {
					kept = append(kept, s)
				}
			}
			kept = append(kept, config.Server{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "password", HostKey: "SHA256:abc", AIVisible: true})
			f.Servers = kept
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := h.Reload(); err != nil {
			t.Fatal(err)
		}
		if err := decideExec(t, h, ExecRequest{Server: "vis", Command: "ls"}); err == nil {
			t.Fatal("want a denial")
		}
		if g := grantOf(h, "vis"); g != nil {
			t.Fatal("grant survived an auth change")
		}
		_, recs := readAudit(t, path)
		if !hasAutoAllowOff(recs, "server changed") {
			t.Fatalf("no autoAllowOff/server changed: %v", recs)
		}
	})

	t.Run("su password added", func(t *testing.T) {
		h, path := newHub(t, &fakeExec{})
		if err := h.SetAutoAllow("vis", "15m"); err != nil {
			t.Fatal(err)
		}
		// A real, decryptable ciphertext: a garbage one would fail Exec's own
		// resolve before the auto path is ever reached, at the top of Exec.
		if err := config.Update(path, testMK, func(f *config.File) error {
			for i := range f.Servers {
				if f.Servers[i].Name == "vis" {
					enc, err := config.Encrypt(testMK, "vis/encSuPassword", config.AADFor(f, f.Servers[i], "encSuPassword"), "su-pw")
					if err != nil {
						return err
					}
					f.Servers[i].EncSuPassword = enc
					return nil
				}
			}
			return serverNotFound("vis")
		}); err != nil {
			t.Fatal(err)
		}
		if err := h.Reload(); err != nil {
			t.Fatal(err)
		}
		if err := decideExec(t, h, ExecRequest{Server: "vis", Command: "ls"}); err == nil {
			t.Fatal("want a denial")
		}
		if g := grantOf(h, "vis"); g != nil {
			t.Fatal("grant survived an su password being added")
		}
		_, recs := readAudit(t, path)
		if !hasAutoAllowOff(recs, "server changed") {
			t.Fatalf("no autoAllowOff/server changed: %v", recs)
		}
	})
}

func TestAutoExecForeverFlagClearedOutside(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	if err := config.Update(path, testMK, func(f *config.File) error {
		for i := range f.Servers {
			if f.Servers[i].Name == "vis" {
				f.Servers[i].AutoAllow = false
				return nil
			}
		}
		return serverNotFound("vis")
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}

	if err := decideExec(t, h, ExecRequest{Server: "vis", Command: "ls"}); err == nil {
		t.Fatal("want a denial")
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant survived the flag being cleared outside")
	}
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "server changed") {
		t.Fatalf("no autoAllowOff/server changed: %v", recs)
	}
}

// TestAutoStartFailsClosedWhenServerHidden covers the fix-round-1 finding:
// checkLocked failing inside autoStart itself (here, the server was hidden
// by an outside write) must end the grant right away, not just skip this run
// and leave it armed for an identical server to resume auto runs later with
// no human action. Exec's own top-of-function check already denies this
// call (same as before the fix); autoStart is exercised directly since,
// within one call, Exec never reaches it once that earlier check has failed.
func TestAutoStartFailsClosedWhenServerHidden(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	if err := config.Update(path, testMK, func(f *config.File) error {
		for i := range f.Servers {
			if f.Servers[i].Name == "vis" {
				f.Servers[i].AIVisible = false
				return nil
			}
		}
		return serverNotFound("vis")
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}

	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); err == nil || err.Error() != serverNotFound("vis").Error() {
		t.Fatalf("err = %v, want %v", err, serverNotFound("vis"))
	}

	if ar := h.autoStart(context.Background(), "vis"); ar != nil {
		t.Fatal("autoStart armed a run for a hidden server")
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant survived checkLocked failing inside autoStart")
	}
	_, recs2 := readAudit(t, path)
	if !hasAutoAllowOff(recs2, "server changed") {
		t.Fatalf("no autoAllowOff/server changed: %v", recs2)
	}
}

func TestAutoExecCap(t *testing.T) {
	be := newBlockExec()
	h, _ := newHub(t, be)
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		go h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
	}
	waitInflight(t, h, "vis", 2)

	if err := decideExec(t, h, ExecRequest{Server: "vis", Command: "ls"}); err == nil {
		t.Fatal("want the over-cap request to go to approval and be denied")
	}

	close(be.release)
	waitFor(t, "grant to drain", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		g := h.grants["vis"]
		return g == nil || len(g.inflight) == 0
	})
}

func TestAutoExecCancelledOnLock(t *testing.T) {
	be := newBlockExec()
	h, path := newHub(t, be)
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
		errc <- err
	}()
	waitInflight(t, h, "vis", 1)

	h.Lock()
	err := <-errc
	if !errors.Is(err, ErrCancelledRunning) {
		t.Fatalf("err = %v, want ErrCancelledRunning", err)
	}
	_, recs := readAudit(t, path)
	rec := findAutoRecord(t, recs)
	if rec["outcome"] != "cancelled_running" || rec["approval"] != "auto" {
		t.Fatalf("audit: %v", rec)
	}
}

func TestAutoExecErrorKeepsApproval(t *testing.T) {
	fe := &fakeExec{err: errors.New("boom")}
	h, path := newHub(t, fe)
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	calls := make(chan map[string]any, 1)
	release := h.setAutoSink(func(method string, params any) {
		if method == "autoAllow.ran" {
			calls <- params.(map[string]any)
		}
	})
	defer release()

	_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
	if err == nil {
		t.Fatal("want an error")
	}
	_, recs := readAudit(t, path)
	last := recs[len(recs)-1]
	if last["outcome"] != "error" || last["approval"] != "auto" {
		t.Fatalf("audit: %v", last)
	}
	select {
	case p := <-calls:
		if p["error"] != err.Error() {
			t.Fatalf("notification error = %v, want %q", p["error"], err.Error())
		}
	case <-time.After(time.Second):
		t.Fatal("autoAllow.ran not sent")
	}
	if grantOf(h, "vis") == nil {
		t.Fatal("grant ended after a run-time error")
	}
}

func TestAutoRunsDoNotHoldIdleLock(t *testing.T) {
	be := newBlockExec()
	h, _ := newHub(t, be)
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
		errc <- err
	}()
	waitInflight(t, h, "vis", 1)

	h.mu.Lock()
	h.lastActivity = time.Now().Add(-time.Hour)
	h.mu.Unlock()
	h.lockIfIdle(time.Minute)
	if !h.Locked() {
		t.Fatal("idle lock did not fire during a forever auto run")
	}
	if err := <-errc; !errors.Is(err, ErrCancelledRunning) {
		t.Fatalf("err = %v, want ErrCancelledRunning", err)
	}
}

func TestTimedGrantHoldsIdleLock(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.lastActivity = time.Now().Add(-time.Hour)
	h.mu.Unlock()
	h.lockIfIdle(time.Minute)
	if h.Locked() {
		t.Fatal("idle lock fired despite a timed grant before its deadline")
	}

	h.mu.Lock()
	h.grants["vis"].until = time.Now().Add(-time.Second)
	h.mu.Unlock()
	h.lockIfIdle(time.Minute)
	if !h.Locked() {
		t.Fatal("idle lock did not fire once the grant passed its deadline")
	}
}

func TestSweepGrantsCancelsAtDeadline(t *testing.T) {
	be := newBlockExec()
	h, path := newHub(t, be)
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
		errc <- err
	}()
	waitInflight(t, h, "vis", 1)

	h.mu.Lock()
	h.grants["vis"].until = time.Now().Add(-time.Second)
	h.mu.Unlock()
	h.sweepGrants(time.Now())

	if err := <-errc; !errors.Is(err, ErrCancelledRunning) {
		t.Fatalf("err = %v, want ErrCancelledRunning", err)
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant survived sweepGrants")
	}
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "expired") {
		t.Fatalf("no autoAllowOff/expired: %v", recs)
	}
}

func TestAutoCommandCut(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	calls := make(chan map[string]any, 1)
	release := h.setAutoSink(func(method string, params any) {
		if method == "autoAllow.ran" {
			calls <- params.(map[string]any)
		}
	})
	defer release()

	cmd := "echo " + strings.Repeat("a", 4995) // 5000 bytes total
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: cmd}); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-calls:
		shown, _ := p["command"].(string)
		if len(shown) != 1000 {
			t.Fatalf("command length = %d, want 1000", len(shown))
		}
		if !utf8.ValidString(shown) {
			t.Fatal("cut command is not valid UTF-8")
		}
		if want := len(cmd) - 1000; p["truncated"] != want {
			t.Fatalf("truncated = %v, want %d", p["truncated"], want)
		}
	case <-time.After(time.Second):
		t.Fatal("autoAllow.ran not sent")
	}

	// A multi-byte rune whose second byte would land exactly at the cut
	// point (byte 1000) must not be split: the cut backs up to byte 999,
	// before the rune's first byte.
	cmd2 := "echo " + strings.Repeat("a", 994) + "é" + strings.Repeat("a", 4000)
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: cmd2}); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-calls:
		shown, _ := p["command"].(string)
		if len(shown) != 999 {
			t.Fatalf("command length = %d, want 999 (cut before the straddling rune)", len(shown))
		}
		if !utf8.ValidString(shown) {
			t.Fatalf("cut command is not valid UTF-8: %q", shown)
		}
		if want := len(cmd2) - 999; p["truncated"] != want {
			t.Fatalf("truncated = %v, want %d", p["truncated"], want)
		}
	case <-time.After(time.Second):
		t.Fatal("autoAllow.ran not sent")
	}
}

func TestAutoAllowCheck(t *testing.T) {
	t.Run("uid and passwordless sudo", func(t *testing.T) {
		fe := &fakeExec{res: sshx.ExecResult{Stdout: "1000\nnopasswd\n"}}
		h, path := newHub(t, fe)
		c, err := h.AutoAllowCheck(context.Background(), "vis")
		if err != nil {
			t.Fatal(err)
		}
		if c.UID != 1000 || !c.PasswordlessSudo {
			t.Fatalf("c = %+v", c)
		}
		if len(fe.calls) != 1 || fe.calls[0] != autoProbe {
			t.Fatalf("calls = %v", fe.calls)
		}
		_, recs := readAudit(t, path)
		var rec map[string]any
		for _, r := range recs {
			if r["action"] == "autoAllowCheck" {
				rec = r
			}
		}
		if rec == nil {
			t.Fatalf("no autoAllowCheck audit record: %v", recs)
		}
		if rec["server"] != "vis" || rec["reason"] != "uid 1000, passwordless sudo true" {
			t.Fatalf("audit: %v", rec)
		}
	})

	t.Run("uid without sudo", func(t *testing.T) {
		fe := &fakeExec{res: sshx.ExecResult{Stdout: "0\n"}}
		h, _ := newHub(t, fe)
		c, err := h.AutoAllowCheck(context.Background(), "vis")
		if err != nil {
			t.Fatal(err)
		}
		if c.UID != 0 || c.PasswordlessSudo {
			t.Fatalf("c = %+v", c)
		}
	})

	t.Run("unparseable output", func(t *testing.T) {
		fe := &fakeExec{res: sshx.ExecResult{Stdout: "garbage"}}
		h, path := newHub(t, fe)
		_, err := h.AutoAllowCheck(context.Background(), "vis")
		if err == nil || err.Error() != "could not check this host" {
			t.Fatalf("got %v", err)
		}
		assertAutoAllowCheckFailed(t, path)
	})

	t.Run("exec error", func(t *testing.T) {
		fe := &fakeExec{err: errors.New("boom")}
		h, path := newHub(t, fe)
		_, err := h.AutoAllowCheck(context.Background(), "vis")
		if err == nil || err.Error() != "could not check this host" {
			t.Fatalf("got %v", err)
		}
		assertAutoAllowCheckFailed(t, path)
	})
}

// assertAutoAllowCheckFailed checks the last audit record is an
// autoAllowCheck failure with no error detail in it: the detail stays on
// stderr (final-fixes item 4).
func assertAutoAllowCheckFailed(t *testing.T, path string) {
	t.Helper()
	_, recs := readAudit(t, path)
	last := recs[len(recs)-1]
	if last["action"] != "autoAllowCheck" || last["reason"] != "failed" {
		t.Fatalf("audit: %v", last)
	}
}

func TestAutoAllowCheckRefusals(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHub(t, fe)
	addServer(t, h, path, config.Server{Name: "root", Host: "h", Port: 22, User: "root", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true})
	addServer(t, h, path, config.Server{Name: "suPw", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true, EncSuPassword: "x"})
	addServer(t, h, path, config.Server{Name: "sudoPw", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true, EncSudoPassword: "x"})

	cases := []struct {
		name   string
		locked bool
		server string
		want   string
	}{
		{"locked", true, "vis", ErrLocked.Error()},
		{"nokey", false, "nokey", ErrNoHostKey.Error()},
		{"hidden", false, "hid", serverNotFound("hid").Error()},
		{"ghost", false, "ghost", serverNotFound("ghost").Error()},
		{"root", false, "root", "auto-allow refused: root login"},
		{"suPassword", false, "suPw", "auto-allow refused: has an su password"},
		{"sudoPassword", false, "sudoPw", "auto-allow refused: has a sudo password"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.locked {
				h.Lock()
				defer unlockForTest(h)
			}
			_, err := h.AutoAllowCheck(context.Background(), c.server)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want containing %q", err, c.want)
			}
		})
	}
	if len(fe.calls) != 0 {
		t.Fatalf("calls = %v, want none for a refused check", fe.calls)
	}
}

// TestCLIModeNeverArms covers the constraint that a reload, or the hub
// simply loading a store whose AutoAllow flag is already true, never arms a
// grant: only SetAutoAllow does.
// TestCloseEndsGrants covers final-fixes item 3: Hub.Close must end every
// grant (cancelling in-flight runs) and audit autoAllowOff "hub stopped",
// same as Lock, but the vault flag itself is untouched: a forever host comes
// back paused, not off, after the hub restarts.
func TestCloseEndsGrants(t *testing.T) {
	be := newBlockExec()
	h, path := newHub(t, be)
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
		errc <- err
	}()
	waitInflight(t, h, "vis", 1)

	h.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrCancelledRunning) {
			t.Fatalf("err = %v, want ErrCancelledRunning", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Exec did not return after Close")
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant survived Close")
	}
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "hub stopped") {
		t.Fatalf("no autoAllowOff/hub stopped: %v", recs)
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := f.FindServer("vis")
	if !s.AutoAllow {
		t.Fatal("forever flag cleared by hub stop; it should come back paused")
	}
}

func TestCLIModeNeverArms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	f := &config.File{Version: 1, Servers: []config.Server{
		{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true, AutoAllow: true},
	}}
	var mk []byte
	f.KDF, mk = testVault(t)
	if err := config.Save(path, f, mk); err != nil {
		t.Fatal(err)
	}
	audit, err := broker.OpenAudit(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { audit.Close() })
	fe := &fakeExec{}
	h, err := New(Options{StorePath: path, Audit: audit, ApprovalExpiry: time.Minute,
		Dialer: func(sshx.DialConfig) Executor { return fe }})
	if err != nil {
		t.Fatal(err)
	}
	unlockForTest(h)

	if err := decideExec(t, h, ExecRequest{Server: "vis", Command: "ls"}); err == nil {
		t.Fatal("want a denial; the hub must never arm from the stored flag")
	}
}
