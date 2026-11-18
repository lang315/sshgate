package view

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

func capFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

func (a *App) buildReady(c *ui.Context) {
	if !a.aiOpen {
		a.col.mounted, a.col.seen, a.col.fading, a.pausedCol, a.aiFaded = false, nil, nil, nil, false
	}
	now := a.Now()
	autoN := len(model.AutoHostNames(a.servers, now))
	liveAuto := autoN - len(model.PausedHosts(a.servers))
	for _, s := range a.servers {
		if model.IsActive(s.AutoAllow, now) && s.AutoAllow.Until != "" {
			// Timed chips, the AI button's count and the Stop buttons follow
			// the app's clock: a redraw, never a hub call (Electron's 15 s).
			c.After(15 * time.Second)
			break
		}
	}
	ui.Column(c).Fill().Children(func() {
		a.buildTopBar(c, autoN, liveAuto)
		ui.Row(c).Grow(1).Children(func() {
			a.buildSidebar(c)
			// HeightPercent bounds the column so the host list's Scroll scrolls.
			// Banners sit here, beside the AI column (Electron's main.work), so
			// one appearing never moves Allow.
			ui.Column(c.Key("work")).Grow(1).MinWidth(0).Clip().HeightPercent(100).Children(func() {
				if a.status != nil && a.status.StoreError != "" {
					ui.Row(c).Padding(8, 12).Background(tok(c).DangerTint).Role(ui.RoleStatus).Children(func() {
						ui.Text(c, capFirst(model.DisplayText(a.status.StoreError)))
					})
				}
				if text := model.RanLockedText(a.ranLocked); text != "" {
					ui.Row(c).Gap(8).Padding(8, 12).Background(tok(c).WaitTint).Role(ui.RoleStatus).Children(func() {
						ui.Text(c, model.DisplayText(text)).Grow(1)
						if ui.Button(c, "Dismiss").Clicked() {
							a.ranLocked = nil
						}
					})
				}
				if a.alert != "" {
					ui.Row(c).Gap(8).Padding(8, 12).Background(tok(c).WaitTint).Children(func() {
						ui.Text(c, model.DisplayText(a.alert)).Grow(1)
						if ui.Button(c, "Dismiss").Clicked() {
							a.alert = ""
						}
					})
				}
				if t, ok := a.tabs.Get(a.tabs.Active); ok {
					a.pausedHome = nil
					a.buildTab(c, t)
					return
				}
				if a.tabs.Active == model.KeychainTab {
					a.pausedHome = nil
					a.fadeView(c, "keychain", a.buildKeychain)
					return
				}
				if a.tabs.Active == model.AuditTab {
					a.pausedHome = nil
					a.fadeView(c, "audit", a.buildAudit)
					return
				}
				a.fadeView(c, "hosts", a.buildHosts)
			})
			switch {
			case a.editor != nil:
				a.buildEditor(c)
			case a.imp != nil:
				a.buildImport(c)
			}
			if a.aiOpen {
				a.buildAIColumn(c)
			}
		})
	})
	a.buildHostKeyDialogs(c)
	a.buildDeleteConfirm(c)
	a.buildKeyDialogs(c)
	a.buildSettings(c)
}

// fadeView builds the Hosts or Audit view; one just entered fades in over
// 120 ms (the leaving one is gone at once). It animates only while the fade
// runs: Animate on every frame of a view at rest costs frames.
func (a *App) fadeView(c *ui.Context, key string, build func(*ui.Context)) {
	if a.viewKey != key {
		// The first view after start shows at once, and so does every view under
		// Reduce Motion (Animate would show one transparent frame first).
		a.viewFading = a.viewKey != "" && !c.Preferences().ReduceMotion
		a.viewKey, a.viewStep = key, 0
		a.viewFades++
	}
	col := ui.Column(c).Grow(1).HeightPercent(100)
	if a.viewFading {
		// A fresh key starts at its first target, so the first frame targets 0.
		target := float32(1)
		if a.viewStep == 0 {
			target = 0
		}
		a.viewStep++
		f := col.Animate(fmt.Sprint("viewfade", a.viewFades), target, 120*time.Millisecond)
		col.Opacity(f)
		if a.viewStep > 1 && f >= 1 {
			a.viewFading = false
		} else {
			c.Invalidate()
		}
	}
	col.Children(func() { build(c) })
}

