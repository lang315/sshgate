package view

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// importState is the open import sheet (Electron's ImportSheet.tsx). scan
// is nil until the first import.scan answers.
type importState struct {
	seq     int
	scan    *hubapi.ImportScan
	checked map[string]bool // aliases; only ready ones are ever set
	busy    bool
	err     string
}

// openImport shows the import sheet in place of the editor and scans.
func (a *App) openImport() {
	a.sheetSeq++
	a.editor = nil
	a.imp = &importState{seq: a.sheetSeq}
	a.scanImport(false)
}

// scanImport runs import.scan; a rescan keeps the person's checks on rows
// still ready. A reply is dropped if its hub stopped, the ready screen went
// (a lock), or its sheet closed.
func (a *App) scanImport(rescan bool) {
	imp, gen := a.imp, a.hubGen
	imp.busy = true
	go func() {
		s, err := a.hub().ImportScan(context.Background())
		a.Update(func() {
			if gen != a.hubGen || a.screen != model.ScreenReady || a.imp != imp {
				return
			}
			imp.busy = false
			if err != nil {
				imp.err = err.Error()
				return
			}
			var prev map[string]bool
			if rescan {
				prev = imp.checked
			}
			imp.scan, imp.checked = &s, model.NextChecked(s.Candidates, prev)
		})
	}()
}

// importAliases are the checked rows that are ready, in the scan's order:
// alias names are all import.apply gets.
func (imp *importState) importAliases() []string {
	var out []string
	if imp.scan == nil {
		return out
	}
	for _, c := range imp.scan.Candidates {
		if c.Status == "ready" && imp.checked[c.Alias] {
			out = append(out, c.Alias)
		}
	}
	return out
}

// applyImport sends import.apply. The reply is dropped if its hub stopped
// or the ready screen went (a lock); it touches this sheet only, never an
// editor or sheet opened since.
func (a *App) applyImport() {
	imp, gen := a.imp, a.hubGen
	names := imp.importAliases()
	if imp.busy || len(names) == 0 {
		return
	}
	imp.busy, imp.err = true, ""
	go func() {
		r, err := a.hub().ImportApply(context.Background(), names)
		a.Update(func() {
			if gen != a.hubGen || a.screen != model.ScreenReady {
				return
			}
			imp.busy = false
			if err != nil {
				imp.err = err.Error()
				return
			}
			a.reloadServers()
			if a.imp != imp {
				return
			}
			if len(r.Skipped) == 0 {
				a.closeSheet()
				return
			}
			parts := make([]string, len(r.Skipped))
			for i, s := range r.Skipped {
				parts[i] = fmt.Sprintf("%s (%s)", s.Alias, s.Reason)
			}
			imp.err = "Not imported: " + strings.Join(parts, "; ")
			a.scanImport(true)
		})
	}()
}

func (a *App) buildImport(c *ui.Context) {
	t := c.Theme()
	imp := a.imp
	n := len(imp.importAliases())
	closed, submit := false, false
	sheet := ui.Column(c.Key("import-"+strconv.Itoa(imp.seq))).Fill().Width(420).Background(t.Surface).BorderWidth(0, 0, 0, 1).BorderColor(t.Border).
		Role(ui.RoleDialog).Label("Import from SSH config")
	sheet.Children(func() {
		ui.Row(c).Gap(8).Padding(12, 16).Children(func() {
			ui.Text(c, "Import from SSH config").Bold().FontSize(16).Grow(1)
			if ui.Button(c, "×").Label("Close import").Tooltip("Close").Clicked() {
				closed = true
			}
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(12).Padding(8, 16).Children(func() {
				ui.RichText(c,
					ui.Span{Text: "Host keys come from your known_hosts. With "},
					ui.Span{Text: "StrictHostKeyChecking accept-new", Font: "monospace"},
					ui.Span{Text: ", OpenSSH accepted them without asking you; compare them with the server if unsure."}).TextColor(t.TextMuted)
				switch {
				case imp.scan == nil && imp.busy:
					ui.Text(c, "Reading your SSH config…").TextColor(t.TextMuted)
				case imp.scan != nil:
					if imp.scan.Note != "" {
						ui.Text(c, model.DisplayText(imp.scan.Note)).TextColor(t.TextMuted)
					}
					for _, cand := range imp.scan.Candidates {
						a.buildImportRow(c, cand)
					}
				}
			})
		})
		ui.Row(c).Gap(8).Padding(12, 16).Justify(ui.End).Children(func() {
			if imp.err != "" {
				ui.Text(c, model.DisplayText(imp.err)).TextColor(t.Danger).Grow(1)
			}
			// Keyed: the error above comes and goes, and would re-mount (and
			// re-focus) them.
			if ui.Button(c.Key("close"), "Close").AutoFocus().Clicked() {
				closed = true
			}
			label := fmt.Sprintf("Import %d hosts", n)
			if n == 1 {
				label = "Import 1 host"
			}
			if ui.PrimaryButton(c.Key("import"), label).Disabled(imp.busy || n == 0).Clicked() {
				submit = true
			}
		})
	})
	if sheet.Shortcut(0, ui.KeyEscape) && !imp.busy {
		closed = true
	}
	switch {
	case closed:
		a.closeSheet()
	case submit:
		a.applyImport()
	}
}

// buildImportRow is one alias; only a ready one has a checkbox. Every text
// here comes from ~/.ssh/config and known_hosts, so it is display text.
func (a *App) buildImportRow(c *ui.Context, cand hubapi.Candidate) {
	t := c.Theme()
	imp := a.imp
	alias := model.DisplayText(cand.Alias)
	ui.Column(c.Key("cand-"+cand.Alias)).Gap(4).Padding(8, 0).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			if cand.Status == "ready" {
				on := imp.checked[cand.Alias]
				if ui.Checkbox(c, &on, alias).Label("Import " + alias).Disabled(imp.busy).Changed() {
					imp.checked[cand.Alias] = on
				}
			} else {
				ui.Text(c, alias).Bold()
			}
			status, col := map[string]string{"ready": "Ready", "exists": "Already in vault"}[cand.Status], t.Warning
			if status == "" {
				status = "Skipped"
			}
			if cand.Status == "ready" {
				col = t.Success
			}
			ui.Text(c, status).TextColor(col)
		})
		if cand.Status == "skipped" {
			ui.Text(c, model.DisplayText(cand.Reason)).TextColor(t.TextMuted)
		} else {
			key := "agent"
			if cand.Auth == "key" {
				key = cand.KeyPath
			}
			ui.Text(c, model.DisplayText(fmt.Sprintf("%s@%s:%d · %s", cand.User, cand.Host, cand.Port, key))).Font("monospace").TextColor(t.TextMuted)
		}
		if cand.Status != "ready" {
			return
		}
		pin := "not in known_hosts"
		if cand.HostKey != "" {
			pin = cand.HostKeyAlgo + " " + cand.HostKey
		}
		ui.Text(c, model.DisplayText(pin)).Font("monospace").Selectable()
		if cand.NeedsPassphrase {
			ui.Text(c, "Key has a passphrase: add it in the editor after import.").TextColor(t.TextMuted)
		}
	})
}
