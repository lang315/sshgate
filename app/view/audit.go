package view

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// typingDelay is how long after the last key in the search field the
// list is read again (Electron's 300 ms Debouncer).
const typingDelay = 300 * time.Millisecond

// auditState is the fixed Audit tab (desktop/src/renderer/AuditView.tsx).
// It calls audit.read only while it shows with nothing loaded, or on a
// filter change, Enter or 300 ms after typing in search, Refresh, or Load
// older. audit.appended records are merged in live; a notification never
// calls the hub.
type auditState struct {
	list    model.AuditList
	filters model.AuditFilters
	query   hubapi.AuditQuery // the query the shown list was read with
	gen     int               // bumped by every read and reset: replies to older ones are dropped
	older   bool              // a Load older call is in flight
	typedAt time.Time         // the search changed and was not read yet; zero when not
	host    string            // the host select's label
	hostNow string            // the label filters.Server was last set from
	open    map[int]*bool     // the rows expanded, by seq
	raw     map[int]*bool     // the rows showing Raw JSON, by seq
	ls      ui.ListState
	atTop   bool // the list showed its first row in the last frame
}

const allHosts = "All hosts"

// auditReset drops the list and every read in flight: the ready screen was
// entered or left. The tab reads again once it shows.
func (a *App) auditReset() {
	v := &a.audit
	v.gen++
	v.older, v.typedAt = false, time.Time{}
	v.list = model.AuditList{Status: "idle"}
}

// auditLocked follows the locked notification: nothing shown, and nothing
// read until the ready screen is entered again (auditReset).
func (a *App) auditLocked() {
	v := &a.audit
	v.gen++
	v.older, v.typedAt = false, time.Time{}
	v.open, v.raw = nil, nil
	v.list = model.ClearOnLock()
}

// auditAppended merges an audit.appended record into the list read with
// the same query; it calls nothing.
func (a *App) auditAppended(e hubapi.AuditEntry) {
	a.audit.list, _ = model.AppendLive(a.audit.list, model.RowOf(e), a.audit.query, a.audit.atTop)
}

// auditLoad reads the newest page with the filters shown, from the top.
func (a *App) auditLoad() {
	v := &a.audit
	v.gen++
	v.older, v.typedAt = false, time.Time{}
	v.query = model.AuditQueryFor(v.filters)
	v.list = model.StartLoad(v.list)
	v.ls.ScrollTo(0, ui.Start)
	v.atTop = true
	a.auditRead(v.query, false)
}

// auditLoadOlder reads the page below the last row shown.
func (a *App) auditLoadOlder() {
	v := &a.audit
	if v.older || v.list.Status != "loaded" || v.list.Next == 0 {
		return
	}
	v.gen++
	v.older = true
	q := v.query
	q.Before = v.list.Next
	a.auditRead(q, true)
}

func (a *App) auditRead(q hubapi.AuditQuery, older bool) {
	v := &a.audit
	gen, hubGen := v.gen, a.hubGen
	go func() {
		p, err := a.hub().AuditRead(context.Background(), q)
		a.Update(func() {
			if gen != v.gen || hubGen != a.hubGen {
				return
			}
			if older {
				v.older = false
			}
			if err != nil {
				v.list = model.FailLoad(v.list, err.Error())
				return
			}
			v.list = model.ApplyPage(v.list, p)
		})
	}()
}

// auditChange applies a filter change and reads with it at once.
func (a *App) auditChange(f model.AuditFilters) {
	a.audit.filters = f
	a.auditLoad()
}

