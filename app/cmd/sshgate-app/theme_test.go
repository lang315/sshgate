package main

import (
	"testing"

	"github.com/egoist/mygo"
)

func TestThemeSource(t *testing.T) {
	for pref, want := range map[string]mygo.ThemeSource{"dark": mygo.ThemeDark, "light": mygo.ThemeLight, "auto": mygo.ThemeSystem, "": mygo.ThemeSystem} {
		if got := themeSource(pref); got != want {
			t.Errorf("themeSource(%q) = %q, want %q", pref, got, want)
		}
	}
}
