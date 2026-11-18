package view

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
)

func dialogRect(t *testing.T, tt *tester) ui.Rect {
	t.Helper()
	info, ok := tt.AccessByRole(ui.RoleDialog, "")
	if !ok {
		t.Fatalf("no dialog: %q", tt.Texts())
	}
	return info.Bounds
}

func TestModalPanelLook(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	for _, dark := range []bool{false, true} {
		_, tt, _, _ := settingsApp(t, "7d", nil)
		tt.SetDark(dark)
		tt.Advance(time.Second)
		tt.Frame()
		r := dialogRect(t, tt)
		patch := ui.Rect{X: r.X + 8, Y: r.Y + 8, W: 4, H: 4}
		if n := countNear(shot(tt), patch, palette(dark).Surface, 2); n != 16 {
			t.Fatalf("dark=%v: %d of 16 pixels are Surface", dark, n)
		}
	}
}

func TestModalEntersThenRests(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	_, tt, _, _ := settingsApp(t, "7d", nil)
	if !tt.FramePending() {
		t.Fatal("no frame pending right after opening")
	}
	r := grow(dialogRect(t, tt), 20)
	first := shot(tt)
	tt.Advance(40 * time.Millisecond)
	tt.Frame()
	mid := shot(tt)
	tt.Advance(time.Second)
	tt.Frame()
	rest := shot(tt)
	if sameRegion(first, rest, r) {
		t.Fatal("the dialog did not change after its entrance")
	}
	if sameRegion(mid, first, r) || sameRegion(mid, rest, r) {
		t.Fatal("no mid-fade frame: the entrance went instant")
	}
	if tt.FramePending() {
		t.Fatal("frames still pending at rest")
	}
}

func TestModalReduceMotionAtRest(t *testing.T) {
	ui.SetDefaultThemes(Themes())
	t.Cleanup(func() { ui.SetDefaultThemes(nil, nil) })
	d := &fakeDoor{status: hubapi.Status{HasVault: true, SoftLockLimit: "7d"}}
	_, tt, _, _ := newApp(t, d)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.flush()
	clickT(t, tt, "Settings")
	r := grow(dialogRect(t, tt), 20)
	first := shot(tt)
	tt.Advance(time.Second)
	tt.Frame()
	if !sameRegion(first, shot(tt), r) {
		t.Fatal("the dialog changed after its first frame")
	}
}

func TestEnterCancelsSettingsDuringEntrance(t *testing.T) {
	a, tt, _, _ := settingsApp(t, "7d", nil)
	tt.Key(0, ui.KeyEnter)
	if a.settings != nil {
		t.Fatal("Settings still open")
	}
}

func TestEveryDialogUsesModal(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if f == "modal.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "ui.Modal(") {
			t.Errorf("%s calls ui.Modal; use modal", f)
		}
	}
}