func (a *App) buildHosts(c *ui.Context) {
	t := c.Theme()
	p := tok(c)
	now := a.Now()
	shown := model.FilterHosts(a.servers, a.query)
	ui.Scroll(c).Grow(1).Padding(16).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.TextInput(c.Key("search"), &a.query).Placeholder("Search hosts").Label("Search hosts").Grow(1).Bind(&a.searchH)
			if ui.PrimaryButton(c, "New host").Clicked() {
				a.openEditor("", false)
			}
			if ui.Button(c, "Import from SSH config").Clicked() {
				a.openImport()
			}
		})
		ui.Text(c, fmt.Sprintf("Hosts · %d", len(shown))).Bold()
		a.buildPaused(c, &a.pausedHome)
		switch {
		case len(a.servers) == 0:
			ui.Text(c, "No hosts yet. Add one with New host.").TextColor(t.TextMuted)
		case len(shown) == 0:
			ui.Text(c, fmt.Sprintf(`No hosts match "%s"`, model.DisplayText(strings.TrimSpace(a.query)))).TextColor(t.TextMuted)
		}
		ui.Row(c).Wrap().Gap(12).AlignItems(ui.Stretch).Children(func() {
			for name := range a.lift { // a host that is gone has no card to finish its fade
				if !slices.ContainsFunc(shown, func(s hubapi.Server) bool { return s.Name == name }) {
					delete(a.lift, name)
				}
			}
			for _, s := range shown {
				name := s.Name
				shownName := model.DisplayText(name)
				card := ui.Column(c.Key("host-"+name)).Width(240).MinWidth(200).Gap(8).Padding(12).Radius(radiusCard).Border(1, p.Line).Background(p.Surface).Justify(ui.SpaceBetween)
				hov := card.Hovered()
				// Only a hovered card, or one still fading out, calls Animate: one on
				// every card at rest made each frame about 7x dearer under -race.
				lift, lifting := a.lift[name]
				if hov || lifting {
					if !lifting {
						card.Animate("lift", 0, 120*time.Millisecond) // a first Animate starts at its target
					}
					target := float32(0)
					if hov {
						target = 1
					}
					lift = card.Animate("lift", target, 120*time.Millisecond)
					if a.lift == nil {
						a.lift = map[string]float32{}
					}
					if hov || lift > 0 {
						a.lift[name] = lift
					} else {
						delete(a.lift, name)
					}
					lerpShadow(cardShadow(c), raisedShadow(c), lift).On(card)
				} else {
					cardShadow(c).On(card)
				}
				iconCol := p.Muted
				if hov {
					iconCol = p.Text
				}
				card.Children(func() {
					// Flat inside the card: no face or border at rest, a tint on hover.
					open := ui.Button(c, "").Label("Open "+shownName).Justify(ui.Start).Padding(4, 0) // flush with the chips below
					open.Background(ui.Color{}).Border(0, ui.Color{})
					if open.Hovered() {
						open.Background(p.SurfaceHover)
					}
					if open.Children(func() {
						ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
							a.buildTile(c, p, s, shownName, now)
							ui.Column(c).Children(func() {
								ui.Text(c, shownName).Bold().SingleLine()
								ui.Text(c, model.DisplayText(model.Addr(s))).Font("monospace").TextColor(p.Muted).SingleLine()
							})
						})
					}).Clicked() {
						a.openTab(name)
					}
					// Wraps rather than overflows the card: the chips (left) take what the
					// actions leave, the actions (right) drop to a line of their own when short.
					ui.Row(c).Gap(6).Wrap().Justify(ui.End).Children(func() {
						ui.Row(c).Gap(6).Wrap().Grow(1).Basis(60).Children(func() {
							if s.AIVisible {
								chip(c, "AI", p.Accent, p.AccentTint, "")
							}
							if s.HostKey == "" {
								chip(c, "New key", p.Wait, p.WaitTint, "")
							}
							if label := model.ChipLabel(s.AutoAllow, now); label != "" {
								chip(c, label, p.Wait, p.WaitTint, "")
							}
							if n := model.RunningTunnels(a.tunnels, name); n > 0 {
								chip(c, fmt.Sprintf("%d ⇄", n), p.Accent, p.AccentTint, "Running tunnels")
							}
						})
						ui.Row(c).Gap(6).Children(func() {
							// Always shown while a grant is on or paused: it must be stoppable at a glance.
							if model.OnOrPaused(s.AutoAllow, now) {
								if ui.Button(c, "\u25a0").Label("Stop auto-allow "+shownName).Tooltip("Stop auto-allow").Padding(6, cardButtonPadX).Clicked() {
									a.setAuto([]string{name}, "off", "stop")
								}
							}
							if iconButton(c, "Files", shownName, cardFolderIcon, iconCol) {
								a.openFiles(name)
							}
							if iconButton(c, "Tunnels", shownName, tunnelIcon, iconCol) {
								a.openTunnels(name)
							}
							if iconButton(c, "Edit", shownName, editIcon, iconCol) {
								a.openEditor(name, false)
							}
							if iconButton(c, "Delete", shownName, trashIcon, iconCol) {
								a.askDelete(name)
							}
						})
					})
				})
			}
		})
		path := ""
		if a.status != nil {
			path = a.status.StorePath
		}
		ui.Text(c, "Vault file: "+model.DisplayText(path)+" — copy it to back up.").TextColor(t.TextMuted)
	})
}