// buildAudit draws the Audit tab: the toolbar with the "N new" pill, the
// list and its footer.
func (a *App) buildAudit(c *ui.Context) {
	v := &a.audit
	t := c.Theme()
	now := a.Now()
	if v.list.Status == "idle" {
		a.auditLoad() // shown with the vault unlocked and nothing loaded
	}
	if !v.typedAt.IsZero() {
		if wait := v.typedAt.Add(typingDelay).Sub(now); wait > 0 {
			c.After(wait)
		} else {
			a.auditLoad() // follows the person's typing only; never a poll
		}
	}
	names := []string{allHosts}
	for _, s := range a.servers {
		names = append(names, model.DisplayText(s.Name))
	}
	if v.host == "" {
		v.host, v.hostNow = allHosts, allHosts
	}
	var change func()
	ui.Column(c).Fill().Label("Audit log").Children(func() {
		ui.Row(c).Gap(8).Padding(8, 12).Wrap().Children(func() {
			// The select writes its choice outside Build: compare with the last applied.
			ui.Select(c.Key("audit-host"), &v.host, names).Label("Host").Width(180)
			if v.host != v.hostNow {
				v.hostNow = v.host
				f := v.filters
				f.Server = ""
				if i := slices.Index(names, v.host); i > 0 {
					f.Server = a.servers[i-1].Name
				}
				change = func() { a.auditChange(f) }
			}
			ui.ToggleGroup(c, func() {
				for _, ch := range model.AuditChips {
					on := slices.Contains(v.filters.Chips, ch.ID)
					if ui.Toggle(c.Key("chip-"+ch.ID), &on, ch.Label).Label("Filter " + ch.Label).Changed() {
						f := v.filters
						f.Chips = model.ToggleChip(f.Chips, ch.ID)
						change = func() { a.auditChange(f) }
					}
				}
			})
			s := ui.SearchField(c.Key("audit-search"), &v.filters.Text).Label("Search audit").Grow(1).Basis(160)
			if s.Changed() {
				v.typedAt = now
				c.After(typingDelay) // the frame that reads it; no key, no timer
			}
			if s.Submitted() {
				change = func() { a.auditLoad() }
			}
			if ui.Button(c, "Refresh").Clicked() {
				change = func() { a.auditLoad() }
			}
			// In the toolbar, not above the list: its showing must not move a row.
			if n := len(v.list.Held); n > 0 {
				if ui.Button(c, fmt.Sprintf("%d new", n)).Clicked() {
					change = func() {
						v.ls.ScrollTo(0, ui.Start)
						v.list = model.ReleaseHeld(v.list)
					}
				}
			}
		})
		if v.list.Error != "" {
			ui.Text(c, model.DisplayText(v.list.Error)).TextColor(t.Danger).Role(ui.RoleStatus).Padding(0, 12)
		}
		a.buildAuditHead(c)
		lines := model.AuditLines(v.list.Entries, now)
		v.ls.Header = func(i int) bool { return lines[i].Heading != "" }
		ui.List(c.Key("audit-list"), &v.ls, len(lines), func(i int) {
			if l := lines[i]; l.Heading != "" {
				ui.Text(c, l.Heading).Bold().Padding(6, 12).Background(t.Surface).Role(ui.RoleHeading)
			} else {
				a.buildAuditRow(c, l, i)
			}
		}).Grow(1).Children(func() {
			switch {
			case v.list.Status == "loading":
				ui.Text(c, "Loading…").TextColor(t.TextMuted).Padding(12)
			case v.list.Status == "loaded" && len(v.list.Entries) == 0:
				ui.Text(c, "No audit records match.").TextColor(t.TextMuted).Padding(12)
			}
		})
		first, _ := v.ls.Visible()
		v.atTop = first <= 0
		if v.atTop && len(v.list.Held) > 0 {
			v.list = model.ReleaseHeld(v.list) // scrolled back to the top
			c.Invalidate()                     // draw the released rows and drop the pill
		}
		ui.Row(c).Gap(8).Padding(6, 12).Children(func() {
			foot := ""
			if n := v.list.Skipped; n > 0 {
				foot = fmt.Sprintf("%d malformed line%s skipped · ", n, map[bool]string{true: "", false: "s"}[n == 1])
			}
			if v.list.Path != "" {
				foot += "Audit file: " + model.DisplayText(v.list.Path)
			}
			ui.Text(c, foot).TextColor(t.TextMuted).Grow(1)
			if v.list.Next != 0 && ui.Button(c, "Load older").Disabled(v.older || v.list.Status != "loaded").Clicked() {
				change = func() { a.auditLoadOlder() }
			}
		})
	})
	if change != nil {
		change()
	}
}

// The columns' widths; Command or change takes the rest.
const (
	auditTimeW  = 72
	auditKindW  = 64
	auditHostW  = 130
	auditBadgeW = 150
	auditSideW  = 170
)

var auditKind = map[string]string{"exec": "Exec", "config": "Config", "file": "File", "tunnel": "Tunnel"}

func (a *App) buildAuditHead(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(8).Padding(4, 12, 4, 34).Children(func() {
		for _, h := range []struct {
			s string
			w float32
		}{{"Time", auditTimeW}, {"Kind", auditKindW}, {"Host", auditHostW}, {"Result", auditBadgeW}} {
			ui.Text(c, h.s).TextColor(t.TextMuted).Width(h.w)
		}
		ui.Text(c, "Command or change").TextColor(t.TextMuted).Grow(1)
		ui.Text(c, "Exit and notes").TextColor(t.TextMuted).Width(auditSideW)
	})
}

