package hub

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/config"
)

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
