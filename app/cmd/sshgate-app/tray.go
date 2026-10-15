package main

import (
	"encoding/base64"
	"log"

	"github.com/egoist/mygo"
	"github.com/lang315/sshgate/app/model"
)

// trayIcon is the menu-bar shield, a template image (black on transparent),
// from desktop/src/main/attention.ts TRAY_TEMPLATE_PNG_BASE64['2'] (32 px;
// macOS scales a tray image to 18 pt).
const trayIconBase64 = "iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAC4UlEQVR42rWXv2sUQRTHP7t3FxWj+ANtRBCtRERERIhVOFEET2wUxULERm0UrPQPEJIiYCWCFmprF1GCioWcjTGp1C4IEcxZiEWimLvdtflOeAyzl709b2DYZebN+37nvbcz363QW4uAip6ZNxdrLuvFYaUgqHOeCiAD9gK3gBPAd6CleYAqfTS3y2qA4HrgHPAC6BgyCTAFXACGA5usmsjltljdb2uAA8BdYM6AZkBb3Y59BcaAg1rbFSfEaAuwDzgMjMjRbjOfGEdufWbCb6M2B8wC74EPwCfgZ6gOIqABvFUus0BvCzxbpSeBqLjeEkbDpJqaiIwZw1ROOgVBu5HpyJct4DFh1my1LmlBKlJ2Ls2pj9Wav6atsaWQQZxTqQ48o//mwh7nMczbxYw5fP4HEYoQSNWfAkeAm4MgEXcBj4BvwGUV0j3guklHNkgCDmQnMAmsVe7uA5fMfDrIFDiQk8BzHcMx8AQ469kMhICbbwN14CWwQePPgDPA737TERcgU1MNjABNYJfGJoHbWpeUxbUhbBVYuBlY533XZVrL3CErl8dRc3z6d0Cmi2SHcXJaJ1rHHNt+TwJHcyasFWy3i2FgwTN0z2lguwE/X+KOcPYLRjNEVQ1WgEXgFXDRfF4R8E67/aWxK8BDc1C1gC+eTHPv24D95pONhLEozMSXZqMy7mjBD0/h3DC7Wdb7oy65bhh/LgKjeXLQCYxpGS6LxGPdjHc8cq42HnQhcFw2f00qozxF5EJSB14LyF3JH4FDJoyRuSU/K6yhFOwBTolsDTgGvPHDH1LJEyYKiclhGVHiUjVRRIlbRdwMkOgmwUL9j2yaRZWxrYWtHomyO2/KV9SLqnKGG6X33U6LilJnNyUflJF0sXmOeydjqB5STw2Pez5KaziXs7o0vi/TfRk+K1t/fV/NVe6QDqP5QATmNTfUw38nZX9kNwFX9bczA1zTGIMCtympBsYwf8U9hfwf9KFWxMb2a20AAAAASUVORK5CYII="

// trayShell shows the pending count on the tray and a notification per AI
// request; it runs on the main thread (App calls it from Update).
type trayShell struct {
	tray *mygo.Tray
	win  *mygo.Window
}

func newTrayShell(win *mygo.Window) *trayShell {
	png, err := base64.StdEncoding.DecodeString(trayIconBase64)
	if err != nil {
		log.Fatal("tray icon: ", err)
	}
	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon: png, IconIsTemplate: true, ToolTip: model.TrayTooltip(0),
		Menu: mygo.NewMenu([]*mygo.MenuItem{
			{Label: "Show", Click: func(*mygo.MenuItem, *mygo.Window) { showWindow(win) }},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}),
	})
	if err != nil {
		log.Print("tray: ", err) // e.g. Linux without libayatana-appindicator3; the app works without it
	}
	return &trayShell{tray: tray, win: win}
}

func showWindow(w *mygo.Window) {
	w.Show()
	w.Focus()
	mygo.App.Focus()
}

func (s *trayShell) SetPending(n int) {
	if s.tray == nil {
		return
	}
	s.tray.SetTitle(model.TrayTitle(n))
	s.tray.SetToolTip(model.TrayTooltip(n))
}

// Notify skips a focused window and an unpackaged macOS app (no bundle, no
// notifications); Show waits for the permission prompt, so it runs off the
// main thread.
func (s *trayShell) Notify(id, title, body string) {
	if s.win.IsFocused() || !mygo.NotificationsSupported() {
		return
	}
	n := mygo.NewNotification(mygo.NotificationOptions{ID: "req-" + id, Title: title, Body: body})
	n.OnClick(func() { showWindow(s.win) })
	go func() {
		if err := n.Show(); err != nil {
			log.Print("notification: ", err)
		}
	}()
}

// SetTheme applies the theme control's choice app-wide, native chrome
// included; Auto follows the system.
func (s *trayShell) SetTheme(pref string) { mygo.Theme.SetSource(themeSource(pref)) }

func themeSource(pref string) mygo.ThemeSource {
	switch pref {
	case "dark":
		return mygo.ThemeDark
	case "light":
		return mygo.ThemeLight
	}
	return mygo.ThemeSystem
}
