package view

import (
	"encoding/json"
	"os"

	"github.com/egoist/mygo/ui"
)

// themes is the theme control, in Electron's order (ThemeControl.tsx): each
// segment is named and tipped with its name and shows its glyph.
var themes = []struct{ pref, name, glyph string }{
	{"dark", "Dark", "☾"},
	{"light", "Light", "☀"},
	{"auto", "Auto", "Auto"},
}

// parseTheme is Electron's parsePref: anything but dark or light is auto.
func parseTheme(v string) string {
	if v == "dark" || v == "light" {
		return v
	}
	return "auto"
}

// LoadTheme reads the theme kept at path. A display preference, not
// security state: any failure, path "" included, means auto.
func LoadTheme(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "auto"
	}
	var p struct {
		Theme string `json:"theme"`
	}
	if json.Unmarshal(b, &p) != nil {
		return "auto"
	}
	return parseTheme(p.Theme)
}

// saveTheme keeps pref at path ("" keeps nothing), replacing the file whole.
func saveTheme(path, pref string) error {
	if path == "" {
		return nil
	}
	b, _ := json.Marshal(map[string]string{"theme": parseTheme(pref)})
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// setTheme applies a choice app-wide and keeps it for the next launch; a
// failed write only loses the choice, as in Electron.
func (a *App) setTheme(pref string) {
	a.theme = pref
	_ = saveTheme(a.ThemeFile, pref)
	a.shell().SetTheme(pref)
}

// buildThemeControl is the Dark / Light / Auto switch of the top row. It
// calls no hub method.
func (a *App) buildThemeControl(c *ui.Context) {
	t := c.Theme()
	sel := len(themes) - 1
	for i, th := range themes {
		if th.pref == a.theme {
			sel = i
		}
	}
	p := ui.SegmentedBase(c, &sel, len(themes))
	p.Track.Label("Theme").Children(func() {
		for i, th := range themes {
			seg := p.Segment(i).Label(th.name).Tooltip(th.name).Padding(4, 8).Radius(6)
			if i == sel {
				seg.Background(t.Accent.Alpha(0.2))
			}
			seg.Children(func() { ui.Text(c, th.glyph) })
		}
	})
	if p.Track.Changed() {
		a.setTheme(themes[sel].pref)
	}
}
