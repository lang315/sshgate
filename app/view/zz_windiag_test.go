package view

import (
	"testing"
	"time"
)

func TestZZWinScroll(t *testing.T) {
	a, tt, c, _ := colApp(t)
	tt.SetSize(800, 300)
	for _, id := range []string{"s1", "s2", "s3", "s4", "s5"} {
		note(t, a, tt, "pending", map[string]any{"request": reqN(id, "box", "echo "+id)})
	}
	c.add(time.Second)
	tt.Frame()
	t.Logf("scroll %+v", a.col.scroll)
	for _, s := range []string{"AI requests  5", "echo s1", "echo s2", "Allow", "Deny", "box", "Nothing ran on auto-allow.", "Auto-allowed"} {
		r, ok := tt.Find(s)
		t.Logf("find %-28q ok=%v %+v", s, ok, r)
	}
	for _, p := range [][2]float32{{700, 150}, {650, 120}, {600, 200}, {700, 100}, {620, 180}} {
		tt.Move(p[0], p[1])
		tt.Scroll(p[0], p[1], 0, 120)
		tt.Frame()
		t.Logf("after wheel at %v: Y=%v MaxY=%v", p, a.col.scroll.Y, a.col.scroll.MaxY)
	}
}

func TestZZWinBanner(t *testing.T) {
	a, tt, d, c := pausedApp(t)
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "ls")})
	pass(c, tt)
	for _, s := range []string{"Allow", "Deny", pausedBox, "Resume", "Auto-allowed", "ls"} {
		r, ok := tt.Find(s)
		t.Logf("with banner %-12.12q ok=%v %+v", s, ok, r)
	}
	t.Logf("col scroll %+v", a.col.scroll)
	d.set(func() { d.servers[0].AutoAllow = nil })
	a.Update(a.reloadServers)
	tt.flush()
	for _, s := range []string{"Allow", "Deny", "Auto-allowed", "ls"} {
		r, ok := tt.Find(s)
		t.Logf("after %-12.12q ok=%v %+v", s, ok, r)
	}
	t.Logf("col scroll %+v", a.col.scroll)
}