// buildAuditRow is one record: a disclosure whose trigger is the row and
// whose panel is the record's detail.
func (a *App) buildAuditRow(c *ui.Context, l model.AuditLine, i int) {
	v := &a.audit
	t := c.Theme()
	seq, rv := l.Row.Seq, l.View
	if v.open == nil {
		v.open, v.raw = map[int]*bool{}, map[int]*bool{}
	}
	if v.open[seq] == nil {
		v.open[seq], v.raw[seq] = new(bool), new(bool)
	}
	tones := map[string]ui.Color{"ai": t.Accent, "auto": t.Warning, "danger": t.Danger, "wait": t.Warning, "plain": t.TextMuted}
	ui.Column(c.Key(fmt.Sprint("rec-", seq))).Children(func() {
		p := ui.CollapsibleBase(c, v.open[seq])
		row := p.Trigger.Gap(8).Padding(6, 12).Label(strings.Join([]string{rv.Time, auditKind[rv.Kind], model.DisplayText(rv.Host), model.DisplayText(rv.Main)}, " "))
		switch {
		case rv.Alert:
			row.Background(t.Danger.Alpha(0.1))
		case i%2 == 0:
			row.Background(t.Surface)
		}
		row.Children(func() {
			ui.Text(c, map[bool]string{true: "▾", false: "▸"}[*v.open[seq]]).TextColor(t.TextMuted).Width(14)
			ui.Text(c, rv.Time).Font("monospace").Width(auditTimeW)
			ui.Text(c, auditKind[rv.Kind]).Width(auditKindW)
			ui.Text(c, model.DisplayText(rv.Host)).Width(auditHostW).SingleLine()
			ui.Row(c).Gap(4).Width(auditBadgeW).Children(func() {
				for _, b := range rv.Badges {
					ui.Text(c, model.DisplayText(b.Text)).TextColor(tones[b.Tone]).SingleLine()
				}
			})
			main := ui.Text(c, model.DisplayText(rv.Main)).Grow(1).Basis(0).SingleLine().Tooltip(model.DisplayText(rv.Main))
			if rv.Kind == "exec" {
				main.Font("monospace")
			}
			ui.Row(c).Gap(4).Width(auditSideW).Children(func() {
				if rv.Exit != nil {
					exit := ui.Text(c, fmt.Sprintf("exit %d", *rv.Exit))
					if *rv.Exit != 0 {
						exit.TextColor(t.Warning)
					}
				}
				side := model.DisplayText(strings.Join(rv.Side, " · "))
				if rv.Exit != nil && side != "" {
					side = "· " + side
				}
				if side != "" {
					ui.Text(c, side).TextColor(t.TextMuted).SingleLine()
				}
			})
		})
		p.Panel(func() { a.buildAuditDetail(c, l.Row) })
	})
}

// buildAuditDetail is an expanded row: the full command, its stdin, the
// AI's description, the facts, what was masked, and the record as stored.
func (a *App) buildAuditDetail(c *ui.Context, r model.AuditRow) {
	t := c.Theme()
	rec := r.Rec
	ui.Column(c).Gap(6).Padding(8, 12, 10, 34).Children(func() {
		exec := rec["kind"] == nil
		if cmd, ok := rec["command"].(string); exec && ok {
			ui.Text(c, model.DisplayText(cmd)).Font("monospace").Selectable()
		}
		if in, ok := rec["stdin"].(string); exec && ok && in != "" {
			ui.Text(c, "stdin").TextColor(t.TextMuted)
			ui.Text(c, model.AuditStdinText(in)).Font("monospace").Selectable()
		}
		if d, ok := rec["description"].(string); ok && d != "" {
			ui.Text(c, "AI's description · unverified").TextColor(t.Warning)
			ui.Text(c, model.DisplayText(d)).Selectable()
		}
		for _, f := range model.AuditFacts(rec) {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, f.Label).TextColor(t.TextMuted).Width(80)
				ui.Text(c, model.DisplayText(f.Value)).Selectable()
			})
		}
		if m := model.AuditMasked(rec); m != "" {
			ui.Text(c, m).TextColor(t.TextMuted)
		}
		ui.Collapsible(c, "Raw JSON", a.audit.raw[r.Seq], func() {
			ui.Text(c, model.AuditRaw(r.Raw)).Font("monospace").Selectable()
		})
	})
}
