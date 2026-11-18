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
			{"label/surface", p.Label, p.Surface, 4.5},
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
			{"rail icon+label/pill", p.RailText, p.RailText.Alpha(railPillAlpha).Over(p.Rail), 4.5 + 0.5}, // comfortable margin
			{"rail icon/rail", p.RailText, p.Rail, 3}, {"rail icon/hover", p.RailText, railHover(p), 3},
			{"rail icon/pill", p.RailText, p.RailText.Alpha(railPillAlpha).Over(p.Rail), 3},
			{"controlLine/bg", p.ControlLine, p.Bg, 3}, {"controlLine/surface", p.ControlLine, p.Surface, 3}, {"controlLine/raised", p.ControlLine, p.Raised, 3},
			{"accent/bg (focus)", p.Accent, p.Bg, 3},
		} {
			if r := ratio(c.fg, c.bg); r < c.min {
				t.Errorf("dark=%v %s: %.2f < %.1f", dark, c.name, r, c.min)
			}
		}
	}
}

// Every ui.Color field of each theme differs from the stock theme, except
// the ones kept on purpose.
func TestThemesOverrideStock(t *testing.T) {
	kept := map[string]bool{
		"light.AccentText": true, // white on teal, which is the stock value
		"light.Scrollbar":  true, // stock scrollbar thumbs read fine on the warm neutrals
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
