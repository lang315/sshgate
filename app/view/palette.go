package view

import (
	"hash/fnv"

	"github.com/egoist/mygo/ui"
)

// Cool neutrals with a teal accent; the rail (top bar and sidebar) is one
// neutral step off the work area, not a colour. The table lives in the
// 2026-11-16 visual redesign spec.
const radiusControl, radiusCard, radiusDialog = 6, 10, 14

const railHoverAlpha = 0.08

// tokens are the colours with no ui.Theme field, plus the ones that do, so
// a view reads every colour from one place.
type tokens struct {
	Bg, Surface, Raised, SurfaceHover, Line, ControlLine, Divider, Text, Muted, Label                          ui.Color
	Accent, AccentHover, AccentPressed, AccentText, AccentTint                                                 ui.Color
	Danger, DangerFill, DangerFillHover, DangerFillPressed, DangerText, DangerTint, Wait, WaitTint, Ok, OkTint ui.Color
	TermBg, TermFg, Rail, RailText, RailButton, WaitFill, WaitFillHover, OnWaitFill, TileText                  ui.Color
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
		Bg:                pick("#16181B", "#F4F5F7"),
		Surface:           pick("#1C1F23", "#FCFCFD"),
		Raised:            pick("#23272C", "#EDEFF2"),
		SurfaceHover:      pick("#2B2F35", "#E4E7EB"),
		Line:              pick("#363B42", "#DADEE3"),
		ControlLine:       pick("#737B86", "#7D8590"),
		Divider:           pick("#2A2E34", "#E3E6EA"),
		Text:              pick("#E5E8EC", "#1A1E23"),
		Muted:             pick("#959CA7", "#5D6570"),
		Label:             pick("#B3B9C2", "#3F4650"),
		Accent:            pick("#55B8B2", "#0F726D"),
		AccentHover:       pick("#6BC4BF", "#0C6662"),
		AccentPressed:     pick("#45A29D", "#0A5754"),
		AccentText:        pick("#0F1114", "#FCFCFD"),
		AccentTint:        pick("#172E2E", "#DCEFEC"),
		Danger:            pick("#EE8A74", "#B5412F"),
		DangerFill:        ui.Hex("#B9432F"),
		DangerFillHover:   ui.Hex("#A63B2A"),
		DangerFillPressed: ui.Hex("#923425"),
		DangerText:        ui.Hex("#ffffff"),
		DangerTint:        pick("#3B1D21", "#FBE3DE"),
		Wait:              pick("#E0A048", "#87601F"),
		WaitTint:          pick("#30281A", "#FBEFDC"),
		Ok:                pick("#6CC08F", "#3B7352"),
		OkTint:            pick("#1B2D24", "#E6F4EC"),
		TermBg:            pick("#0F1114", "#FCFCFD"),
		TermFg:            pick("#D4D8DE", "#1A1E23"),
		Rail:              pick("#1D2024", "#EBEDF0"),
		RailText:          pick("#BFC5CD", "#3F4650"),
		// The face of a rail button and a chosen theme segment: white on the
		// light rail (a grey pill there reads as disabled), a lighter step on
		// the dark one.
		RailButton: pick("#2F3237", "#FCFCFD"),
		// Light: deep amber with white text, since a pale amber cannot reach
		// 3:1 on the light rail; its hover darkens for the same reason.
		WaitFill:      pick("#E9A23B", "#9A5800"),
		WaitFillHover: pick("#ECAF59", "#864C00"),
		OnWaitFill:    pick("#0E1012", "#FFFFFF"),
		TileText:      ui.Hex("#ffffff"),
		// Plum, blue, violet, rose, amber-brown, slate, muted to sit on the
		// grey; no green, which is the connected tile's, and no teal, which is
		// the accent's. White text on each is at least 4.5:1.
		TileHues: [6]ui.Color{
			ui.Hex("#7E4F86"), ui.Hex("#3D67A8"), ui.Hex("#6A55A8"),
			ui.Hex("#A44C68"), ui.Hex("#8A6230"), ui.Hex("#56657A"),
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
	return ui.Color{R: 26, G: 30, B: 35, A: light}
}

// cardShadow is the shadow of a card at rest.
func cardShadow(c *ui.Context) shadowSpec {
	return shadowSpec{0, 1, 3, 0, shade(c, 20, 90)}
}

// raisedShadow is for hover, dialogs and the AI column's left edge.
func raisedShadow(c *ui.Context) shadowSpec {
	return shadowSpec{0, 6, 18, 0, shade(c, 38, 150)}
}
