package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

// The Audit tab's rules (desktop/src/renderer/audit.ts).

// AuditChip is one filter chip: a kind chip adds that record kind, Auto
// and Denied add those exec outcomes. The hub ORs kinds and outcomes, so
// the chips that are on form a union, and none on means every record.
type AuditChip struct{ ID, Label, Kind, Outcome string }

// AuditChips are the filter chips, in display order.
var AuditChips = []AuditChip{
	{ID: "exec", Label: "Exec", Kind: "exec"},
	{ID: "auto", Label: "Auto", Outcome: "auto"},
	{ID: "denied", Label: "Denied", Outcome: "denied"},
	{ID: "config", Label: "Config", Kind: "config"},
	{ID: "files", Label: "Files", Kind: "file"},
	{ID: "tunnels", Label: "Tunnels", Kind: "tunnel"},
}

// AuditFilters are the toolbar's choices: a host ("" all), the chips on
// (by ID), and the search text.
type AuditFilters struct {
	Server string
	Chips  []string
	Text   string
}

// ToggleChip turns chip c on or off.
func ToggleChip(chips []string, c string) []string {
	if slices.Contains(chips, c) {
		return slices.DeleteFunc(slices.Clone(chips), func(x string) bool { return x == c })
	}
	return append(slices.Clone(chips), c)
}

// AuditQueryFor is the audit.read query for f, in chip order.
func AuditQueryFor(f AuditFilters) hubapi.AuditQuery {
	q := hubapi.AuditQuery{Server: f.Server, Text: strings.TrimSpace(f.Text)}
	for _, c := range AuditChips {
		if !slices.Contains(f.Chips, c.ID) {
			continue
		}
		if c.Kind != "" {
			q.Kinds = append(q.Kinds, c.Kind)
		}
		if c.Outcome != "" {
			q.Outcomes = append(q.Outcomes, c.Outcome)
		}
	}
	return q
}

// AuditRecord is one record as stored, decoded with its numbers kept as
// json.Number. The file can be edited by hand: never trust a field's type.
type AuditRecord map[string]any

// AuditRow is a record, its seq, and its line as stored (for Raw JSON).
type AuditRow struct {
	Seq int
	Rec AuditRecord
	Raw json.RawMessage
}

// RowOf decodes e; a record that is not a JSON object reads as empty.
func RowOf(e hubapi.AuditEntry) AuditRow {
	var rec AuditRecord
	d := json.NewDecoder(bytes.NewReader(e.Record))
	d.UseNumber()
	if d.Decode(&rec) != nil || rec == nil {
		rec = AuditRecord{}
	}
	return AuditRow{Seq: e.Seq, Rec: rec, Raw: e.Record}
}

func (r AuditRecord) str(k string) string {
	s, _ := r[k].(string)
	return s
}

