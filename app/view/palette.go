package view

import (
	"hash/fnv"

	"github.com/egoist/mygo/ui"
)

// Warm neutrals with a teal shell; the table lives in the 2026-11-16 visual
// redesign spec.
const radiusControl, radiusCard, radiusDialog = 6, 10, 14

// railTextHex is RailText in both themes.
const railHoverAlpha, railPillAlpha = 0.08, 0.11

const railTextHex = "#E6F2F0"

// tokens are the colours with no ui.Theme field, plus the ones that do, so
// a view reads every colour from one place.
type tokens struct {
	Bg, Surface, Raised, SurfaceHover, Line, ControlLine, Divider, Text, Muted, Label                          ui.Color
	Accent, AccentHover, AccentPressed, AccentText, AccentTint                                                 ui.Color
	Danger, DangerFill, DangerFillHover, DangerFillPressed, DangerText, DangerTint, Wait, WaitTint, Ok, OkTint ui.Color
	TermBg, TermFg, Rail, RailText, WaitFill, OnWaitFill, TileText                                             ui.Color
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
		Bg:                pick("#161514", "#F6F4F0"),
		Surface:           pick("#1C1A19", "#FFFDFB"),
		Raised:            pick("#23211F", "#EEEBE5"),
		SurfaceHover:      pick("#2B2927", "#E6E2DA"),
		Line:              pick("#3D3A37", "#DFDAD2"),
		ControlLine:       pick("#7A746D", "#857E73"),
		Divider:           pick("#2E2B29", "#E6E2DB"),
		Text:              pick("#ECE8E0", "#1F1C19"),
		Muted:             pick("#9A948C", "#6B655D"),
		Label:             pick("#B8B3AB", "#4A453F"),
		Accent:            pick("#56BFB9", "#12766F"),
		AccentHover:       pick("#6CCAC4", "#0F6A64"),
		AccentPressed:     pick("#45A9A3", "#0C5A55"),
		AccentText:        pick("#100F0E", "#FFFDFB"),
		AccentTint:        pick("#16312F", "#dcf1ec"),
		Danger:            pick("#F28B73", "#B5412F"),
		DangerFill:        ui.Hex("#B5412F"),
		DangerFillHover:   ui.Hex("#A23A2A"),
		DangerFillPressed: ui.Hex("#8F3325"),
		DangerText:        ui.Hex("#ffffff"),
		DangerTint:        pick("#3D1D18", "#fbe2dc"),
		Wait:              pick("#E9A23B", "#8B6123"),
		WaitTint:          pick("#3A2C14", "#fcefdc"),
		Ok:                pick("#6CC58F", "#3D7554"),
		OkTint:            pick("#1A3324", "#e9f6ee"),
		TermBg:            pick("#100F0E", "#FFFDFB"),
		TermFg:            pick("#D8D4CC", "#1F1C19"),
		Rail:              pick("#134542", "#0F5F59"),
		RailText:          ui.Hex(railTextHex),
		WaitFill:          ui.Hex("#E9A23B"),
		OnWaitFill:        ui.Hex("#0E1012"),
		TileText:          ui.Hex("#ffffff"),
		// Teal, blue, violet, rose, amber-brown, slate; no green, which is the
		// connected tile's. White text on each is at least 4.5:1.
		TileHues: [6]ui.Color{
			pick("#15807A", "#0F766E"), pick("#3A6BC4", "#2563B8"), pick("#7B52C2", "#6D43B5"),
			pick("#BE4767", "#B03A5B"), pick("#9A6516", "#8A5A12"), pick("#566B82", "#475A70"),
		},
	}
}

// railTheme scopes the rail: SidebarItem paints its icon in Accent and an
// unselected item's hover in SurfaceHover, which on the rail must be RailText
// and a RailText tint (the light rail swallows the stock pair).
func railTheme(c *ui.Context) *ui.Theme {
	p := tok(c)
	t := *c.Theme()
	t.Accent, t.InactiveAccent, t.Focus = p.RailText, p.RailText, p.RailText
	t.SurfaceHover = railHover(p)
	return &t
}

func railHover(p tokens) ui.Color { return p.RailText.Alpha(railHoverAlpha).Over(p.Rail) }

// railButton styles a top-bar button for the rail: RailText on a pill, a
// RailText outline, and the hover tint.
func railButton(e ui.Element, p tokens) ui.Element {
	e = e.TextColor(p.RailText).Background(railButtonFill(p)).Border(1, railButtonBorder(p))
	if e.Hovered() {
		e = e.Background(railHover(p))
	}
	return e
}

// waitFillHover is the lit AI button under the pointer: a little lighter, since
// a darker amber would fall under 3:1 against the light rail.
func waitFillHover(p tokens) ui.Color { return p.WaitFill.Mix(ui.Hex("#FFFFFF"), 0.12) }

func railButtonFill(p tokens) ui.Color   { return p.RailText.Alpha(railPillAlpha).Over(p.Rail) }
func railButtonBorder(p tokens) ui.Color { return p.RailText.Alpha(0.6).Over(p.Rail) }

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
	return ui.Color{R: 31, G: 28, B: 25, A: light}
}

// cardShadow is the shadow of a card at rest.
func cardShadow(c *ui.Context) shadowSpec {
	return shadowSpec{0, 1, 3, 0, shade(c, 20, 90)}
}

// raisedShadow is for hover, dialogs and the AI column's left edge.
func raisedShadow(c *ui.Context) shadowSpec {
	return shadowSpec{0, 6, 18, 0, shade(c, 38, 150)}
}
