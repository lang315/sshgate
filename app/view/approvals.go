package view

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// colState is the AI requests column's own state: what moved its list
// last (for the Allow delay), and each request's reason text.
type colState struct {
	lc      *model.ListChanges
	listH   ui.Handle
	scroll  ui.ScrollState
	feed    ui.ScrollState // the Auto-allowed list's own scroll
	lastY   float32
	reasons map[string]*string
	mounted bool
}

// segments draws a command or stdin with its non-ASCII runs marked; a
// marked run goes through DisplayText so a bidi or zero-width character
// shows as its \uXXXX escape, never raw.
func segments(c *ui.Context, segs []model.Segment) {
	t := c.Theme()
	spans := make([]ui.Span, 0, len(segs))
	for _, s := range segs {
		sp := ui.Span{Text: s.Text}
		if s.NonASCII {
			sp.Text, sp.Color, sp.Underline = model.DisplayText(s.Text), t.Danger, true
		}
		spans = append(spans, sp)
	}
	ui.RichText(c, spans...).Font("monospace").Selectable()
}

func (a *App) buildColumn(c *ui.Context) {
	t := c.Theme()
	now := a.Now()
	ids := make([]string, len(a.items))
	for i, it := range a.items {
		ids[i] = it.Request.ID
	}
	key := strings.Join(ids, "\n")
	col := &a.col
	if !col.mounted || col.lc == nil {
		col.lc, col.mounted, col.lastY = model.NewListChanges(key, now), true, col.scroll.Y
	}
	if col.reasons == nil {
		col.reasons = map[string]*string{}
	}
	col.lc.SetKey(key, now)
	col.lc.SetHeight(col.listH.Bounds(c).H, now)
	if col.scroll.Y != col.lastY {
		col.lc.Touch(now)
		col.lastY = col.scroll.Y
	}
	var wait time.Duration
	for _, it := range a.items {
		wait = max(wait, model.DelayRemaining(it.ShownAt, col.lc.At, now))
	}
	if wait > 0 {
		c.After(wait)
	}

	ui.Column(c).Fill().Gap(8).Padding(12).Background(t.Surface).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			title := "AI requests"
			if n := len(a.items); n > 0 {
				title += fmt.Sprintf("  %d", n)
			}
			ui.Text(c, title).Bold().Grow(1)
			if ui.Button(c, "Deny all").Disabled(len(a.items) < 2).Clicked() {
				a.denyAll()
			}
			if ui.Button(c, "×").Label("Close AI requests").Clicked() {
				a.aiOpen = false
			}
		})
		ui.Text(c, "Commands wait for you unless their host is on auto-allow. Enter in a request denies; Allow takes a mouse click.").TextColor(t.TextMuted)
		ui.Scroll(c).Grow(1).TrackScroll(&col.scroll).Children(func() {
			ui.Column(c).Gap(10).Bind(&col.listH).Children(func() {
				if a.denyAllErr != "" {
					ui.Text(c, model.DisplayText(a.denyAllErr)).TextColor(t.Danger)
				}
				if len(a.items) == 0 {
					if a.seedErr != "" {
						ui.Text(c, "Could not load pending requests: "+model.DisplayText(a.seedErr)).TextColor(t.Danger)
					} else {
						ui.Text(c, "Nothing waiting.").TextColor(t.TextMuted)
					}
				}
				for _, it := range a.items {
					a.buildItem(c, it, now)
				}
			})
		})
		a.buildFeed(c)
	})
}

