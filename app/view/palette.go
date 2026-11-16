package view

import "github.com/egoist/mygo/ui"

// Warm neutrals with a teal shell; the table lives in the 2026-11-16 visual
// redesign spec.
const radiusControl, radiusCard, radiusDialog = 6, 10, 14

// railTextHex is RailText in both themes; the rail icons paint with it, as an
// SVG takes no token.
const railTextHex = "#E6F2F0"

// tokens are the colours with no ui.Theme field, plus the ones that do, so
// a view reads every colour from one place.
type tokens struct {
	Bg, Surface, Raised, SurfaceHover, Line, ControlLine, Divider, Text, Muted, Label ui.Color
	Accent, AccentHover, AccentPressed, AccentText, AccentTint                        ui.Color
	Danger, DangerFill, DangerTint, Wait, WaitTint, Ok, OkTint                        ui.Color
	TermBg, TermFg, Rail, RailText                                                    ui.Color
}

func palette(dark bool) tokens {
	pick := func(d, l string) ui.Color {
		if dark {
			return ui.Hex(d)
		}
		return ui.Hex(l)
	}
	return tokens{
		Bg:            pick("#14171A", "#f5f6fa"),
		Surface:       pick("#1A1E22", "#ffffff"),
		Raised:        pick("#21262B", "#eef0f6"),
		SurfaceHover:  pick("#282E34", "#e6e9f1"),
		Line:          pick("#3A4148", "#dde0ea"),
		ControlLine:   pick("#737B82", "#7C8196"),
		Divider:       pick("#2B3137", "#e6e8ef"),
		Text:          pick("#ECE8E0", "#1b1f2e"),
		Muted:         pick("#8B908E", "#62698a"),
		Label:         pick("#B3B5B1", "#474e6b"),
		Accent:        pick("#56BFB9", "#12766F"),
		AccentHover:   pick("#6CCAC4", "#0F6A64"),
		AccentPressed: pick("#45A9A3", "#0C5A55"),
		AccentText:    pick("#0E1012", "#ffffff"),
		AccentTint:    pick("#16312F", "#dcf1ec"),
		Danger:        pick("#F28B73", "#B5412F"),
		DangerFill:    ui.Hex("#B5412F"),
		DangerTint:    pick("#3D1D18", "#fbe2dc"),
		Wait:          pick("#E9A23B", "#8B6123"),
		WaitTint:      pick("#3A2C14", "#fcefdc"),
		Ok:            pick("#6CC58F", "#3F7A57"),
		OkTint:        pick("#1A3324", "#e9f6ee"),
		TermBg:        pick("#0E1012", "#ffffff"),
		TermFg:        pick("#D8D4CC", "#1b1f2e"),
		Rail:          pick("#0F2A29", "#0F5F59"),
		RailText:      ui.Hex(railTextHex),
	}
}

func tok(c *ui.Context) tokens { return palette(c.Theme().Dark) }

// Themes is the app's light and dark ui.Theme, for ui.SetDefaultThemes.
func Themes() (light, dark *ui.Theme) {
	build := func(t *ui.Theme, isDark bool) *ui.Theme {
		p := palette(isDark)
		t.Background, t.Field = p.Bg, p.Bg
		t.Surface, t.SurfaceHover, t.SurfacePressed = p.Raised, p.SurfaceHover, p.Line
		t.Border = p.Line
		t.ControlBorder, t.ButtonBorder = p.ControlLine, p.ControlLine
		t.Text, t.TextMuted = p.Text, p.Muted
		t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = p.Accent, p.AccentHover, p.AccentPressed, p.AccentText
		t.Focus, t.InactiveAccent = p.Accent, p.Muted
		t.Selection, t.RowSelected, t.RowSelectedText, t.RowHover = p.AccentTint, p.AccentTint, p.Text, p.Raised
		t.Danger, t.Warning, t.Success = p.Danger, p.Wait, p.Ok
		t.Inverse, t.InverseText = p.Text, p.Bg
		t.Radius = radiusControl
		return t
	}
	return build(ui.LightTheme(), false), build(ui.DarkTheme(), true)
}

// shadowSpec is one box shadow for an element.
type shadowSpec struct {
	X, Y, Blur, Spread float32
	Color              ui.Color
}

// On puts the shadow on e.
func (s shadowSpec) On(e ui.Element) ui.Element { return e.Shadow(s.X, s.Y, s.Blur, s.Spread, s.Color) }

func shade(c *ui.Context, light, dark uint8) ui.Color {
	if c.Theme().Dark {
		return ui.Color{A: dark}
	}
	return ui.Color{R: 27, G: 31, B: 46, A: light}
}

// cardShadow is the shadow of a card at rest.
func cardShadow(c *ui.Context) shadowSpec {
	return shadowSpec{0, 1, 3, 0, shade(c, 20, 90)}
}

// raisedShadow is for hover, dialogs and the AI column's left edge.
func raisedShadow(c *ui.Context) shadowSpec {
	return shadowSpec{0, 6, 18, 0, shade(c, 38, 150)}
}
