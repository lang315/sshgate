package view

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/model"
)

const railWidth = 200

// sidebarShown: the sidebar picks between Hosts and Audit, so a terminal, a
// file browser or a tunnel tab, which want the width, hide it.
// A sheet beside the AI column leaves no room for it (200 + 420 + 380 > 900),
// and the column must neither clip nor move, so the sidebar steps aside, at once.
func (a *App) sidebarShown() bool {
	return (a.tabs.Active == "" || a.tabs.Active == model.AuditTab || a.tabs.Active == model.KeychainTab) && a.editor == nil && a.imp == nil
}

// buildTopBar is the full-width bar: Home, the tab strip, AI and Lock. It is
// the same whatever the sidebar does, so the tabs never move.
func (a *App) buildTopBar(c *ui.Context, autoN, liveAuto int) {
	p := tok(c)
	ui.Row(c).Gap(8).Padding(8).AlignItems(ui.Center).Background(p.Rail).Children(func() {
		if railButton(ui.Button(c, "").Label("Home").Width(railWidth).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, shieldIcon)
				ui.Text(c, "sshgate").Bold()
			})
		}), p).Clicked() {
			a.tabs.ShowHome()
		}
		a.buildTabStrip(c) // grows, so AI and Lock stay at the right
		if a.aiOpen || len(a.items) > 0 || autoN > 0 {
			label := fmt.Sprintf("AI %d", len(a.items))
			if autoN > 0 {
				label += fmt.Sprintf(" · auto %d", autoN)
			}
			waiting := model.AIButtonWaiting(a.aiOpen, len(a.items))
			b := ui.Button(c, "").Label("AI requests").Children(func() { ui.Text(c, label) })
			if waiting {
				b.TextColor(p.OnWaitFill).Background(p.WaitFill) // a lit button, not a pill
				if b.Hovered() {
					b.Background(waitFillHover(p))
				}
			} else {
				railButton(b, p)
			}
			if b.Clicked() {
				a.aiOpen = !a.aiOpen
			}
		}
		lock := railButton(ui.Button(c, "Lock"), p)
		if tip := model.LockTitle(liveAuto); tip != "" {
			lock.Tooltip(tip)
		}
		if lock.Clicked() {
			a.lock()
		}
	})
}

// buildSidebar is the left rail: Hosts, Audit and Keychain, then the theme switch and
// Settings. It draws nothing while a tab is active, and the page snaps.
func (a *App) buildSidebar(c *ui.Context) {
	if !a.sidebarShown() {
		return
	}
	p := tok(c)
	sel := "hosts"
	switch a.tabs.Active {
	case model.AuditTab:
		sel = "audit"
	case model.KeychainTab:
		sel = "keychain"
	}
	ui.Themed(c, railTheme(c)).Width(railWidth).Shrink(0).HeightPercent(100).Background(p.Rail).Children(func() {
		sb := ui.Sidebar(c, &sel, func() {
			for _, it := range []struct {
				id, label string
				icon      *ui.SVG
			}{{"hosts", "Hosts", hostsIcon}, {"audit", "Audit", auditIcon}, {"keychain", "Keychain", keyIcon}} {
				item := ui.SidebarItem(c, it.id, it.icon, it.label).TextColor(p.RailText)
				if sel == it.id {
					item.Background(p.RailText.Alpha(railPillAlpha)) // the pill, per item
				}
			}
		}).Grow(1).Background(p.Rail)
		if sb.Changed() {
			switch sel {
			case "audit":
				a.tabs.ShowAudit()
			case "keychain":
				a.tabs.ShowKeychain()
				a.keysLoad() // on entering the view, by the click
			default:
				a.tabs.ShowHome()
			}
		}
		ui.Row(c).Gap(8).Padding(8).AlignItems(ui.Center).Children(func() {
			a.buildThemeControl(c)
			if ui.Button(c, "").Label("Settings").Tooltip("Settings").TextColor(p.RailText).Background(p.RailText.Alpha(railPillAlpha)).Children(func() { ui.Icon(c, gearIcon).TextColor(p.RailText) }).Disabled(a.status == nil || a.status.SoftLockLimit == "").Clicked() {
				a.openSettings()
			}
		})
	})
}

// buildAIColumn snaps into place; only its content fades in, over 120 ms.
func (a *App) buildAIColumn(c *ui.Context) {
	col := ui.Column(c.Key("ai")).Fill().Width(380).Shrink(0) // never narrows: its buttons stay whole
	target := float32(1)
	if !a.aiFaded {
		a.aiFaded = true
		if !c.Preferences().ReduceMotion { // Animate would otherwise show one transparent frame
			target = 0
			c.Invalidate() // one more frame, so the fade has a start and an end
		}
	}
	col.Opacity(col.Animate("fade", target, 120*time.Millisecond))
	raisedShadow(c).On(col)
	col.Children(func() { a.buildColumn(c) })
}