func (a *App) buildItem(c *ui.Context, it model.PendingItem, now time.Time) {
	t := c.Theme()
	r := it.Request
	id := r.ID
	if a.col.reasons[id] == nil {
		a.col.reasons[id] = new(string)
	}
	reason := a.col.reasons[id]
	busy := a.itemBusy[id]
	border := t.Border
	if r.Sudo {
		border = t.Danger
	}
	ui.Column(c.Key(id)).Gap(6).Padding(10).Radius(8).Border(1, border).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			ui.Text(c, model.DisplayText(r.Server)).Bold()
			if r.Sudo {
				ui.Text(c, "SUDO").TextColor(t.Danger).Bold()
			}
			ui.Spacer(c)
			if !r.ReceivedAt.IsZero() {
				ui.Text(c, r.ReceivedAt.Local().Format("15:04:05")).Font("monospace").TextColor(t.TextMuted)
			}
		})
		ui.Text(c, model.DisplayText(r.Target)).Font("monospace").TextColor(t.TextMuted)
		segments(c, model.HighlightNonASCII(r.Command, ""))
		if s := model.NonASCIISummary(r.Command, ""); s != "" {
			ui.Text(c, s).TextColor(t.Warning)
		}
		if r.Stdin != "" {
			ui.Text(c, "stdin · "+model.StdinMeta(r.Stdin)).TextColor(t.TextMuted)
			segments(c, model.StdinView(r.Stdin))
			if s := model.NonASCIISummary(r.Stdin, "\n\t"); s != "" {
				ui.Text(c, s).TextColor(t.Warning)
			}
		}
		if r.Description != "" {
			ui.Text(c, "AI's description · unverified").TextColor(t.TextMuted)
			ui.Text(c, model.DisplayText(r.Description)).MaxLines(4)
		}
		ui.Text(c, fmt.Sprintf("timeout %ds · client %s (unverified)", r.TimeoutSec, model.DisplayText(r.Client))).TextColor(t.TextMuted)
		in := ui.TextInput(c.Key("why-"+id), reason).Placeholder("Reason (optional)").Label("Reason (optional)")
		if in.Submitted() {
			a.decide(id, hubapi.Denied, *reason)
		}
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, "Deny").Disabled(busy).Clicked() {
				a.decide(id, hubapi.Denied, *reason)
			}
			MouseButton(c, "Send to tab", false) // slice 2 brings tabs
			enabled := model.ClickAllowed(it, now, a.col.lc.At, busy)
			if MouseButton(c, "Allow", enabled) && model.ClickAllowed(it, a.Now(), a.col.lc.At, a.itemBusy[id]) {
				a.decide(id, hubapi.Allowed, "")
			}
		})
		if e := a.itemErr[id]; e != "" {
			ui.Text(c, model.DisplayText(e)).TextColor(t.Danger)
		}
	})
}

func (a *App) buildFeed(c *ui.Context) {
	t := c.Theme()
	autoN := len(model.AutoHostNames(a.servers, a.Now()))
	// Electron's .autofeed { flex: none; max-height: 45% }, so runs never push
	// requests out of reach. MinHeight(0): mygo's automatic minimum (the
	// content's height) is not clamped by the max height.
	ui.Column(c).Gap(6).MinHeight(0).Shrink(0).MaxHeightPercent(45).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "Auto-allowed").Bold().Grow(1)
			if ui.Button(c, "Stop all auto-allow").Disabled(autoN == 0).Clicked() {
				a.stopAll()
			}
		})
		if len(a.feed) == 0 {
			ui.Text(c, "Nothing ran on auto-allow.").TextColor(t.TextMuted)
			return
		}
		ui.Scroll(c).Grow(1).Gap(6).TrackScroll(&a.col.feed).Children(func() {
			for i, r := range a.feed {
				ui.Column(c.Key(fmt.Sprintf("%s-%d", r.Time, i))).Gap(4).Padding(8).Radius(6).Border(1, t.Border).Children(func() {
					ui.Row(c).Gap(6).Children(func() {
						ui.Text(c, model.DisplayText(r.Server)).Bold()
						if r.Sudo {
							ui.Text(c, "sudo").TextColor(t.Danger)
						}
						if r.StdinBytes > 0 {
							ui.Text(c, fmt.Sprintf("stdin %d B", r.StdinBytes)).TextColor(t.TextMuted)
						}
						status, col := "error", t.Danger
						if r.Error == "" && r.ExitCode != nil {
							status, col = fmt.Sprintf("exit %d", *r.ExitCode), t.Success
							if *r.ExitCode != 0 {
								col = t.Warning
							}
						}
						ui.Text(c, status).TextColor(col)
						ui.Spacer(c)
						if ts, err := time.Parse(time.RFC3339, r.Time); err == nil {
							ui.Text(c, ts.Local().Format("15:04:05")).Font("monospace").TextColor(t.TextMuted)
						}
					})
					segments(c, model.HighlightNonASCII(model.CommandLabel(r), ""))
					if r.Error != "" {
						ui.Text(c, model.DisplayText(r.Error)).TextColor(t.Danger)
					}
					if r.Description != "" {
						ui.Text(c, model.DisplayText(r.Description)).MaxLines(2).TextColor(t.TextMuted)
					}
				})
			}
		})
	})
}
