package view

import "github.com/egoist/mygo/ui"

// section is a heading above a raised card holding the section's fields.
func section(c *ui.Context, title string, fn func()) {
	ui.Column(c.Key("section-" + title)).Gap(10).Children(func() {
		ui.Text(c, title).Bold().FontSize(12).TextColor(tok(c).Label)
		ui.Column(c).Gap(8).Padding(12).Radius(radiusCard).Background(tok(c).Raised).Children(fn)
	})
}