func (r AuditRecord) list(k string) []string {
	v, _ := r[k].([]any)
	out := make([]string, 0, len(v))
	for _, x := range v {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func (r AuditRecord) num(k string) (float64, bool) {
	n, ok := r[k].(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}

// truthy is JavaScript's truthiness, for the fields Electron tests that way.
func truthy(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		f, err := v.Float64()
		return err == nil && f != 0
	}
	return true
}

// broker's fields.outcomeIs, for records that arrive by audit.appended.
func outcomeIs(r AuditRecord, o string) bool {
	switch o {
	case "auto":
		return r["approval"] == "auto"
	case "allowed":
		return r["outcome"] == "allowed" && r["approval"] != "auto"
	case "cancelled":
		return r["outcome"] == "approved_but_cancelled" || r["outcome"] == "cancelled_running"
	}
	return r["outcome"] == o
}

// leaves is what a text search sees: every string and number value,
// walked recursively, one per line, like the hub's leaves. Never a key
// name or an escape.
func leaves(v any, b *strings.Builder) {
	switch v := v.(type) {
	case string:
		b.WriteString(v)
		b.WriteByte('\n')
	case json.Number:
		b.WriteString(v.String())
		b.WriteByte('\n')
	case []any:
		for _, x := range v {
			leaves(x, b)
		}
	case AuditRecord:
		leaves(map[string]any(v), b)
	case map[string]any:
		for _, x := range v {
			leaves(x, b)
		}
	}
}

// AuditMatches reports whether r belongs in a list read with q: broker's
// ReadQuery.match, mirrored.
func AuditMatches(r AuditRecord, q hubapi.AuditQuery) bool {
	if q.Server != "" && r.str("server") != q.Server && !slices.Contains(r.list("servers"), q.Server) {
		return false
	}
	if q.Text != "" {
		var b strings.Builder
		leaves(r, &b)
		if !strings.Contains(strings.ToLower(b.String()), strings.ToLower(q.Text)) {
			return false
		}
	}
	if len(q.Kinds) == 0 && len(q.Outcomes) == 0 {
		return true
	}
	kind := r.str("kind")
	if kind == "" {
		kind = "exec"
	}
	if slices.Contains(q.Kinds, kind) {
		return true
	}
	return kind == "exec" && slices.ContainsFunc(q.Outcomes, func(o string) bool { return outcomeIs(r, o) })
}

func parseTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

// AuditTime is HH:MM:SS in now's zone, "" when iso is not a time. The
// day is the row group's heading (DayLabel).
func AuditTime(iso string, now time.Time) string {
	t, ok := parseTime(iso)
	if !ok {
		return ""
	}
	return t.In(now.Location()).Format("15:04:05")
}

// DayLabel is the heading of a record's day group: Today, Yesterday, or
// "Tue Sep 29 2026", in now's zone; "No date" when iso is not a time.
func DayLabel(iso string, now time.Time) string {
	t, ok := parseTime(iso)
	if !ok {
		return "No date"
	}
	day := func(t time.Time) string { return t.Format("Mon Jan 02 2006") }
	d := day(t.In(now.Location()))
	switch d {
	case day(now):
		return "Today"
	case day(now.AddDate(0, 0, -1)):
		return "Yesterday"
	}
	return d
}

func formatWait(ms float64) string {
	if ms < 1000 {
		return fmt.Sprintf("%v ms", ms)
	}
	return fmt.Sprintf("%v s", math.Round(ms/1000))
}

// grantLength is a timed grant's length (15m, 2h), from its record's time
// to its deadline.
func grantLength(r AuditRecord) string {
	if r["forever"] == true {
		return "forever"
	}
	until, ok1 := parseTime(r.str("until"))
	at, ok2 := parseTime(r.str("time"))
	if !ok1 || !ok2 {
		return ""
	}
	m := int(math.Round(until.Sub(at).Minutes()))
	if m >= 60 && m%60 == 0 {
		return fmt.Sprintf("%dh", m/60)
	}
	return fmt.Sprintf("%dm", m)
}

// AuditStdinText is an exec record's stdin as the approval card showed it:
// newlines and tabs kept, CR as ␍; every other hidden character escaped
// by DisplayText.
func AuditStdinText(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r", "␍"), "\n")
	for i, l := range lines {
		parts := strings.Split(l, "\t")
		for j, p := range parts {
			parts[j] = DisplayText(p)
		}
		lines[i] = strings.Join(parts, "\t")
	}
	return strings.Join(lines, "\n")
}

// maskedKinds are a record's redacted counts, by kind in key order.
func maskedKinds(r AuditRecord) (kinds []string, counts []float64) {
	m, _ := r["redacted"].(map[string]any)
	for k := range m {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	kinds = slices.DeleteFunc(kinds, func(k string) bool { _, ok := m[k].(json.Number); return !ok })
	for _, k := range kinds {
		f, _ := m[k].(json.Number).Float64()
		counts = append(counts, f)
	}
	return kinds, counts
}

// MaskedCount is how many values the hub masked in a run's output.
func MaskedCount(r AuditRecord) int {
	_, counts := maskedKinds(r)
	n := 0.0
	for _, c := range counts {
		n += c
	}
	return int(n)
}

// Badge tones: "ai", "auto", "danger", "wait" and "plain".
type Badge struct{ Text, Tone string }

// RowView is one table row: time, kind, host, badges, the main text, and
// the side notes. Alert marks a row the gate refused or failed (denied,
// error); Exit is an exec's exit code, shown apart from Side so a
// non-zero one can stand out.
type RowView struct {
	Time, Day, Host, Kind string
	Badges                []Badge
	Main                  string
	Side                  []string
	Exit                  *int
	Alert                 bool
}

var execBadge = map[string]Badge{
	"allowed":                {"allowed", "ai"},
	"denied":                 {"denied", "danger"},
	"expired":                {"expired", "wait"},
	"sent_to_tab":            {"sent to tab", "plain"},
	"approved_but_cancelled": {"cancelled", "wait"},
	"cancelled_running":      {"cancelled", "wait"},
	"error":                  {"error", "danger"},
}

var camel = regexp.MustCompile(`([a-z])([A-Z])`)

// AuditRowView is r's row.
func AuditRowView(r AuditRecord, now time.Time) RowView {
	kind := r.str("kind")
	if kind != "config" && kind != "file" && kind != "tunnel" {
		kind = "exec"
	}
	v := RowView{Time: AuditTime(r.str("time"), now), Day: DayLabel(r.str("time"), now), Host: r.str("server"), Kind: kind, Side: []string{}}
	if v.Host == "" {
		v.Host = strings.Join(r.list("servers"), ", ")
	}
	switch kind {
	case "config":
		action := r.str("action")
		changed := r.list("changed")
		switch {
		case action == "autoAllowOn":
			v.Main = grantLength(r)
		case action == "softLock":
			v.Main = strings.Join(r.list("servers"), ", ")
		case len(changed) > 0:
			v.Main = strings.Join(changed, ", ")
		default:
			v.Main = cmpOr(r.str("reason"), r.str("fingerprint"), r.str("oldFingerprint"))
		}
		// The badge spaces the camelCase action (autoAllowOn reads "auto allow
		// on"); Raw JSON keeps the stored name, which is what a search matches.
		v.Badges = []Badge{{strings.ToLower(camel.ReplaceAllString(action, "$1 $2")), "plain"}}
		return v
	case "file":
		if r["action"] == "rename" {
			v.Main = r.str("from") + " → " + r.str("to")
		} else if l := r.list("remote"); len(l) > 0 {
			v.Main = l[0]
		}
		v.Badges = []Badge{{r.str("action"), "plain"}}
		if truthy(r["phase"]) {
			v.Side = []string{r.str("phase")}
		}
		return v
	case "tunnel":
		v.Badges = []Badge{{r.str("phase"), "plain"}}
		if truthy(r["to"]) {
			v.Main = r.str("listen") + " → " + r.str("to")
		} else {
			v.Main = r.str("listen") + " SOCKS5"
		}
		return v
	}
	outcome := r.str("outcome")
	auto := r["approval"] == "auto"
	if auto {
		v.Badges = append(v.Badges, Badge{"auto", "auto"})
	}
	if !auto || outcome != "allowed" {
		b, ok := execBadge[outcome]
		if !ok {
			b = Badge{outcome, "plain"}
		}
		v.Badges = append(v.Badges, b)
	}
	if r["sudo"] == true {
		v.Badges = append(v.Badges, Badge{"sudo", "danger"})
	}
	if w, ok := r.num("waitMs"); ok && w > 0 {
		v.Side = append(v.Side, "wait "+formatWait(w))
	}
	if n := MaskedCount(r); n > 0 {
		v.Side = append(v.Side, fmt.Sprintf("%d masked", n))
	}
	if e, ok := r.num("exitCode"); ok {
		code := int(e)
		v.Exit = &code
	}
	v.Alert = outcome == "denied" || outcome == "error"
	v.Main = r.str("command")
	return v
}

func cmpOr(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// AuditFact is one label and value of an expanded row's facts.
type AuditFact struct{ Label, Value string }

// AuditFacts are an expanded row's facts: Reason, Client, Timeout and
// Duration, each only when the record has it.
func AuditFacts(r AuditRecord) []AuditFact {
	var out []AuditFact
	text := func(k string) string {
		if s, ok := r[k].(string); ok {
			return s
		}
		if n, ok := r[k].(json.Number); ok {
			return n.String()
		}
		return ""
	}
	if s := text("reason"); s != "" {
		out = append(out, AuditFact{"Reason", s})
	}
	if s := text("client"); s != "" {
		out = append(out, AuditFact{"Client", s})
	}
	if n, ok := r["timeoutSec"].(json.Number); ok {
		out = append(out, AuditFact{"Timeout", n.String() + " s"})
	}
	if n, ok := r["durationMs"].(json.Number); ok {
		out = append(out, AuditFact{"Duration", n.String() + " ms"})
	}
	return out
}

// AuditMasked is the expanded row's "Masked: password ×2, token ×1", or ""
// when nothing was masked.
func AuditMasked(r AuditRecord) string {
	if MaskedCount(r) <= 0 {
		return ""
	}
	kinds, counts := maskedKinds(r)
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = fmt.Sprintf("%s ×%v", DisplayText(k), counts[i])
	}
	return "Masked: " + strings.Join(parts, ", ")
}

// AuditRaw is the record as stored, indented, each line through
// DisplayText.
func AuditRaw(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Indent(&b, raw, "", "  ") != nil {
		return DisplayText(string(raw))
	}
	lines := strings.Split(b.String(), "\n")
	for i, l := range lines {
		lines[i] = DisplayText(l)
	}
	return strings.Join(lines, "\n")
}

// AuditList is the records the tab holds. "cleared" follows a lock and
// never loads by itself; the view resets it to "idle" when the ready
// screen is entered, and reads from "idle" only while the tab shows.
type AuditList struct {
	Status  string     // "idle", "loading", "loaded", "error" or "cleared"
	Entries []AuditRow // newest first, one per seq
	Held    []AuditRow // live records that arrived while scrolled away: "N new"
	Next    int        // the Before of Load older; 0 when nothing is older
	Skipped int
	Path    string
	Error   string
}

// MergeEntries is a and b, one per seq, newest first.
func MergeEntries(a, b []AuditRow) []AuditRow {
	seen := map[int]bool{}
	var out []AuditRow
	for _, e := range slices.Concat(a, b) {
		if !seen[e.Seq] {
			seen[e.Seq] = true
			out = append(out, e)
		}
	}
	slices.SortStableFunc(out, func(x, y AuditRow) int { return y.Seq - x.Seq })
	return out
}

// StartLoad is a fresh list loading; it keeps the path shown.
func StartLoad(l AuditList) AuditList { return AuditList{Status: "loading", Path: l.Path} }

// ApplyPage adds a page. A page's Skipped counts only the lines that read
// scanned, so the pages of one list add up; StartLoad starts the count over.
func ApplyPage(l AuditList, p hubapi.AuditPage) AuditList {
	rows := make([]AuditRow, len(p.Records))
	for i, e := range p.Records {
		rows[i] = RowOf(e)
	}
	l.Status, l.Entries, l.Next, l.Skipped, l.Path, l.Error = "loaded", MergeEntries(l.Entries, rows), p.Next, l.Skipped+p.Skipped, p.Path, ""
	return l
}

// FailLoad keeps what is shown, with the error.
func FailLoad(l AuditList, msg string) AuditList {
	l.Status, l.Error = "error", msg
	return l
}

// AppendLive adds an audit.appended record: kept only while a list is
// loading or loaded, if it matches the query that list was read with and
// is not there yet; held while the list is scrolled away from the top, so
// rows never move under the cursor. It reports whether l changed.
func AppendLive(l AuditList, e AuditRow, q hubapi.AuditQuery, atTop bool) (AuditList, bool) {
	if l.Status != "loading" && l.Status != "loaded" {
		return l, false
	}
	if !AuditMatches(e.Rec, q) {
		return l, false
	}
	// The normal case is a seq above everything held: prepend, no sort.
	// Out of order falls back to the merge, which also dedupes.
	top := 0
	if len(l.Entries) > 0 {
		top = l.Entries[0].Seq
	}
	if len(l.Held) > 0 {
		top = max(top, l.Held[0].Seq)
	}
	fresh := e.Seq > top
	has := func(r AuditRow) bool { return r.Seq == e.Seq }
	if !fresh && (slices.ContainsFunc(l.Entries, has) || slices.ContainsFunc(l.Held, has)) {
		return l, false
	}
	add := func(list []AuditRow) []AuditRow {
		if fresh {
			return append([]AuditRow{e}, list...)
		}
		return MergeEntries(list, []AuditRow{e})
	}
	if atTop {
		l.Entries = add(l.Entries)
	} else {
		l.Held = add(l.Held)
	}
	return l, true
}

// ReleaseHeld moves the held records into the list.
func ReleaseHeld(l AuditList) AuditList {
	if len(l.Held) > 0 {
		l.Entries, l.Held = MergeEntries(l.Entries, l.Held), nil
	}
	return l
}

// ClearOnLock is the list after a lock: nothing shown, nothing loads.
func ClearOnLock() AuditList { return AuditList{Status: "cleared"} }

// AuditLine is one row of the tab's list: a day heading (Heading set) or a
// record with its view.
type AuditLine struct {
	Heading string
	Row     AuditRow
	View    RowView
}

// AuditLines is entries under one heading per day, newest first.
func AuditLines(entries []AuditRow, now time.Time) []AuditLine {
	var out []AuditLine
	for i, e := range entries {
		v := AuditRowView(e.Rec, now)
		if i == 0 || v.Day != out[len(out)-1].View.Day {
			out = append(out, AuditLine{Heading: v.Day, View: RowView{Day: v.Day}})
		}
		out = append(out, AuditLine{Row: e, View: v})
	}
	return out
}
