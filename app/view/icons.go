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
)
