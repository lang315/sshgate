package view

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lang315/sshgate/app/hubapi"
)

func TestParseThemeAndItsFile(t *testing.T) {
	for in, want := range map[string]string{"dark": "dark", "light": "light", "auto": "auto", "": "auto", "purple": "auto"} {
		if got := parseTheme(in); got != want {
			t.Errorf("parseTheme(%q) = %q", in, got)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "theme.json")
	if LoadTheme("") != "auto" || LoadTheme(path) != "auto" {
		t.Fatal("no file must read as auto")
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if LoadTheme(path) != "auto" {
		t.Fatal("a broken file must read as auto")
	}
	if err := saveTheme(filepath.Join(dir, "missing", "theme.json"), "dark"); err == nil {
		t.Fatal("a write into a missing folder should fail, quietly")
	}
	if err := saveTheme(path, "light"); err != nil || LoadTheme(path) != "light" {
		t.Fatal("round trip:", err)
	}
	if b, _ := os.ReadFile(path); string(b) != `{"theme":"light"}` {
		t.Fatalf("%s", b)
	}
}

func TestThemeControlAppliesKeepsAndCallsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	d := &fakeDoor{status: hubapi.Status{HasVault: true}}
	a := &App{Door: d, ThemeFile: path}
	c := &clock{}
	a.Now = c.now
	sh := &fakeShell{}
	a.Shell = sh
	tt := newTester(t, a)
	running(a, tt)
	for _, s := range []string{"Theme", "Dark", "☾", "Light", "☀", "Auto"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	if a.theme != "auto" {
		t.Fatal(a.theme)
	}
	d.resetCalls()
	clickT(t, tt, "Light")
	tt.flush()
	if a.theme != "light" || !reflect.DeepEqual(sh.themes, []string{"light"}) || LoadTheme(path) != "light" {
		t.Fatalf("theme %q, applied %v, kept %q", a.theme, sh.themes, LoadTheme(path))
	}
	if got := d.callList(); len(got) != 0 {
		t.Fatalf("the theme control called the hub: %v", got)
	}

	// The next launch starts in it; main has applied it, so the view does not again.
	sh2 := &fakeShell{}
	b := &App{Door: d, ThemeFile: path, Now: c.now, Shell: sh2}
	tt2 := newTester(t, b)
	running(b, tt2)
	if b.theme != "light" || len(sh2.themes) != 0 {
		t.Fatalf("theme %q, applied %v", b.theme, sh2.themes)
	}
}
