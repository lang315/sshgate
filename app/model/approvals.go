package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

// AllowDelay keeps Allow and Send to tab disabled after a request appears
// or the list changes, so nothing that moves under the pointer is pressed
// by a click meant for something else.
const AllowDelay = 500 * time.Millisecond

type PendingItem struct {
	Request hubapi.Request
	ShownAt time.Time
}

func AddPending(items []PendingItem, r hubapi.Request, now time.Time) []PendingItem {
	for _, it := range items {
		if it.Request.ID == r.ID {
			return items
		}
	}
	return append(append([]PendingItem(nil), items...), PendingItem{Request: r, ShownAt: now})
}

func RemoveDecided(items []PendingItem, id string) []PendingItem {
	out := make([]PendingItem, 0, len(items))
	for _, it := range items {
		if it.Request.ID != id {
			out = append(out, it)
		}
	}
	return out
}

func DelayElapsed(opened, changed, now time.Time) bool {
	return DelayRemaining(opened, changed, now) == 0
}

// DelayRemaining is how long until AllowDelay has passed since the later
// of opened and changed; 0 once it has.
func DelayRemaining(opened, changed, now time.Time) time.Duration {
	from := opened
	if changed.After(from) {
		from = changed
	}
	return max(0, AllowDelay-now.Sub(from))
}

func AllowEnabled(it PendingItem, now, changedAt time.Time) bool {
	return DelayElapsed(it.ShownAt, changedAt, now)
}

func ClickAllowed(it PendingItem, now, changedAt time.Time, busy bool) bool {
	return !busy && AllowEnabled(it, now, changedAt)
}

// ListChanges records when the request list last changed in a way that
// can move a button: its ids, its height, its scroll, or its mounting.
type ListChanges struct {
	At       time.Time
	key      string
	height   float32
	measured bool
}

// NewListChanges counts the column mounting as a change.
func NewListChanges(key string, at time.Time) *ListChanges {
	return &ListChanges{At: at, key: key}
}

func (l *ListChanges) SetKey(key string, now time.Time) {
	if key != l.key {
		l.key, l.At = key, now
	}
}

// SetHeight reports a height change; the first measurement is not one.
func (l *ListChanges) SetHeight(h float32, now time.Time) bool {
	if !l.measured {
		l.measured, l.height = true, h
		return false
	}
	if h == l.height {
		return false
	}
	l.height, l.At = h, now
	return true
}

func (l *ListChanges) Touch(now time.Time) { l.At = now }

// MergeSeed folds the pending snapshot into the live list: a request
// decided while the snapshot was in flight stays gone, one that arrived
// since stays shown, and known requests keep their shownAt.
func MergeSeed(current []PendingItem, snapshot []hubapi.Request, decidedSince, pendingSince map[string]bool, now time.Time) []PendingItem {
	known := map[string]PendingItem{}
	for _, it := range current {
		known[it.Request.ID] = it
	}
	inSnap := map[string]bool{}
	var out []PendingItem
	for _, r := range snapshot {
		inSnap[r.ID] = true
		if decidedSince[r.ID] {
			continue
		}
		shown := now
		if it, ok := known[r.ID]; ok {
			shown = it.ShownAt
		}
		out = append(out, PendingItem{Request: r, ShownAt: shown})
	}
	for _, it := range current {
		id := it.Request.ID
		if !inSnap[id] && !decidedSince[id] && pendingSince[id] {
			out = append(out, it)
		}
	}
	return out
}

type Segment struct {
	Text     string
	NonASCII bool
}

func isNonASCII(r rune) bool { return r > 0x7e || r < 0x20 }

// HighlightNonASCII splits s into runs of ASCII and non-ASCII characters
// (controls included) so homoglyphs and hidden characters stand out;
// characters in plain are left unmarked.
func HighlightNonASCII(s, plain string) []Segment {
	var out []Segment
	for _, r := range s {
		na := isNonASCII(r) && !strings.ContainsRune(plain, r)
		if n := len(out); n > 0 && out[n-1].NonASCII == na {
			out[n-1].Text += string(r)
			continue
		}
		out = append(out, Segment{string(r), na})
	}
	return out
}

func NonASCIISummary(s, plain string) string {
	n := 0
	var distinct []rune
	seen := map[rune]bool{}
	for _, r := range s {
		if !isNonASCII(r) || strings.ContainsRune(plain, r) {
			continue
		}
		n++
		if !seen[r] {
			seen[r] = true
			distinct = append(distinct, r)
		}
	}
	if n == 0 {
		return ""
	}
	var shown []string
	for i, r := range distinct {
		if i == 5 {
			shown = append(shown, fmt.Sprintf("+%d more", len(distinct)-5))
			break
		}
		shown = append(shown, fmt.Sprintf("U+%04X", r))
	}
	return fmt.Sprintf("%d non-ASCII character%s highlighted (%s)", n, plural(n), strings.Join(shown, ", "))
}

// StdinView shows CR as ␍ (itself highlighted); newlines and tabs stay plain.
func StdinView(s string) []Segment {
	return HighlightNonASCII(strings.ReplaceAll(s, "\r", "␍"), "\n\t")
}

func StdinMeta(s string) string {
	bytes := len(s)
	lines := 0
	if s != "" {
		lines = strings.Count(s, "\n") + 1
		if strings.HasSuffix(s, "\n") {
			lines--
		}
	}
	return fmt.Sprintf("%d byte%s, %d line%s", bytes, plural(bytes), lines, plural(lines))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
