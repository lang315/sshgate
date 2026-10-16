package view

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
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
		a.col.mounted, a.pausedCol = false, nil
	}
	t := c.Theme()
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
		ui.Row(c).Gap(8).Padding(8).Children(func() {
			if ui.Button(c, "Hosts").Selected(a.tabs.Active == "").Clicked() {
				a.tabs.ShowHome()
			}
			a.buildTabStrip(c) // grows, so AI and Lock stay at the right
			if a.aiOpen || len(a.items) > 0 || autoN > 0 {
				label := fmt.Sprintf("AI %d", len(a.items))
				if autoN > 0 {
					label += fmt.Sprintf(" · auto %d", autoN)
				}
				b := ui.Button(c, "").Label("AI requests").Children(func() {
					txt := ui.Text(c, label)
					if model.AIButtonWaiting(a.aiOpen, len(a.items)) {
						txt.TextColor(t.Warning)
					}
				})
				if b.Clicked() {
					a.aiOpen = !a.aiOpen
				}
			}
			a.buildThemeControl(c)
			if ui.Button(c, "\u2699").Label("Settings").Tooltip("Settings").Disabled(a.status == nil || a.status.SoftLockLimit == "").Clicked() {
				a.settings = &settingsState{limit: a.status.SoftLockLimit, chosen: a.status.SoftLockLimit}
			}
			lock := ui.Button(c, "Lock")
			if tip := model.LockTitle(liveAuto); tip != "" {
				lock.Tooltip(tip)
			}
			if lock.Clicked() {
				a.lock()
			}
		})
		ui.Row(c).Grow(1).Children(func() {
			// HeightPercent bounds the column so the host list's Scroll scrolls.
			// Banners sit here, beside the AI column (Electron's main.work), so
			// one appearing never moves Allow.
			ui.Column(c).Grow(1).HeightPercent(100).Children(func() {
				if a.status != nil && a.status.StoreError != "" {
					ui.Row(c).Padding(8, 12).Background(t.Danger.Alpha(0.15)).Role(ui.RoleStatus).Children(func() {
						ui.Text(c, capFirst(model.DisplayText(a.status.StoreError)))
					})
				}
				if text := model.RanLockedText(a.ranLocked); text != "" {
					ui.Row(c).Gap(8).Padding(8, 12).Background(t.Warning.Alpha(0.15)).Role(ui.RoleStatus).Children(func() {
						ui.Text(c, model.DisplayText(text)).Grow(1)
						if ui.Button(c, "Dismiss").Clicked() {
							a.ranLocked = nil
						}
					})
				}
				if a.alert != "" {
					ui.Row(c).Gap(8).Padding(8, 12).Background(t.Warning.Alpha(0.15)).Children(func() {
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
				a.buildHosts(c)
			})
			switch {
			case a.editor != nil:
				a.buildEditor(c)
			case a.imp != nil:
				a.buildImport(c)
			}
			if a.aiOpen {
				ui.Column(c).Fill().Width(380).Children(func() { a.buildColumn(c) })
			}
		})
	})
	a.buildHostKeyDialogs(c)
	a.buildDeleteConfirm(c)
	a.buildSettings(c)
}

func (a *App) buildHosts(c *ui.Context) {
	t := c.Theme()
	now := a.Now()
	shown := model.FilterHosts(a.servers, a.query)
	ui.Scroll(c).Grow(1).Padding(16).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.TextInput(c.Key("search"), &a.query).Placeholder("Search hosts").Label("Search hosts").Grow(1).Bind(&a.searchH)
			if ui.Button(c, "New host").Clicked() {
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
		ui.Row(c).Wrap().Gap(12).Children(func() {
			for _, s := range shown {
				name := s.Name
				shownName := model.DisplayText(name)
				ui.Column(c.Key("host-"+name)).Width(240).Gap(8).Padding(12).Radius(10).Border(1, t.Border).Background(t.Surface).Children(func() {
					if ui.Button(c, "").Label("Open " + shownName).Children(func() {
						ui.Row(c).Gap(8).Children(func() {
							ui.Text(c, model.Tile(shownName)).Bold()
							ui.Column(c).Children(func() {
								ui.Text(c, shownName).Bold().SingleLine()
								ui.Text(c, model.DisplayText(model.Addr(s))).Font("monospace").TextColor(t.TextMuted).SingleLine()
							})
						})
					}).Clicked() {
						a.openTab(name)
					}
					ui.Row(c).Gap(6).Children(func() {
						ui.Row(c).Gap(6).Wrap().Grow(1).Children(func() {
							if s.AIVisible {
								ui.Text(c, "AI").TextColor(t.Accent)
							}
							if s.HostKey == "" {
								ui.Text(c, "New key").TextColor(t.Warning)
							}
							if chip := model.ChipLabel(s.AutoAllow, now); chip != "" {
								ui.Text(c, chip).TextColor(t.Warning)
							}
							if n := model.RunningTunnels(a.tunnels, name); n > 0 {
								ui.Text(c, fmt.Sprintf("%d ⇄", n)).TextColor(t.Accent).Tooltip("Running tunnels")
							}
						})
						// Always shown while a grant is on or paused: it must be stoppable at a glance.
						if model.OnOrPaused(s.AutoAllow, now) {
							if ui.Button(c, "\u25a0").Label("Stop auto-allow " + shownName).Tooltip("Stop auto-allow").Clicked() {
								a.setAuto([]string{name}, "off", "stop")
							}
						}
						if ui.Button(c, "Files").Label("Files " + shownName).Tooltip("Files").Clicked() {
							a.openFiles(name)
						}
						if ui.Button(c, "").Label("Tunnels " + shownName).Tooltip("Tunnels").Children(func() { ui.Icon(c, tunnelIcon) }).Clicked() {
							a.openTunnels(name)
						}
						if ui.Button(c, "Edit").Label("Edit " + shownName).Tooltip("Edit").Clicked() {
							a.openEditor(name, false)
						}
						if ui.Button(c, "Delete").Label("Delete " + shownName).Tooltip("Delete").Clicked() {
							a.askDelete(name)
						}
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
