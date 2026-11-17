package view

import (
	"hash/fnv"

	"github.com/egoist/mygo/ui"
)

// Carbon: true neutral greys (dark #1E1E1E, light #F5F5F5) and no decorative
// hue. The accent is near-white in dark and near-black in light. Colour is
// for meaning only: amber waits and auto-allow, red is danger, green is ok or
// connected, plus the host tile hues. The rail (top bar and sidebar) is one
// neutral step off the work area. The table lives in the 2026-11-16 visual
// redesign spec (palette replaced 2026-11-18).
const radiusControl, radiusCard, radiusDialog = 6, 10, 14

const railHoverAlpha = 0.08

// tokens are the colours with no ui.Theme field, plus the ones that do, so
// a view reads every colour from one place.
type tokens struct {
	Bg, Surface, Raised, SurfaceHover, Line, ControlLine, Divider, Text, Muted, Label                          ui.Color
	Accent, AccentHover, AccentPressed, AccentText, AccentTint                                                 ui.Color
	Danger, DangerFill, DangerFillHover, DangerFillPressed, DangerText, DangerTint, Wait, WaitTint, Ok, OkTint ui.Color
	TermBg, TermFg, Rail, RailText, RailButton, WaitFill, WaitFillHover, OnWaitFill, TileText, SwitchOn        ui.Color
	TileHues                                                                                                   [6]ui.Color
}

// tileHueIndex picks a host's tile hue from its name: FNV-1a 32 bit, modulo
// the number of hues, so a host keeps its colour across runs and renames only.
func tileHueIndex(name string) int {
	h := fnv.New32a()
	h.Write([]byte(name))
	return int(h.Sum32() % uint32(len(tokens{}.TileHues)))
}

func palette(dark bool) tokens {
	pick := func(d, l string) ui.Color {
		if dark {
			return ui.Hex(d)
		}
		return ui.Hex(l)
	}
	return tokens{
		Bg:                pick("#1E1E1E", "#F5F5F5"),
		Surface:           pick("#262626", "#FFFFFF"),
		Raised:            pick("#2D2D2D", "#ECECEC"),
		SurfaceHover:      pick("#363636", "#E2E2E2"),
		Line:              pick("#3C3C3C", "#DADADA"),
		ControlLine:       pick("#797979", "#838383"),
		Divider:           pick("#313131", "#E4E4E4"),
		Text:              pick("#EDEDED", "#1D1D1F"),
		Muted:             pick("#A0A0A0", "#636363"),
		Label:             pick("#C6C6C6", "#404040"),
		Accent:            pick("#EDEDED", "#1D1D1F"),
		AccentHover:       pick("#F0F0F0", "#3D3D3E"),
		AccentPressed:     pick("#C7C7C7", "#18181A"),
		AccentText:        pick("#141414", "#FFFFFF"),
		AccentTint:        pick("#343434", "#DADADA"),
		Danger:            pick("#EE8A74", "#AA3D2C"),
		DangerFill:        pick("#CC4B35", "#B9432F"),
		DangerFillHover:   pick("#BE432E", "#A63B2A"),
		DangerFillPressed: pick("#AD3B28", "#923425"),
		DangerText:        pick("#ffffff", "#ffffff"),
		DangerTint:        pick("#41302D", "#FBE3DE"),
		Wait:              pick("#E0A048", "#805B1D"),
		WaitTint:          pick("#3F3425", "#FBEFDC"),
		Ok:                pick("#6CC08F", "#386D4E"),
		OkTint:            pick("#2B3A31", "#E3F2EA"),
		TermBg:            pick("#141414", "#FFFFFF"),
		TermFg:            pick("#DADADA", "#1D1D1F"),
		Rail:              pick("#181818", "#E9E9E9"),
		RailText:          pick("#C6C6C6", "#404040"),
		// The face of a rail button and a chosen theme segment: white on the
		// light rail (a grey pill there reads as disabled), a lighter step on
		// the dark one.
		RailButton: pick("#2E2E2E", "#FFFFFF"),
		// Light: deep amber with white text, since a pale amber cannot reach
		// 3:1 on the light rail; its hover darkens for the same reason.
		WaitFill:      pick("#E9A23B", "#9A5800"),
		WaitFillHover: pick("#ECAF59", "#864C00"),
		OnWaitFill:    pick("#0E1012", "#FFFFFF"),
		// A switch track when on: green, since the near-white accent would sit
		// under the white knob. White on it is at least 3:1 in both themes.
		SwitchOn: pick("#2F8F5B", "#386D4E"),
		TileText: ui.Hex("#ffffff"),
		// Plum, blue, teal, rose, amber-brown, slate, muted to sit on the grey;
		// no green, which is the connected tile's. White text on each is at
		// least 4.5:1.
		TileHues: [6]ui.Color{
			ui.Hex("#7E4F86"), ui.Hex("#3D67A8"), ui.Hex("#2F7D78"), ui.Hex("#A44C68"), ui.Hex("#8A6230"), ui.Hex("#56657A"),
		},
	}
}

// railTheme scopes the rail: SidebarItem paints every item's icon in Accent,
// which on the rail is the neutral RailText (accentTheme gives the chosen one
// back its teal), and an unselected item's hover in SurfaceHover, a RailText
// tint here. Focus keeps the accent.
func railTheme(c *ui.Context) *ui.Theme {
	p := tok(c)
	t := *c.Theme()
	t.Accent, t.InactiveAccent = p.RailText, p.RailText
	t.SurfaceHover = railHover(p)
	return &t
}

// accentTheme is railTheme with the accent back, for the chosen sidebar item:
// its icon in Accent on the theme's AccentTint row.
func accentTheme(c *ui.Context) *ui.Theme {
	p := tok(c)
	t := *railTheme(c)
	t.Accent, t.InactiveAccent = p.Accent, p.Accent
	return &t
}

// switchTheme scopes a switch so its on track is green, not the accent.
func switchTheme(c *ui.Context) *ui.Theme {
	t := *c.Theme()
	t.Accent, t.InactiveAccent = tok(c).SwitchOn, tok(c).SwitchOn
	return &t
}

// onSwitch is ui.Switch with a green on state.
func onSwitch(c *ui.Context, on *bool) ui.Element {
	var sw ui.Element
	ui.Themed(c, switchTheme(c)).Background(ui.Color{}).Children(func() { sw = ui.Switch(c, on) })
	return sw
}

func railHover(p tokens) ui.Color { return p.RailText.Alpha(railHoverAlpha).Over(p.Rail) }

// railButton styles a top-bar button for the rail: RailText on RailButton, the
// control outline, and the hover tint.
func railButton(e ui.Element, p tokens) ui.Element {
	e = e.TextColor(p.RailText).Background(p.RailButton).Border(1, p.ControlLine)
	if e.Hovered() {
		e = e.Background(railHover(p))
	}
	return e
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
	return ui.Color{R: 29, G: 29, B: 31, A: light}
}

// cardShadow is the shadow of a card at rest.
func cardShadow(c *ui.Context) shadowSpec {
	return shadowSpec{0, 1, 3, 0, shade(c, 20, 90)}
}

// raisedShadow is for hover, dialogs and the AI column's left edge.
func raisedShadow(c *ui.Context) shadowSpec {
	return shadowSpec{0, 6, 18, 0, shade(c, 38, 150)}
}