// buildTile is the host's 36 px letter tile: Ok with a "connected" dot while a
// terminal is open, Accent otherwise, inside the grant ring.
func (a *App) buildTile(c *ui.Context, p tokens, s hubapi.Server, shownName string, now time.Time) {
	ring := model.GrantRing(s.AutoAllow, now)
	wrap := ui.Box(c).Size(44, 44).Center()
	if ring.Show {
		wrap.Label(model.ChipLabel(s.AutoAllow, now))
		wrap.Draw(func(pt *ui.Painter, r ui.Rect) {
			path := clockCircle(r.X+r.W/2, r.Y+r.H/2, r.W/2-1.5)
			if ring.Dashed {
				path.Dash(0, 4, 3)
			}
			pt.StrokePath(path.Trim(0, float32(ring.Frac)), 2, p.Wait)
		})
	}
	wrap.Children(func() {
		bg := p.Accent
		connected := a.tabs.OpenCount(s.Name) > 0
		if connected {
			bg = p.Ok
		}
		ui.Box(c).Size(36, 36).Radius(8).Background(bg).Center().Children(func() {
			ui.Text(c, model.Tile(shownName)).Bold().TextColor(p.AccentText)
		})
		if connected {
			ui.Box(c).Absolute().Right(2).Bottom(2).Size(10, 10).Radius(5).Background(p.Ok).Border(2, p.Surface).Label("connected")
		}
	})
}

// chip is a tinted pill; tip, when set, is its tooltip.
func chip(c *ui.Context, text string, fg, bg ui.Color, tip string) {
	ui.Row(c).Padding(2, 8).Radius(10).Background(bg).Children(func() {
		t := ui.Text(c, text).TextColor(fg).SingleLine()
		if tip != "" {
			t.Tooltip(tip)
		}
	})
}

// lerpShadow moves from one shadow to another as f goes from 0 to 1.
func lerpShadow(from, to shadowSpec, f float32) shadowSpec {
	m := func(a, b float32) float32 { return a + (b-a)*f }
	mc := func(a, b uint8) uint8 { return uint8(m(float32(a), float32(b))) }
	return shadowSpec{m(from.X, to.X), m(from.Y, to.Y), m(from.Blur, to.Blur), m(from.Spread, to.Spread),
		ui.Color{R: mc(from.Color.R, to.Color.R), G: mc(from.Color.G, to.Color.G), B: mc(from.Color.B, to.Color.B), A: mc(from.Color.A, to.Color.A)}}
}

// clockCircle is a circle that starts at 12 o'clock and runs clockwise, so a
// Trim of it reads as time left (ui.Path.Circle starts at 3).
func clockCircle(cx, cy, r float32) *ui.Path {
	const k = 0.5522847498
	return (&ui.Path{}).MoveTo(cx, cy-r).
		CubeTo(cx+r*k, cy-r, cx+r, cy-r*k, cx+r, cy).
		CubeTo(cx+r, cy+r*k, cx+r*k, cy+r, cx, cy+r).
		CubeTo(cx-r*k, cy+r, cx-r, cy+r*k, cx-r, cy).
		CubeTo(cx-r, cy-r*k, cx-r*k, cy-r, cx, cy-r).
		Close()
}
