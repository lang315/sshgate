package view

import "github.com/egoist/mygo/ui"

// cardIcon draws one of Electron's stroked 24x24 icons (desktop/src/renderer/icons.tsx).
func cardIcon(paths string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" d="` + paths + `"/></svg>`))
}

var (
	cardFolderIcon = cardIcon("M3 6h6l2 2h10v11H3z")
	editIcon       = cardIcon("M4 20h4L19 9l-4-4L4 16z")
	trashIcon      = cardIcon("M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13")
	tunnelIcon     = cardIcon("M4 8h13l-3-3M20 16H7l3 3") // two arrows, one each way
)

// iconButton is a card action: an icon named "<verb> <name>", with the verb as its tooltip.
// cardButtonPadX keeps a card's five actions (Stop included) inside its 240 DIP.
const cardButtonPadX = 10

func iconButton(c *ui.Context, verb, name string, icon *ui.SVG) bool {
	return ui.Button(c, "").Label(verb+" "+name).Tooltip(verb).Padding(6, cardButtonPadX).Children(func() { ui.Icon(c, icon) }).Clicked()
}
