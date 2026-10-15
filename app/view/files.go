package view

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// filesTab is one Files tab (Electron's FilesView.tsx). shown is the
// folder listed, which every action targets; requested the one the newest
// load asked for; pathIn the Path field. loadGen drops a superseded load's
// reply; relist is a job folder to list again when the load in flight ends.
type filesTab struct {
	server                   string
	shown, requested, pathIn string
	listing                  *hubapi.FileListing
	rows                     []hubapi.FileEntry // the listing as shown: hidden names filtered, sorted
	err                      string
	loading                  bool
	loadGen                  int
	relist                   string
	ls                       ui.ListState
	sel                      int
	selection                ui.Selection[string]
	sort                     ui.SortOrder
	name                     *nameDialog
}

// nameDialog is New folder or Rename. folder is the folder shown when it
// opened: the request goes there, wherever the tab moved since.
type nameDialog struct {
	rename                   bool
	from, folder, value, err string
	busy                     bool
}

var (
	folderIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="currentColor" d="M3 6h7l2 2h9v11H3z"/></svg>`))
	fileIcon   = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="currentColor" d="M6 3h9l4 4v14H6z"/></svg>`))
	linkIcon   = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="currentColor" d="M10 14l-2 2a3 3 0 0 1-4-4l3-3a3 3 0 0 1 4 0l-1 1a1.5 1.5 0 0 0-2 0l-3 3a1.5 1.5 0 0 0 2 2l2-2zm4-4l2-2a3 3 0 0 1 4 4l-3 3a3 3 0 0 1-4 0l1-1a1.5 1.5 0 0 0 2 0l3-3a1.5 1.5 0 0 0-2-2l-2 2z"/></svg>`))
)

var fileColumns = []ui.TableColumn{
	{Title: "Name", Sortable: true},
	{Title: "Size", Width: 90, Align: ui.End, Sortable: true},
	{Title: "Modified", Width: 140, Sortable: true},
	{Title: "Permissions", Width: 110, Sortable: true},
}

// sortKey maps a column to SortEntries' key.
var sortKey = map[string]string{"Name": "name", "Size": "size", "Modified": "mtime", "Permissions": "mode"}

// openFiles adds a Files tab for server, which lists the folder last shown
// for it in this run, else the login's home.
func (a *App) openFiles(server string) string {
	id := a.tabs.OpenFiles(server)
	a.files[id] = &filesTab{server: server, sel: -1, sort: ui.SortOrder{Column: "Name"}}
	last := a.lastFolder[server]
	a.loadFiles(id, last, nil, last != "")
	return id
}

// loadFiles lists p ("" is home) in tab id. trust is set on the retry after
// a host-key prompt; homeOnFail lists home when p fails (the remembered
// folder may be gone).
func (a *App) loadFiles(id, p string, trust *hubapi.Trust, homeOnFail bool) {
	v := a.files[id]
	v.loadGen++
	gen, lg, server := a.hubGen, v.loadGen, v.server
	v.requested, v.loading, v.err, v.pathIn = p, true, "", p
	a.prompts.Drop(id) // a newer load supersedes a prompt still waiting
	go func() {
		l, err := a.hub().FilesList(context.Background(), server, p, trust)
		a.Update(func() { a.listed(id, gen, lg, l, err, trust != nil, homeOnFail) })
	}()
}

// listed applies a files.list reply, unless its hub stopped, its tab
// closed, or a later load superseded it. A reply landing behind the lock
// screen still applies: the tab shows it after the unlock.
func (a *App) listed(id string, gen, lg int, l hubapi.FileListing, err error, trusted, homeOnFail bool) {
	v := a.files[id]
	if v == nil || gen != a.hubGen || lg != v.loadGen {
		return
	}
	if err == nil && l.Status == "hostKeyUnknown" {
		a.prompts.Ask(id, l.HostKey) // still loading until the person answers
		return
	}
	if err == nil && l.Status == "hostKeyMismatch" {
		k := l.HostKey
		a.mismatch = &k
		err = errors.New("host key mismatch")
	}
	v.loading = false
	if err != nil {
		if homeOnFail {
			a.loadFiles(id, "", nil, false)
			return
		}
		v.listing, v.err = nil, err.Error()
	} else {
		if v.shown == l.Path { // a relist: the selection stays, less what went
			keep := model.PruneSelection(slices.Collect(v.selection.All()), l.Entries)
			v.selection.Clear()
			for _, n := range keep {
				v.selection.Add(n)
			}
		} else {
			v.selection.Clear()
			v.sel = -1
		}
		v.listing, v.shown, v.requested, v.pathIn = &l, l.Path, l.Path, l.Path
		a.lastFolder[v.server] = l.Path
		a.sortFiles(v)
		if trusted {
			a.reloadServers() // the card's New key goes
		}
	}
	if f := v.relist; f != "" {
		v.relist = ""
		if f == v.shown && f == v.requested && a.ready(gen) {
			a.loadFiles(id, f, nil, false)
		}
	}
}

func (a *App) sortFiles(v *filesTab) {
	if v.listing == nil {
		v.rows = nil
		return
	}
	v.rows = model.SortEntries(model.VisibleEntries(v.listing.Entries, a.showHidden), sortKey[v.sort.Column], v.sort.Descending)
}

// selected is the rows chosen, in display order: a chosen name hidden by
// the filter does not count.
func (v *filesTab) selected() []hubapi.FileEntry {
	var out []hubapi.FileEntry
	for _, r := range v.rows {
		if v.selection.Has(r.Name) {
			out = append(out, r)
		}
	}
	return out
}

func (a *App) filesUp(id string) {
	v := a.files[id]
	from := v.pathIn
	if v.listing != nil {
		from = v.shown
	}
	if p, ok := model.ParentPath(from); ok {
		a.loadFiles(id, p, nil, false)
	}
}

// openEntry lists a folder or a link (a link to a file fails in the hub).
func (a *App) openEntry(id string, x hubapi.FileEntry) {
	if x.Kind == "dir" || x.Kind == "link" {
		a.loadFiles(id, model.JoinPath(a.files[id].shown, x.Name), nil, false)
	}
}

// endFiles is a hub stop for the Files tabs: a load in flight will get no
// reply, and a name dialog's request none either.
func (a *App) endFiles() {
	for _, v := range a.files {
		if v.loading {
			v.loading, v.relist = false, ""
			v.loadGen++
			if v.listing == nil {
				v.err = "hub restarted"
			}
		}
		v.name = nil
	}
}

// buildFiles draws a Files tab: the toolbar, notes, the file table and its
// name dialog. Actions run after the build, as the tab strip's do.
func (a *App) buildFiles(c *ui.Context, t model.Tab) {
	id := t.ID
	v := a.files[id]
	th := c.Theme()
	ready := model.ActionsReady(v.listing, v.loading)
	sel := v.selected()
	var act func()
	ui.Column(c).Fill().Gap(8).Padding(8).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			if ui.Button(c, "Up").Tooltip("Up").Clicked() {
				act = func() { a.filesUp(id) }
			}
			if ui.TextInput(c.Key("path-"+id), &v.pathIn).Label("Path").Font("monospace").Grow(1).Submitted() {
				act = func() { a.loadFiles(id, strings.TrimSpace(v.pathIn), nil, false) }
			}
			if ui.Button(c, "Refresh").Tooltip("Refresh").Clicked() {
				act = func() { a.loadFiles(id, v.shown, nil, false) }
			}
			if ui.Checkbox(c.Key("hidden-"+id), &a.showHidden, "Show hidden files").Changed() {
				act = func() {
					for _, f := range a.files {
						a.sortFiles(f)
					}
				}
			}
		})
		ui.Row(c).Gap(6).Children(func() {
			if ui.Button(c, "New folder").Disabled(!ready).Clicked() {
				act = func() { v.name = &nameDialog{folder: v.shown} }
			}
			if ui.Button(c, "Rename").Disabled(!ready || len(sel) != 1).Clicked() {
				act = func() { v.name = &nameDialog{rename: true, from: sel[0].Name, value: sel[0].Name, folder: v.shown} }
			}
		})
		if v.err != "" {
			ui.Text(c, model.DisplayText(v.err)).TextColor(th.Danger)
		}
		if v.listing != nil && v.listing.Truncated {
			ui.Text(c, "Showing the first 10,000 entries.").TextColor(th.TextMuted)
		}
		if v.listing != nil && v.listing.Bad > 0 {
			ui.Text(c, model.BadNote(v.listing.Bad)).TextColor(th.TextMuted)
		}
		ui.Column(c).Grow(1).Children(func() {
			rows := v.rows
			v.ls.Selected, v.ls.Selection, v.ls.Sort = &v.sel, &v.selection, &v.sort
			v.ls.Key = func(i int) any { return rows[i].Name }
			v.ls.Label = func(i int) string { return model.DisplayText(rows[i].Name) }
			tb := ui.Table(c.Key("files-"+id), &v.ls, fileColumns, len(rows), func(row, col int) { buildFileCell(c, rows[row], col) }).
				Label("Files on " + model.DisplayText(v.server)).Grow(1)
			if r, ok := tb.Activated(); ok && r < len(rows) {
				x := rows[r]
				act = func() { a.openEntry(id, x) }
			}
			if tb.SortChanged() {
				act = func() { a.sortFiles(v) }
			}
			if tb.Shortcut(0, ui.KeyBackspace) {
				act = func() { a.filesUp(id) }
			}
			if tb.Shortcut(0, ui.KeyF2) && ready && len(sel) == 1 {
				act = func() { v.name = &nameDialog{rename: true, from: sel[0].Name, value: sel[0].Name, folder: v.shown} }
			}
			if v.listing != nil && len(rows) == 0 {
				ui.Text(c, "Empty folder.").TextColor(th.TextMuted)
			}
		})
	})
	if v.name != nil {
		a.buildNameDialog(c, id, v)
	}
	if act != nil {
		act()
	}
}

func buildFileCell(c *ui.Context, x hubapi.FileEntry, col int) {
	th := c.Theme()
	switch col {
	case 0:
		ui.Row(c).Gap(6).Children(func() {
			switch x.Kind {
			case "dir":
				ui.Icon(c, folderIcon)
			case "link":
				ui.Icon(c, linkIcon)
			default:
				ui.Icon(c, fileIcon)
			}
			ui.Text(c, model.DisplayText(x.Name)).Font("monospace").SingleLine()
			if x.Target != "" {
				ui.Text(c, "→ "+model.DisplayText(x.Target)).Font("monospace").TextColor(th.TextMuted).SingleLine()
			}
		})
	case 1:
		if x.Kind == "dir" {
			ui.Text(c, "—")
		} else {
			ui.Text(c, model.FormatSize(x.Size))
		}
	case 2:
		ui.Text(c, model.FormatTime(x.Mtime, time.Local))
	case 3:
		ui.Text(c, model.FormatMode(x.Kind, x.Mode)).Font("monospace")
	}
}

// buildNameDialog is New folder or Rename (Electron's NameDialog): Enter
// submits, Esc cancels, an error keeps it open with the hub's text.
func (a *App) buildNameDialog(c *ui.Context, id string, v *filesTab) {
	th := c.Theme()
	nd := v.name
	title, button := "New folder", "Create"
	if nd.rename {
		title, button = "Rename", "Rename"
	}
	submit, cancel := false, false
	open := true
	m := ui.Modal(c.Key(fmt.Sprintf("name-%p", nd)), &open, func() {
		ui.Column(c).Width(400).Gap(10).Padding(20).Children(func() {
			ui.Text(c, title).Bold().FontSize(16)
			var in ui.Element
			ui.Field(c, "Name", func() { in = ui.TextInput(c.Key("name"), &nd.value).AutoFocus() })
			if in.Submitted() {
				submit = true
			}
			if nd.err != "" {
				ui.Text(c, model.DisplayText(nd.err)).TextColor(th.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					cancel = true
				}
				if ui.Button(c, button).Disabled(nd.busy).Clicked() {
					submit = true
				}
			})
		})
	}).Label(title)
	if m.Dismissed() {
		cancel = true
	}
	switch {
	case cancel && !nd.busy:
		v.name = nil
		v.ls.Handle.Focus()
	case submit:
		a.submitName(id)
	}
}

// submitName sends files.mkdir or files.rename into the dialog's folder.
// On success it closes, and lists again only if that folder is still
// shown; on error it stays open with the message.
func (a *App) submitName(id string) {
	v := a.files[id]
	nd := v.name
	if nd.busy {
		return
	}
	if e := model.NameError(nd.value); e != "" {
		nd.err = e
		return
	}
	nd.busy, nd.err = true, ""
	gen, server, name := a.hubGen, v.server, nd.value
	go func() {
		var err error
		if nd.rename {
			err = a.hub().FilesRename(context.Background(), server, model.JoinPath(nd.folder, nd.from), model.JoinPath(nd.folder, name))
		} else {
			err = a.hub().FilesMkdir(context.Background(), server, model.JoinPath(nd.folder, name))
		}
		a.Update(func() {
			if gen != a.hubGen || a.files[id] != v || v.name != nd {
				return
			}
			nd.busy = false
			if err != nil {
				nd.err = err.Error()
				return
			}
			v.name = nil
			v.ls.Handle.Focus()
			if nd.folder == v.shown && a.ready(gen) {
				a.loadFiles(id, v.shown, nil, false)
			}
		})
	}()
}
