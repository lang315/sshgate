package view

import "github.com/egoist/mygo/ui"

var (
	cardFolderIcon = cardIcon("M3 6h6l2 2h10v11H3z")
	editIcon       = cardIcon("M4 20h4L19 9l-4-4L4 16z")
	trashIcon      = cardIcon("M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13")
	tunnelIcon     = cardIcon("M4 8h13l-3-3M20 16H7l3 3") // two arrows, one each way

	// The rail's icons paint in currentColor: the rail's theme (railTheme)
	// makes a SidebarItem's accent RailText.
	hostsIcon  = cardIcon("M4 5h16v6H4zM4 13h16v6H4zM8 8h.01M8 16h.01")
	keyIcon    = cardIcon("M15 3a6 6 0 1 0 0 12 6 6 0 0 0 0-12zM11 13l-8 8M6 18l3 3M15 8h.01")
	auditIcon  = cardIcon("M6 3h9l4 4v14H6zM14 3v5h5M9 13h7M9 17h7")
	shieldIcon = cardIcon("M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z")
	gearIcon   = cardIcon("M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6zM10.5 3h3l.5 2.2 1.8 1 2.1-.8 1.5 2.6-1.7 1.5v2l1.7 1.5-1.5 2.6-2.1-.8-1.8 1-.5 2.2h-3l-.5-2.2-1.8-1-2.1.8-1.5-2.6 1.7-1.5v-2L4.6 8l1.5-2.6 2.1.8 1.8-1z")
	moonIcon   = cardIcon("M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z")
	sunIcon    = cardIcon("M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5L19 19M5 19l1.5-1.5M17.5 6.5L19 5")

	xIcon           = cardIcon("M6 6l12 12M18 6L6 18")
	chevronIcon     = cardIcon("M9 6l6 6-6 6")
	chevronDownIcon = cardIcon("M6 9l6 6 6-6")
)

// cardIcon draws one of Electron's stroked 24x24 icons (desktop/src/renderer/icons.tsx).
func cardIcon(paths string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" d="` + paths + `"/></svg>`))
}

// iconButton is a card action: an icon named "<verb> <name>", with the verb as its tooltip.
// cardButtonPadX keeps a card's five actions (Stop included) inside its 240 DIP.
const cardButtonPadX = 10

func iconButton(c *ui.Context, verb, name string, icon *ui.SVG, col ui.Color) bool {
	return ui.Button(c, "").Label(verb+" "+name).Tooltip(verb).Padding(6, cardButtonPadX).Children(func() { ui.Icon(c, icon).TextColor(col) }).Clicked()
}
