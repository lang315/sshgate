package view

import (
	"math"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
)

func lum(c ui.Color) float64 {
	ch := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
}

func ratio(a, b ui.Color) float64 {
	la, lb := lum(a), lum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestPaletteContrast(t *testing.T) {
	for _, dark := range []bool{false, true} {
		p := palette(dark)
		for _, c := range []struct {
			name   string
			fg, bg ui.Color
			min    float64
		}{
			{"text/bg", p.Text, p.Bg, 4.5}, {"text/surface", p.Text, p.Surface, 4.5}, {"text/raised", p.Text, p.Raised, 4.5},
			{"muted/bg", p.Muted, p.Bg, 4.5}, {"muted/surface", p.Muted, p.Surface, 4.5}, {"muted/raised", p.Muted, p.Raised, 4.5},
			{"label/surface", p.Label, p.Surface, 4.5}, {"label/raised", p.Label, p.Raised, 4.5},
			{"accent/surface", p.Accent, p.Surface, 4.5}, {"accent/accentTint", p.Accent, p.AccentTint, 4.5},
			{"accentText/accent", p.AccentText, p.Accent, 4.5},
			{"danger/surface", p.Danger, p.Surface, 4.5}, {"danger/dangerTint", p.Danger, p.DangerTint, 4.5},
			{"white/dangerFill", p.DangerText, p.DangerFill, 4.5},
			{"white/dangerFillHover", p.DangerText, p.DangerFillHover, 4.5}, {"white/dangerFillPressed", p.DangerText, p.DangerFillPressed, 4.5},
			{"wait/raised", p.Wait, p.Raised, 4.5}, {"danger/raised", p.Danger, p.Raised, 4.5},
			{"wait/surface", p.Wait, p.Surface, 4.5}, {"wait/waitTint", p.Wait, p.WaitTint, 4.5},
			{"ok/surface", p.Ok, p.Surface, 4.5}, {"ok/raised", p.Ok, p.Raised, 4.5}, {"danger/termBg", p.Danger, p.TermBg, 4.5}, {"ok/okTint", p.Ok, p.OkTint, 4.5},
			{"termFg/termBg", p.TermFg, p.TermBg, 4.5},
			{"railText/rail", p.RailText, p.Rail, 4.5},
			{"accentText/ok (connected tile)", p.AccentText, p.Ok, 4.5},
			{"text/dangerTint", p.Text, p.DangerTint, 4.5}, {"text/waitTint", p.Text, p.WaitTint, 4.5},
			{"rail icon+label/hover", p.RailText, railHover(p), 4.5},
			{"rail icon/rail", p.RailText, p.Rail, 3}, {"rail icon/hover", p.RailText, railHover(p), 3},
			{"controlLine/bg", p.ControlLine, p.Bg, 3}, {"controlLine/surface", p.ControlLine, p.Surface, 3}, {"controlLine/raised", p.ControlLine, p.Raised, 3},
			{"accent/bg (focus)", p.Accent, p.Bg, 3},
			{"waitFill/rail", p.WaitFill, p.Rail, 3},
			{"railText/railButton", p.RailText, p.RailButton, 4.5}, {"controlLine/railButton", p.ControlLine, p.RailButton, 3}, {"railText/railButton hover", p.RailText, railHover(p), 4.5},
			{"controlLine/rail (railButton border)", p.ControlLine, p.Rail, 3},
			{"text/rail (home mark)", p.Text, p.Rail, 4.5},
			{"accent/rail (shield, focus)", p.Accent, p.Rail, 3},
			{"text/termBg (active tab)", p.Text, p.TermBg, 4.5}, {"accent/termBg (active tab bar)", p.Accent, p.TermBg, 3}, {"muted/termBg (tab close)", p.Muted, p.TermBg, 3},
			{"onWaitFill/waitFill", p.OnWaitFill, p.WaitFill, 4.5},
			{"onWaitFill/waitFillHover", p.OnWaitFill, p.WaitFillHover, 4.5}, {"waitFillHover/rail", p.WaitFillHover, p.Rail, 3},
			{"accent/waitTint (Resume)", p.Accent, p.WaitTint, 4.5},
			{"ok/bg (allowed)", p.Ok, p.Bg, 4.5},
			{"dangerFill/surface (Delete)", p.DangerFill, p.Surface, 3},
			{"white knob/switchOn", ui.Hex("#ffffff"), p.SwitchOn, 3},
			{"switchOn/bg", p.SwitchOn, p.Bg, 3}, {"switchOn/surface", p.SwitchOn, p.Surface, 3}, {"switchOn/raised", p.SwitchOn, p.Raised, 3},
		} {
			if r := ratio(c.fg, c.bg); r < c.min {
				t.Errorf("dark=%v %s: %.2f < %.1f", dark, c.name, r, c.min)
			}
		}
		for i, hue := range p.TileHues {
			if r := ratio(p.TileText, hue); r < 4.5 {
				t.Errorf("dark=%v white/tileHue[%d]: %.2f < 4.5", dark, i, r)
			}
			for j := 0; j < i; j++ {
				if p.TileHues[j] == hue {
					t.Errorf("dark=%v tileHue[%d] repeats [%d]", dark, i, j)
				}
			}
		}
	}
}

// Every ui.Color field of each theme differs from the stock theme, except
// the ones kept on purpose.
func TestThemesOverrideStock(t *testing.T) {
	kept := map[string]bool{
		"light.AccentText": true, // kept on purpose when it equals the stock value
		"light.Scrollbar":  true, // stock scrollbar thumbs read fine on the cool neutrals
		"dark.Scrollbar":   true,
	}
	light, dark := Themes()
	for _, c := range []struct {
		name       string
		got, stock *ui.Theme
	}{{"light", light, ui.LightTheme()}, {"dark", dark, ui.DarkTheme()}} {
		gv, sv := reflect.ValueOf(*c.got), reflect.ValueOf(*c.stock)
		ct := reflect.TypeOf(ui.Color{})
		for i := 0; i < gv.NumField(); i++ {
			f := gv.Type().Field(i)
			if f.Type != ct || !f.IsExported() || kept[c.name+"."+f.Name] {
				continue
			}
			g, s := gv.Field(i).Interface().(ui.Color), sv.Field(i).Interface().(ui.Color)
			if g.R == s.R && g.G == s.G && g.B == s.B && g.A == s.A {
				t.Errorf("%s theme: %s still the stock value", c.name, f.Name)
			}
		}
	}
}

func TestTileHueIndex(t *testing.T) {
	for name, want := range map[string]int{"": 1, "a": 4, "fviainboxes-server": 5, "buildpc": 2, "key": 4, "long": 1, "quyen-gmail": 2, "hang-nguyen": 0} {
		if got := tileHueIndex(name); got != want {
			t.Errorf("tileHueIndex(%q) = %d, want %d", name, got, want)
		}
	}
}
