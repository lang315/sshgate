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
// any non-empty string does for those fields when the refusal itself, not a
// resolve past it, is what a test needs (encServer below builds a real,
// decryptable ciphertext for a test that must reach resolveLocked).
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

// encServer returns a config.Server (agent auth, pinned, AI-visible) with
// field set to a real, decryptable ciphertext of pt: a garbage blob would
// fail Exec's own resolve before the auto path is ever reached. aadFor only
// depends on version/name/host/port/user/auth, so computing it against a
// scratch one-server File gives the same AAD the vault computes once this
// server is actually stored.
func encServer(t *testing.T, name, field, pt string) config.Server {
	t.Helper()
	s := config.Server{Name: name, Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true}
	enc, err := config.Encrypt(testMK, name+"/"+field, config.AADFor(&config.File{Version: 1}, s, field), pt)
	if err != nil {
		t.Fatal(err)
	}
	switch field {
	case "encSuPassword":
		s.EncSuPassword = enc
	case "encSudoPassword":
		s.EncSudoPassword = enc
	}
	return s
}

// setAutoAllowRoot flips name's AutoAllowRoot flag outside servers.save (a
// raw store rewrite) and reloads h, the same technique addServer's sibling
// tests use to simulate an out-of-band or already-saved state.
func setAutoAllowRoot(t *testing.T, h *Hub, path, name string, on bool) {
	t.Helper()
	if err := config.Update(path, testMK, func(f *config.File) error {
		for i := range f.Servers {
			if f.Servers[i].Name == name {
				f.Servers[i].AutoAllowRoot = on
				return nil
			}
		}
		return serverNotFound(name)
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
}

// Amendment 2026-09-29: AutoAllowRoot lets a root login, a stored su
// password, or a stored sudo password onto auto-allow. The refusals that
// always apply (not visible to AI, no pinned host key) still hold with the
// option on.
func TestRootOptInAllowsRootHosts(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	root := config.Server{Name: "root", Host: "h", Port: 22, User: "root", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true}
	addServer(t, h, path, root)
	addServer(t, h, path, encServer(t, "suPw", "encSuPassword", "su-pw"))
	addServer(t, h, path, encServer(t, "sudoPw", "encSudoPassword", "sudo-pw"))

	for _, name := range []string{"root", "suPw", "sudoPw"} {
		if err := h.SetAutoAllow(name, "15m"); err == nil {
			t.Fatalf("%s: want a refusal with the option off", name)
		}
		if g := grantOf(h, name); g != nil {
			t.Fatalf("%s: grant created despite the option being off", name)
		}
	}

	for _, name := range []string{"root", "suPw", "sudoPw"} {
		setAutoAllowRoot(t, h, path, name, true)
		if err := h.SetAutoAllow(name, "15m"); err != nil {
			t.Fatalf("%s: got %v, want the option to allow it", name, err)
		}
		if g := grantOf(h, name); g == nil {
			t.Fatalf("%s: no grant created", name)
		}
		if err := h.SetAutoAllow(name, "off"); err != nil {
			t.Fatal(err)
		}
	}

	// The refusals that always apply still hold with the option on.
	addServer(t, h, path, config.Server{Name: "nopin", Host: "h", Port: 22, User: "root", Auth: "agent", AIVisible: true, AutoAllowRoot: true})
	if err := h.SetAutoAllow("nopin", "15m"); err == nil || !strings.Contains(err.Error(), ErrNoHostKey.Error()) {
		t.Fatalf("got %v, want %v", err, ErrNoHostKey)
	}
	// Not AI-visible: checkLocked reports it byte-identical to a nonexistent
	// server (spec rule) before autoRefusal is ever reached.
	addServer(t, h, path, config.Server{Name: "hiddenroot", Host: "h", Port: 22, User: "root", Auth: "agent", HostKey: "SHA256:abc", AutoAllowRoot: true})
	if err := h.SetAutoAllow("hiddenroot", "15m"); err == nil || err.Error() != serverNotFound("hiddenroot").Error() {
		t.Fatalf("got %v, want %v", err, serverNotFound("hiddenroot"))
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

// TestAutoAllowOffAuditsDespiteReloadFailureAfterWrite covers round 5 item
// 1: autoAllowOff's flag write and the reload right after it are now two
// separately tracked errors. A grant present at the point autoAllowOff runs
// (or, as here, a flag that was still set) proves something really ended
// once the Update itself has landed, regardless of whether the confirming
// reload that follows succeeds — so the audit line must still be written,
// and the flag must still be false on disk, even though this call returns
// the reload's error. loadStore is the existing seam (hosts_test.go,
// servers_test.go et al.); call 1 is autoAllowOff's own new leading reload
// (item 2), call 2 is the one right after the write.
func TestAutoAllowOffAuditsDespiteReloadFailureAfterWrite(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.endGrantLocked("vis") // paused forever: flag stays true, no live grant
	h.mu.Unlock()

	var calls int
	loadStore = func(p string) (*config.File, error) {
		calls++
		if calls == 1 {
			return config.Load(p)
		}
		return nil, errors.New("boom")
	}
	t.Cleanup(func() { loadStore = config.Load })

	err := h.SetAutoAllow("vis", "off")
	if err == nil || err.Error() != "boom" {
		t.Fatalf("got %v, want the reload error", err)
	}

	f, lerr := config.Load(path)
	if lerr != nil {
		t.Fatal(lerr)
	}
	s, _ := f.FindServer("vis")
	if s.AutoAllow {
		t.Fatal("flag still true on disk despite the Update succeeding")
	}
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "turned off") {
		t.Fatalf("no autoAllowOff/turned off despite a successful write: %v", recs)
	}
}

// TestAutoAllowOffReloadsBeforeReadingFlag covers round 5 item 2:
// autoAllowOff used to read the flag straight from whatever h.deps.File
// already held, with no reload of its own. A flag flipped true on disk by a
// write this hub's memory hasn't observed yet (an out-of-band config.Update,
// standing in for a concurrent write this hub simply hasn't reloaded since)
// would then be read as false, skipping the write entirely: "off" would
// report success but leave the on-disk flag untouched. autoAllowOff now
// reloads at the start of its section, the same as SetAutoAllow, so it
// reads the fresh flag instead.
func TestAutoAllowOffReloadsBeforeReadingFlag(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	// Out of band: nothing about this hub's own in-memory copy changes.
	if err := config.Update(path, testMK, func(f *config.File) error {
		for i := range f.Servers {
			if f.Servers[i].Name == "vis" {
				f.Servers[i].AutoAllow = true
				return nil
			}
		}
		return serverNotFound("vis")
	}); err != nil {
		t.Fatal(err)
	}

	if err := h.SetAutoAllow("vis", "off"); err != nil {
		t.Fatal(err)
	}

	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := f.FindServer("vis")
	if s.AutoAllow {
		t.Fatal("flag still true on disk: autoAllowOff read a stale in-memory flag instead of reloading first")
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
	// A rename must not inherit a grant already sitting under the target
	// name either (a stray left by an earlier bug, or of a since-deleted
	// server that reused the name): SaveServer ends both names.
	t.Run("rename ends a leftover grant under the target name too", func(t *testing.T) {
		h, path := newHub(t, &fakeExec{})
		h.mu.Lock()
		h.grants["vis2"] = &grant{inflight: map[uint64]context.CancelFunc{}}
		h.mu.Unlock()
		in := config.ServerInput{Name: "vis2", Host: "h", Port: 22, User: "u", Auth: "agent"}
		if err := h.SaveServer("vis", in); err != nil {
			t.Fatal(err)
		}
		if g := grantOf(h, "vis2"); g != nil {
			t.Fatal("rename inherited a leftover grant under the target name")
		}
		// Round 4 item 5: the stray grant that just ended was under "vis2",
		// never "vis" (nothing about "vis" itself ended here — it had no
		// grant and no flag), so the record must say "vis2".
		_, recs := readAudit(t, path)
		var found bool
		for _, r := range recs {
			if r["action"] == "autoAllowOff" && r["reason"] == "saved" && r["server"] == "vis2" {
				found = true
			}
			if r["action"] == "autoAllowOff" && r["server"] == "vis" {
				t.Fatalf("autoAllowOff recorded under vis, which had nothing to end: %v", r)
			}
		}
		if !found {
			t.Fatalf("no autoAllowOff/saved for vis2: %v", recs)
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

// TestSetAutoAllowFromInsideSaveDenyPending is a deterministic reproduction
// of denyPending's reentrancy: broker.Decide fires the broker's "decided"
// event synchronously, on the caller's own goroutine, with no lock held.
// Reentering SetAutoAllow from that event runs it after SaveServer's own
// h.mu section (write, reload, grant-end) has already finished — denyPending
// is called only once that section has unlocked (hosts.go) — so this
// reentrant call races nothing: no other write can be in flight, and it must
// arm cleanly, not hit errAutoAllowRace at all.
func TestSetAutoAllowFromInsideSaveDenyPending(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	// A pending exec request for "vis" gives denyPending something to decide.
	errc := make(chan error, 1)
	go func() {
		_, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"})
		errc <- err
	}()
	waitPending(t, h.Broker(), 1)

	release := h.setEventSink(func(e broker.Event) {
		if e.Kind == "decided" && e.Request.Server == "vis" {
			if err := h.SetAutoAllow("vis", "forever"); err != nil {
				t.Errorf("reentrant SetAutoAllow: %v", err)
			}
		}
	})
	defer release()

	in := config.ServerInput{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "agent", AIVisible: true}
	if err := h.SaveServer("vis", in); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err == nil {
		t.Fatal("want a denial for the pending request the save just denied")
	}

	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := f.FindServer("vis")
	if hasGrant := grantOf(h, "vis") != nil; hasGrant != s.AutoAllow {
		t.Fatalf("grant present = %v, flag on disk = %v", hasGrant, s.AutoAllow)
	}
}

// TestSaveEndsPreexistingTimedGrantDespiteUnrelatedRevisionBump guards
// against reintroducing revision arithmetic: an earlier design (round 2)
// compared the reloaded revision against this save's own write, which wrongly
// protected a grant that predates the save whenever some UNRELATED write (a
// tunnels.save on another host, say — reproduced here via a raw config.Update
// run as a side effect of the loadStore seam, right where the cleanup's
// reload lands) bumped the store's revision for a reason that has nothing to
// do with this server's grant. The current design ends the grant
// unconditionally instead, so an unrelated bump can't protect it: a
// label-only save (no dial-affecting field changes, so autoStart's own
// snapshot check would not have caught this on its own) must still end a
// preexisting timed grant — "every save turns it off".
func TestSaveEndsPreexistingTimedGrantDespiteUnrelatedRevisionBump(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	if grantOf(h, "vis") == nil {
		t.Fatal("no grant armed")
	}

	loadStore = func(p string) (*config.File, error) {
		if err := config.Update(p, testMK, func(f *config.File) error { return nil }); err != nil {
			return nil, err
		}
		return config.Load(p)
	}
	t.Cleanup(func() { loadStore = config.Load })

	in := config.ServerInput{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "agent", AIVisible: true}
	if err := h.SaveServer("vis", in); err != nil {
		t.Fatal(err)
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("preexisting timed grant survived a label-only save because of an unrelated revision bump")
	}
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "saved") {
		t.Fatalf("no autoAllowOff/saved: %v", recs)
	}
}

// TestSaveEndsGrantDespiteReloadFailure covers round 3's "fail closed" rule:
// SaveServer ends the server's grant unconditionally even when the reload
// that follows its own write fails, since a grant present at that point
// provably predates the write regardless of what the reload can confirm.
// loadStore is the existing seam (hosts_test.go, servers_test.go et al.).
func TestSaveEndsGrantDespiteReloadFailure(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	loadStore = func(string) (*config.File, error) { return nil, errors.New("boom") }
	t.Cleanup(func() { loadStore = config.Load })

	in := config.ServerInput{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "agent", AIVisible: true}
	err := h.SaveServer("vis", in)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("got %v, want the reload error", err)
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant survived a save despite its own reload failing (fail closed)")
	}
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "saved") {
		t.Fatalf("no autoAllowOff/saved: %v", recs)
	}
}

// TestReviewSaveEndsGrantAfterVaultRollback is round 3's reviewer test for
// the first hole in the old armedRev design: a MAC-valid older copy of the
// vault (restored from a backup, or by a file sync) makes the store's
// revision go backwards, so a save's own write produces a "wrote" revision
// lower than the grant's armedRev — the grant then survived the save with no
// audit record. The fixed design (unconditional end, no revision arithmetic)
// has nothing to compare, so a rollback can't create this hole.
func TestReviewSaveEndsGrantAfterVaultRollback(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ { // unrelated writes (e.g. tunnels.save on another host)
		if err := config.Update(path, testMK, func(f *config.File) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, old, 0o600); err != nil { // restore the older valid copy
		t.Fatal(err)
	}
	in := config.ServerInput{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "agent", AIVisible: true}
	if err := h.SaveServer("vis", in); err != nil {
		t.Fatal(err)
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant survived a save after a vault rollback")
	}
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "saved") {
		t.Fatalf("no autoAllowOff/saved: %v", recs)
	}
}

// TestReviewSaveAuditOrderSurvivesRearmDuringDenyPending is round 3's
// reviewer test for the second hole in the old armedRev design: with a
// paused forever grant (flag on, no live grant), a save's denyPending step
// re-enters SetAutoAllow(15m) from the broker's synchronous "decided" event.
// The old code wrote the save's own "autoAllowOff saved" audit line AFTER
// denyPending (and so after the reentrant re-arm's "autoAllowOn" line),
// misreporting a live grant as off. The fix moves the grant-end and its
// audit line into the h.mu section, which finishes before denyPending runs,
// so "saved" can only ever precede a re-arm that happens because of it, not
// follow one. This checks the order directly (index, not mere presence): a
// live grant at the end must not have "saved" as the last word about it.
func TestReviewSaveAuditOrderSurvivesRearmDuringDenyPending(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.endGrantLocked("vis") // paused forever: flag stays true, no live grant
	h.mu.Unlock()

	errc := make(chan error, 1)
	go func() { _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "ls"}); errc <- err }()
	waitPending(t, h.Broker(), 1)

	rel := h.setEventSink(func(e broker.Event) {
		if e.Kind == "decided" && e.Request.Server == "vis" {
			if err := h.SetAutoAllow("vis", "15m"); err != nil {
				t.Errorf("reentrant SetAutoAllow: %v", err)
			}
		}
	})
	defer rel()

	in := config.ServerInput{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "agent", AIVisible: true}
	if err := h.SaveServer("vis", in); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err == nil {
		t.Fatal("want a denial for the pending request the save's denyPending decided")
	}

	_, recs := readAudit(t, path)
	lastOff, lastOn := -1, -1
	for i, r := range recs {
		if r["server"] != "vis" {
			continue
		}
		switch r["action"] {
		case "autoAllowOff":
			lastOff = i
		case "autoAllowOn", "autoAllowResume":
			lastOn = i
		}
	}
	if grantOf(h, "vis") != nil && lastOff > lastOn {
		t.Fatalf("grant armed but the last audit word for vis is autoAllowOff: off@%d, on@%d, recs=%v", lastOff, lastOn, recs)
	}
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

func TestServersForUIShowsAutoAllowOptIns(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	addServer(t, h, path, config.Server{Name: "opted", Host: "h", Port: 22, User: "u", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true, AutoAllowRoot: true, AutoAllowSudo: true})
	for _, s := range h.serversForUI() {
		if s.Name == "opted" {
			if !s.AutoAllowRoot || !s.AutoAllowSudo {
				t.Fatalf("opted = %+v", s)
			}
			return
		}
	}
	t.Fatal("opted not found")
}

// TestSetAutoAllowConcurrentWithSave covers round 3's design: SaveServer now
// holds h.mu across its own write, reload and grant-end (hosts.go), the same
// section SetAutoAllow uses, so the two fully serialize — whichever call
// acquires h.mu first runs to completion before the other starts. Neither
// call can land inside the other's section any more, so this must always
// land on one of the two clean equilibria (no grant and the flag false, or a
// grant and the flag true) with no race error from either call, and the
// audit's last word about "vis" must never be an off while a grant is live.
func TestSetAutoAllowConcurrentWithSave(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	in := config.ServerInput{Name: "vis", Host: "h", Port: 22, User: "u", Auth: "agent", AIVisible: true}

	const iterations = 200
	for i := 0; i < iterations; i++ {
		var wg sync.WaitGroup
		wg.Add(2)
		var saveErr, setErr error
		go func() { defer wg.Done(); saveErr = h.SaveServer("vis", in) }()
		go func() { defer wg.Done(); setErr = h.SetAutoAllow("vis", "forever") }()
		wg.Wait()

		if saveErr != nil {
			t.Fatalf("iteration %d: SaveServer: %v", i, saveErr)
		}
		if setErr != nil {
			t.Fatalf("iteration %d: SetAutoAllow: %v", i, setErr)
		}

		f, err := config.Load(path)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		s, ok := f.FindServer("vis")
		if !ok {
			t.Fatalf("iteration %d: vis not found", i)
		}
		hasGrant := grantOf(h, "vis") != nil
		if hasGrant != s.AutoAllow {
			t.Fatalf("iteration %d: grant present = %v, flag on disk = %v", i, hasGrant, s.AutoAllow)
		}
		_, recs := readAudit(t, path)
		var lastAction string
		for _, r := range recs {
			if r["server"] != "vis" {
				continue
			}
			if a, _ := r["action"].(string); a == "autoAllowOff" || a == "autoAllowOn" || a == "autoAllowResume" {
				lastAction = a
			}
		}
		if hasGrant && lastAction == "autoAllowOff" {
			t.Fatalf("iteration %d: grant present but the last audit word for vis is autoAllowOff", i)
		}
		if hasGrant {
			// Only a clean SetAutoAllow may leave a grant armed; reset for
			// the next iteration.
			if err := h.SetAutoAllow("vis", "off"); err != nil {
				t.Fatalf("iteration %d: cleanup off: %v", i, err)
			}
		}
	}

	_, recs := readAudit(t, path)
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := f.FindServer("vis"); ok && s.AutoAllow {
		var found bool
		for _, r := range recs {
			if r["action"] == "autoAllowOn" && r["forever"] == true {
				found = true
			}
		}
		if !found {
			t.Fatal("flag ended up set with no autoAllowOn forever record in the audit")
		}
	}
}

// TestAutoAllowOffRaceWithConcurrentArm covers round 4 item 2:
// autoAllowOff now holds h.mu across ending the grant, writing the flag,
// reloading and auditing (autoallow.go), the same one-section pattern
// hosts.go's SaveServer et al. use, so a concurrent SetAutoAllow("15m")
// can't land in the gap between the grant ending and the flag write and
// leave a live timed grant behind a stale "off" audit record: whichever call
// acquires h.mu first now runs to completion before the other starts.
func TestAutoAllowOffRaceWithConcurrentArm(t *testing.T) {
	h, path := newHub(t, &fakeExec{})

	const iterations = 200
	for i := 0; i < iterations; i++ {
		if err := h.SetAutoAllow("vis", "forever"); err != nil {
			t.Fatalf("iteration %d: arm: %v", i, err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		var offErr, setErr error
		go func() { defer wg.Done(); offErr = h.SetAutoAllow("vis", "off") }()
		go func() { defer wg.Done(); setErr = h.SetAutoAllow("vis", "15m") }()
		wg.Wait()

		if offErr != nil {
			t.Fatalf("iteration %d: off: %v", i, offErr)
		}
		if setErr != nil {
			t.Fatalf("iteration %d: 15m: %v", i, setErr)
		}

		f, err := config.Load(path)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		s, ok := f.FindServer("vis")
		if !ok {
			t.Fatalf("iteration %d: vis not found", i)
		}
		if s.AutoAllow {
			t.Fatalf("iteration %d: a timed grant never sets the persistent flag, but it ended up true", i)
		}

		_, recs := readAudit(t, path)
		var lastAction string
		for _, r := range recs {
			if r["server"] != "vis" {
				continue
			}
			if a, _ := r["action"].(string); a == "autoAllowOff" || a == "autoAllowOn" || a == "autoAllowResume" {
				lastAction = a
			}
		}
		hasGrant := grantOf(h, "vis") != nil
		if hasGrant && lastAction == "autoAllowOff" {
			t.Fatalf("iteration %d: grant present but the last audit word for vis is autoAllowOff", i)
		}
		// Whichever call won, clean up so the next iteration starts paused.
		if err := h.SetAutoAllow("vis", "off"); err != nil {
			t.Fatalf("iteration %d: cleanup off: %v", i, err)
		}
	}
}

// TestSetAutoAllowReloadFailureAuditsAndArmsNothing covers item 1: if the
// flag write succeeds but the reload right after it fails, the write still
// happened and must be audited, but nothing may be armed on a state we could
// not confirm. loadStore is the existing seam hub.go documents for forcing a
// reload failure (see hosts_test.go, servers_test.go, trust_test.go).
// SetAutoAllow now reloads once at the very start of its section too (round
// 2, item 4), so the seam only fails the second call (the one right after
// the write); the first must still succeed, or the eligibility check itself
// would fail first and the write would never be attempted.
func TestSetAutoAllowReloadFailureAuditsAndArmsNothing(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	var calls int
	loadStore = func(p string) (*config.File, error) {
		calls++
		if calls == 1 {
			return config.Load(p)
		}
		return nil, errors.New("boom")
	}
	t.Cleanup(func() { loadStore = config.Load })

	err := h.SetAutoAllow("vis", "forever")
	if err == nil || err.Error() != "boom" {
		t.Fatalf("got %v, want the reload error", err)
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant armed despite a failed reload")
	}
	_, recs := readAudit(t, path)
	last := recs[len(recs)-1]
	if last["action"] != "autoAllowOn" || last["forever"] != true {
		t.Fatalf("audit: %v", last)
	}

	loadStore = config.Load
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := f.FindServer("vis"); !s.AutoAllow {
		t.Fatal("flag write did not stand despite the reload failing")
	}
}

// TestSetAutoAllowReloadFailureEndsOldForeverGrant covers item 3 (round 2):
// switching an armed forever grant to a timed mode clears the forever flag
// first; if the reload right after that fails, the old forever grant (still
// armed, now backed by nothing since the flag is cleared) must be ended too,
// not left standing — and exactly one autoAllowOff record/notification must
// result, not two (the flag-change audit already covers it).
func TestSetAutoAllowReloadFailureEndsOldForeverGrant(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	calls := make(chan map[string]string, 1)
	release := h.setAutoSink(func(method string, params any) {
		if method == "autoAllow.off" {
			calls <- params.(map[string]string)
		}
	})
	defer release()

	var n int
	loadStore = func(p string) (*config.File, error) {
		n++
		if n == 1 {
			return config.Load(p) // SetAutoAllow's own initial reload (item 4)
		}
		return nil, errors.New("boom") // the reload right after clearing the flag
	}
	t.Cleanup(func() { loadStore = config.Load })

	err := h.SetAutoAllow("vis", "15m")
	if err == nil || err.Error() != "boom" {
		t.Fatalf("got %v, want the reload error", err)
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("old forever grant survived a reload failure that cleared its flag")
	}
	_, recs := readAudit(t, path)
	var offCount int
	for _, r := range recs {
		if r["action"] == "autoAllowOff" {
			offCount++
		}
	}
	if offCount != 1 {
		t.Fatalf("want exactly one autoAllowOff record, got %d: %v", offCount, recs)
	}
	select {
	case p := <-calls:
		if p["server"] != "vis" || p["reason"] != "turned off" {
			t.Fatalf("params = %+v", p)
		}
	case <-time.After(time.Second):
		t.Fatal("autoAllow.off notification not sent")
	}
}

// TestSetAutoAllowPostWriteChangeRefused covers the post-reload recheck
// (`!timed && !s.AutoAllow`), which has no deterministic test of its own —
// the concurrency test only proves no violation is ever observed, not that
// this specific branch is what prevents one. Round 3 holds h.mu across
// SaveServer/DeleteServer/ForgetHostKey's own write, reload and grant-end, so
// none of them can land here any more; what the recheck still catches is a
// change from outside the hub (another process editing the store file).
// loadStore's seam stands in for that: it clears the flag this call just set
// as a side effect of the reload right after that write.
func TestSetAutoAllowPostWriteChangeRefused(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	var n int
	loadStore = func(p string) (*config.File, error) {
		n++
		if n == 2 { // right after SetAutoAllow's own write set the flag true
			if err := config.Update(p, testMK, func(f *config.File) error {
				for i := range f.Servers {
					if f.Servers[i].Name == "vis" {
						f.Servers[i].AutoAllow = false
						return nil
					}
				}
				return serverNotFound("vis")
			}); err != nil {
				return nil, err
			}
		}
		return config.Load(p)
	}
	t.Cleanup(func() { loadStore = config.Load })

	if err := h.SetAutoAllow("vis", "forever"); !errors.Is(err, errAutoAllowRace) {
		t.Fatalf("got %v, want errAutoAllowRace", err)
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant armed despite the flag being cleared out from under it")
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := f.FindServer("vis"); s.AutoAllow {
		t.Fatal("flag ended up true despite the concurrent clear")
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

// Amendment 2026-09-29: sudo-exec on a granted host needs its own opt-in.
// Without it, a sudo request goes to approval and the grant survives the
// denial (autoStart only ends a grant it finds to be stale). With
// AutoAllowSudo turned on by a raw store rewrite (not a servers.save, so the
// grant stays armed across it), the same request runs with no approval.
func TestSudoExecNeedsOptIn(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHub(t, fe)
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}

	if err := decideExec(t, h, ExecRequest{Server: "vis", Command: "id", Sudo: true}); err == nil {
		t.Fatal("want a denial")
	}
	if g := grantOf(h, "vis"); g == nil {
		t.Fatal("grant ended by a denied sudo request")
	}

	if err := config.Update(path, testMK, func(f *config.File) error {
		for i := range f.Servers {
			if f.Servers[i].Name == "vis" {
				f.Servers[i].AutoAllowSudo = true
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
	if g := grantOf(h, "vis"); g == nil {
		t.Fatal("grant lost across the opt-in rewrite")
	}

	type call struct {
		method string
		params map[string]any
	}
	calls := make(chan call, 1)
	release := h.setAutoSink(func(method string, params any) { calls <- call{method, params.(map[string]any)} })
	defer release()

	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "id", Sudo: true, Description: "d"}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if len(h.Broker().Pending()) != 0 {
		t.Fatal("sudo request reached the broker despite the opt-in")
	}
	if len(fe.calls) != 1 || fe.calls[0] != "sudo:id" {
		t.Fatalf("calls = %v", fe.calls)
	}

	_, recs := readAudit(t, path)
	last := findAutoRecord(t, recs)
	if last["approval"] != "auto" || last["sudo"] != true {
		t.Fatalf("audit: %v", last)
	}

	select {
	case c := <-calls:
		if c.method != "autoAllow.ran" || c.params["sudo"] != true {
			t.Fatalf("params = %+v", c.params)
		}
	case <-time.After(time.Second):
		t.Fatal("autoAllow.ran not sent")
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

// A root server with the option on and a live grant: turning the option off
// outside servers.save (a raw rewrite + reload) is caught at the next exec
// like any other snap mismatch, since autoStart re-checks autoRefusal on the
// current server every run.
func TestAutoStartEndsGrantWhenRootOptInRemoved(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	addServer(t, h, path, config.Server{Name: "root", Host: "h", Port: 22, User: "root", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true, AutoAllowRoot: true})
	if err := h.SetAutoAllow("root", "15m"); err != nil {
		t.Fatal(err)
	}

	setAutoAllowRoot(t, h, path, "root", false)

	if err := decideExec(t, h, ExecRequest{Server: "root", Command: "ls"}); err == nil {
		t.Fatal("want a denial (fell through to approval)")
	}
	if g := grantOf(h, "root"); g != nil {
		t.Fatal("grant survived the opt-in being removed")
	}
	_, recs := readAudit(t, path)
	if !hasAutoAllowOff(recs, "server changed") {
		t.Fatalf("no autoAllowOff/server changed: %v", recs)
	}
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

	if ar := h.autoStart(context.Background(), "vis", false); ar != nil {
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

// Task 2 (Amendment 2026-09-29): servers.save takes an optional auto-allow
// mode, armed in the same h.mu section as the write. inputFor (servers_test.go)
// returns "vis" unchanged (same host/port), so its pin survives the save and
// it stays eligible.

func TestSaveArmsTimed(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	in := inputFor(t, h, "vis")
	before := time.Now()
	if err := h.SaveServerWithAutoAllow("vis", in, "15m"); err != nil {
		t.Fatal(err)
	}
	g := grantOf(h, "vis")
	if g == nil || g.until.IsZero() {
		t.Fatalf("grant = %+v", g)
	}
	want := before.Add(15 * time.Minute)
	if diff := g.until.Sub(want); diff < -5*time.Second || diff > 5*time.Second {
		t.Fatalf("until = %v, want ~%v", g.until, want)
	}
	_, recs := readAudit(t, path)
	var sawSave, sawOn bool
	for _, r := range recs {
		if r["action"] == "save" {
			sawSave = true
		}
		if r["action"] == "autoAllowOn" {
			sawOn = true
			if _, ok := r["until"]; !ok {
				t.Fatalf("autoAllowOn missing until: %v", r)
			}
		}
	}
	if !sawSave || !sawOn {
		t.Fatalf("audit missing save/autoAllowOn: %v", recs)
	}
}

func TestSaveArmsForever(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	in := inputFor(t, h, "vis")
	if err := h.SaveServerWithAutoAllow("vis", in, "forever"); err != nil {
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
	var sawOn bool
	for _, r := range recs {
		if r["action"] == "autoAllowOn" && r["forever"] == true {
			sawOn = true
		}
	}
	if !sawOn {
		t.Fatalf("audit missing autoAllowOn forever: %v", recs)
	}
}

// TestSaveEndsOldGrantThenArms: the audit order is autoAllowOff (reason
// "saved") then autoAllowOn (with until), both written inside the locked
// section, then save, written after unlocking.
func TestSaveEndsOldGrantThenArms(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	if err := h.SetAutoAllow("vis", "forever"); err != nil {
		t.Fatal(err)
	}
	in := inputFor(t, h, "vis")
	if err := h.SaveServerWithAutoAllow("vis", in, "30m"); err != nil {
		t.Fatal(err)
	}
	_, recs := readAudit(t, path)
	if len(recs) < 3 {
		t.Fatalf("too few audit records: %v", recs)
	}
	last3 := recs[len(recs)-3:]
	if last3[0]["action"] != "autoAllowOff" || last3[0]["reason"] != "saved" {
		t.Fatalf("record[0] = %v, want autoAllowOff saved", last3[0])
	}
	if last3[1]["action"] != "autoAllowOn" {
		t.Fatalf("record[1] = %v, want autoAllowOn", last3[1])
	}
	if _, ok := last3[1]["until"]; !ok {
		t.Fatalf("autoAllowOn missing until: %v", last3[1])
	}
	if last3[2]["action"] != "save" {
		t.Fatalf("record[2] = %v, want save", last3[2])
	}
	g := grantOf(h, "vis")
	if g == nil || g.until.IsZero() {
		t.Fatalf("grant not timed: %+v", g)
	}
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	s, _ := h.Deps().File.FindServer("vis")
	if s.AutoAllow {
		t.Fatal("forever flag still set")
	}
}

// TestSaveOffEndsGrant: mode "off" or "" behaves exactly like today's
// SaveServer: the grant ends and nothing is armed.
func TestSaveOffEndsGrant(t *testing.T) {
	for _, mode := range []string{"off", ""} {
		t.Run(mode, func(t *testing.T) {
			h, _ := newHub(t, &fakeExec{})
			if err := h.SetAutoAllow("vis", "15m"); err != nil {
				t.Fatal(err)
			}
			in := inputFor(t, h, "vis")
			if err := h.SaveServerWithAutoAllow("vis", in, mode); err != nil {
				t.Fatal(err)
			}
			if g := grantOf(h, "vis"); g != nil {
				t.Fatal("grant survived a save with mode off")
			}
		})
	}
}

// TestSaveArmRefusedKeepsSave: the write stands even when arming is refused,
// and the returned error is prefixed with errSavedButPrefix.
func TestSaveArmRefusedKeepsSave(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	addServer(t, h, path, config.Server{Name: "root", Host: "h", Port: 22, User: "root", Auth: "agent", HostKey: "SHA256:abc", AIVisible: true})

	in := inputFor(t, h, "root")
	in.Name = "root2" // the label change that must land on disk despite the refusal
	err := h.SaveServerWithAutoAllow("root", in, "15m")
	wantErr := errSavedButPrefix + "auto-allow refused: root login"
	if err == nil || err.Error() != wantErr {
		t.Fatalf("got %v, want %q", err, wantErr)
	}
	if g := grantOf(h, "root2"); g != nil {
		t.Fatal("grant armed despite the refusal")
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.FindServer("root"); ok {
		t.Fatal("old name still present")
	}
	if _, ok := f.FindServer("root2"); !ok {
		t.Fatal("rename did not land on disk")
	}

	in2 := inputFor(t, h, "root2")
	in2.AutoAllowRoot = true
	if err := h.SaveServerWithAutoAllow("root2", in2, "15m"); err != nil {
		t.Fatal(err)
	}
	if g := grantOf(h, "root2"); g == nil {
		t.Fatal("grant not armed once AutoAllowRoot is set")
	}
}

// TestUnknownAutoAllowModeRefused: armLocked refuses any mode that is
// neither "forever" nor a key of autoModes, before arming or writing
// anything — a controller sending a garbage mode string must fail closed,
// not silently arm forever.
func TestUnknownAutoAllowModeRefused(t *testing.T) {
	h, path := newHub(t, &fakeExec{})
	wantErr := `unknown auto-allow mode "1h"`

	in := inputFor(t, h, "vis")
	err := h.SaveServerWithAutoAllow("vis", in, "1h")
	if err == nil || err.Error() != errSavedButPrefix+wantErr {
		t.Fatalf("got %v, want %q", err, errSavedButPrefix+wantErr)
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant armed for an unknown mode")
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := f.FindServer("vis"); s.AutoAllow {
		t.Fatal("forever flag set for an unknown mode")
	}

	if err := h.SetAutoAllow("vis", "1h"); err == nil || err.Error() != wantErr {
		t.Fatalf("got %v, want %q", err, wantErr)
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant armed for an unknown mode")
	}
}

// TestSaveRenameArmsUnderNewName: the new grant is armed under the name
// this save leaves the server with, not the original.
func TestSaveRenameArmsUnderNewName(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	in := inputFor(t, h, "vis")
	in.Name = "vis2"
	if err := h.SaveServerWithAutoAllow("vis", in, "15m"); err != nil {
		t.Fatal(err)
	}
	if g := grantOf(h, "vis2"); g == nil {
		t.Fatal("no grant under the new name")
	}
	if g := grantOf(h, "vis"); g != nil {
		t.Fatal("grant left behind under the old name")
	}
}
