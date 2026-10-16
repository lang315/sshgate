package model

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

// The cases of desktop/test/audit.test.ts. now is in a zone of its own, so
// the tests do not depend on the machine's.
var auditNow = time.Date(2026, 9, 30, 14, 5, 9, 0, time.FixedZone("UTC+7", 7*3600))

func iso(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// rec decodes a record from JSON, with "time" at auditNow unless given.
func rec(t *testing.T, js string) AuditRecord {
	t.Helper()
	r := RowOf(hubapi.AuditEntry{Seq: 1, Record: json.RawMessage(js)}).Rec
	if _, ok := r["time"]; !ok {
		r["time"] = iso(auditNow)
	}
	return r
}

func entry(seq int, js string) AuditRow {
	if js == "" {
		js = `{}`
	}
	return RowOf(hubapi.AuditEntry{Seq: seq, Record: json.RawMessage(js)})
}

func seqs(rows []AuditRow) []int {
	out := []int{}
	for _, r := range rows {
		out = append(out, r.Seq)
	}
	return out
}

func loaded(rows ...AuditRow) AuditList { return AuditList{Status: "loaded", Entries: rows} }

func TestAuditTimeAndDay(t *testing.T) {
	if got := AuditTime(iso(auditNow), auditNow); got != "14:05:09" {
		t.Errorf("time %q", got)
	}
	if got := AuditTime("not a time", auditNow); got != "" {
		t.Errorf("bad time %q", got)
	}
	loc := auditNow.Location()
	for _, tc := range []struct {
		at   string
		want string
	}{
		{iso(auditNow), "Today"},
		{iso(time.Date(2026, 9, 29, 23, 59, 1, 0, loc)), "Yesterday"},
		{iso(time.Date(2026, 1, 5, 12, 0, 0, 0, loc)), "Mon Jan 05 2026"},
		{"not a time", "No date"},
	} {
		if got := DayLabel(tc.at, auditNow); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.at, got, tc.want)
		}
	}
}

func TestAuditRowViewExec(t *testing.T) {
	v := AuditRowView(rec(t, `{"server":"box","command":"rm -rf /tmp/x","outcome":"denied","sudo":true,"waitMs":12300}`), auditNow)
	want := RowView{Time: "14:05:09", Day: "Today", Host: "box", Kind: "exec", Alert: true, Main: "rm -rf /tmp/x", Side: []string{"wait 12 s"},
		Badges: []Badge{{"denied", "danger"}, {"sudo", "danger"}}}
	if !reflect.DeepEqual(v, want) {
		t.Fatalf("%+v\nwant %+v", v, want)
	}

	v = AuditRowView(rec(t, `{"server":"box","command":"cat .env","outcome":"allowed","approval":"auto","exitCode":0,"redacted":{"password":2,"token":1}}`), auditNow)
	if !reflect.DeepEqual(v.Badges, []Badge{{"auto", "auto"}}) || !reflect.DeepEqual(v.Side, []string{"3 masked"}) || v.Exit == nil || *v.Exit != 0 || v.Alert {
		t.Fatalf("auto run %+v", v)
	}

	badges := func(js string) []string {
		var out []string
		for _, b := range AuditRowView(rec(t, js), auditNow).Badges {
			out = append(out, b.Text)
		}
		return out
	}
	for js, want := range map[string][]string{
		`{"command":"x","outcome":"allowed"}`:                    {"allowed"},
		`{"command":"x","outcome":"error","approval":"auto"}`:    {"auto", "error"},
		`{"command":"x","outcome":"expired"}`:                    {"expired"},
		`{"command":"x","outcome":"approved_but_cancelled"}`:     {"cancelled"},
		`{"command":"x","outcome":"cancelled_running"}`:          {"cancelled"},
		`{"command":"x","outcome":"sent_to_tab"}`:                {"sent to tab"},
		`{"command":"x","outcome":"something new","sudo":"yes"}`: {"something new"},
	} {
		if got := badges(js); !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q", js, got, want)
		}
	}
	if s := AuditRowView(rec(t, `{"command":"x","outcome":"allowed","waitMs":400}`), auditNow).Side; !slices.Equal(s, []string{"wait 400 ms"}) {
		t.Errorf("wait %q", s)
	}
}

func TestAuditRowViewConfig(t *testing.T) {
	main := func(js string) string { return AuditRowView(rec(t, js), auditNow).Main }
	for js, want := range map[string]string{
		`{"kind":"config","action":"autoAllowOn","until":"` + iso(auditNow.Add(15*time.Minute-800*time.Millisecond)) + `"}`: "15m",
		`{"kind":"config","action":"autoAllowOn","until":"` + iso(auditNow.Add(2*time.Hour)) + `"}`:                         "2h",
		`{"kind":"config","action":"autoAllowOn","forever":true}`:                                                           "forever",
		`{"kind":"config","action":"save","changed":["host: a → 10.0.0.2","password"]}`:                                     "host: a → 10.0.0.2, password",
		`{"kind":"config","action":"autoAllowOff","reason":"locked"}`:                                                       "locked",
		`{"kind":"config","action":"delete"}`:                                                                               "",
	} {
		if got := main(js); got != want {
			t.Errorf("%s: %q, want %q", js, got, want)
		}
	}
	if b := AuditRowView(rec(t, `{"kind":"config","action":"save","server":"box"}`), auditNow).Badges; !reflect.DeepEqual(b, []Badge{{"save", "plain"}}) {
		t.Errorf("save badge %+v", b)
	}
	if b := AuditRowView(rec(t, `{"kind":"config","action":"forgetHostKey"}`), auditNow).Badges; !reflect.DeepEqual(b, []Badge{{"forget host key", "plain"}}) {
		t.Errorf("forget badge %+v", b)
	}
	v := AuditRowView(rec(t, `{"kind":"config","action":"softLock","servers":["box","db"]}`), auditNow)
	if v.Main != "box, db" || v.Host != "box, db" {
		t.Errorf("softLock %+v", v)
	}
}

func TestAuditRowViewFileAndTunnel(t *testing.T) {
	v := AuditRowView(rec(t, `{"kind":"file","server":"box","action":"upload","phase":"start","remote":["/home/u/a.txt","/home/u/b.txt"]}`), auditNow)
	want := RowView{Time: "14:05:09", Day: "Today", Host: "box", Kind: "file", Badges: []Badge{{"upload", "plain"}}, Main: "/home/u/a.txt", Side: []string{"start"}}
	if !reflect.DeepEqual(v, want) {
		t.Fatalf("%+v\nwant %+v", v, want)
	}
	if m := AuditRowView(rec(t, `{"kind":"file","action":"rename","from":"/a","to":"/b"}`), auditNow).Main; m != "/a → /b" {
		t.Errorf("rename %q", m)
	}
	v = AuditRowView(rec(t, `{"kind":"tunnel","server":"box","phase":"start","listen":"127.0.0.1:5433","to":"db:5432"}`), auditNow)
	if !reflect.DeepEqual(v.Badges, []Badge{{"start", "plain"}}) || v.Main != "127.0.0.1:5433 → db:5432" {
		t.Errorf("tunnel %+v", v)
	}
	if m := AuditRowView(rec(t, `{"kind":"tunnel","phase":"end","listen":"127.0.0.1:1080"}`), auditNow).Main; m != "127.0.0.1:1080 SOCKS5" {
		t.Errorf("socks %q", m)
	}
}

// A hand-edited file: wrong field types never panic, and read as empty.
func TestAuditRowViewWrongTypes(t *testing.T) {
	v := AuditRowView(RowOf(hubapi.AuditEntry{Seq: 1, Record: json.RawMessage(`{"time":5,"command":7,"server":{},"outcome":null,"redacted":{"x":"y"},"exitCode":"0","waitMs":"9"}`)}).Rec, auditNow)
	if v.Time != "" || v.Host != "" || v.Main != "" || v.Exit != nil || len(v.Side) != 0 {
		t.Fatalf("%+v", v)
	}
	for _, raw := range []string{`[1,2]`, `"text"`, `null`, `not json`} {
		r := RowOf(hubapi.AuditEntry{Seq: 2, Record: json.RawMessage(raw)})
		if len(r.Rec) != 0 || AuditRowView(r.Rec, auditNow).Kind != "exec" {
			t.Errorf("%s: %+v", raw, r.Rec)
		}
	}
}

func TestAuditQueryFor(t *testing.T) {
	for _, tc := range []struct {
		f    AuditFilters
		want hubapi.AuditQuery
	}{
		{AuditFilters{}, hubapi.AuditQuery{}},
		{AuditFilters{Chips: []string{"auto"}}, hubapi.AuditQuery{Outcomes: []string{"auto"}}},
		{AuditFilters{Chips: []string{"denied", "exec"}}, hubapi.AuditQuery{Kinds: []string{"exec"}, Outcomes: []string{"denied"}}},
		{AuditFilters{Chips: []string{"tunnels", "files", "config"}}, hubapi.AuditQuery{Kinds: []string{"config", "file", "tunnel"}}},
		{AuditFilters{Server: "box", Text: "  2>&1 "}, hubapi.AuditQuery{Server: "box", Text: "2>&1"}},
	} {
		if got := AuditQueryFor(tc.f); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%+v: %+v, want %+v", tc.f, got, tc.want)
		}
	}
	if got := ToggleChip([]string{"exec"}, "auto"); !slices.Equal(got, []string{"exec", "auto"}) {
		t.Errorf("on %q", got)
	}
	in := []string{"exec", "auto"}
	if got := ToggleChip(in, "exec"); !slices.Equal(got, []string{"auto"}) || !slices.Equal(in, []string{"exec", "auto"}) {
		t.Errorf("off %q, input now %q", got, in)
	}
}

func TestAuditMatches(t *testing.T) {
	auto := rec(t, `{"server":"box","command":"tail 2>&1","outcome":"allowed","approval":"auto"}`)
	denied := rec(t, `{"server":"vis","command":"rm x","outcome":"denied"}`)
	save := rec(t, `{"kind":"config","server":"vis","action":"save"}`)
	all := []AuditRecord{auto, denied, save}
	got := func(q hubapi.AuditQuery) []bool {
		var out []bool
		for _, r := range all {
			out = append(out, AuditMatches(r, q))
		}
		return out
	}
	for _, tc := range []struct {
		q    hubapi.AuditQuery
		want []bool
	}{
		{hubapi.AuditQuery{}, []bool{true, true, true}},
		{hubapi.AuditQuery{Outcomes: []string{"auto"}}, []bool{true, false, false}},
		{hubapi.AuditQuery{Kinds: []string{"config"}, Outcomes: []string{"denied"}}, []bool{false, true, true}},
		{hubapi.AuditQuery{Outcomes: []string{"allowed"}}, []bool{false, false, false}},
		{hubapi.AuditQuery{Server: "vis"}, []bool{false, true, true}},
		{hubapi.AuditQuery{Text: "TAIL 2>&1"}, []bool{true, false, false}},
	} {
		if g := got(tc.q); !slices.Equal(g, tc.want) {
			t.Errorf("%+v: %v, want %v", tc.q, g, tc.want)
		}
	}
	// Decoded values: quotes, backslashes and numbers match as shown; key names do not.
	q := rec(t, `{"server":"box","command":"echo \"hi\" C:\\Users","timeoutSec":30,"outcome":"allowed"}`)
	for text, want := range map[string]bool{`echo "hi"`: true, `C:\Users`: true, "30": true, "command": false, "server": false} {
		if AuditMatches(q, hubapi.AuditQuery{Text: text}) != want {
			t.Errorf("text %q: want %v", text, want)
		}
	}
	if !AuditMatches(rec(t, `{"command":"x","outcome":"cancelled_running"}`), hubapi.AuditQuery{Outcomes: []string{"cancelled"}}) {
		t.Error("cancelled_running is not cancelled")
	}
	soft := rec(t, `{"kind":"config","action":"softLock","servers":["box","db"]}`)
	if !AuditMatches(soft, hubapi.AuditQuery{Server: "box"}) || AuditMatches(soft, hubapi.AuditQuery{Server: "x"}) {
		t.Error("a softLock record is found by its servers")
	}
}

func TestAuditListPages(t *testing.T) {
	if got := seqs(MergeEntries([]AuditRow{entry(5, ""), entry(3, "")}, []AuditRow{entry(4, ""), entry(5, ""), entry(1, "")})); !slices.Equal(got, []int{5, 4, 3, 1}) {
		t.Fatalf("merge %v", got)
	}
	l := StartLoad(AuditList{Status: "loaded", Entries: []AuditRow{entry(1, "")}, Skipped: 3, Path: "/old", Error: "x"})
	if !reflect.DeepEqual(l, AuditList{Status: "loading", Path: "/old"}) {
		t.Fatalf("start %+v", l)
	}
	page := func(next, skipped int, s ...int) hubapi.AuditPage {
		p := hubapi.AuditPage{Next: next, Skipped: skipped, Path: "/a/audit.jsonl", Records: []hubapi.AuditEntry{}}
		for _, n := range s {
			p.Records = append(p.Records, hubapi.AuditEntry{Seq: n, Record: json.RawMessage(`{}`)})
		}
		return p
	}
	l = ApplyPage(l, page(8, 1, 9, 8))
	if l.Status != "loaded" || l.Next != 8 || l.Skipped != 1 || l.Path != "/a/audit.jsonl" {
		t.Fatalf("first page %+v", l)
	}
	l = ApplyPage(l, page(0, 1, 7))
	if !slices.Equal(seqs(l.Entries), []int{9, 8, 7}) || l.Skipped != 2 || l.Next != 0 {
		t.Fatalf("older page %+v", l)
	}
	f := FailLoad(l, "boom")
	if f.Status != "error" || f.Error != "boom" || !slices.Equal(seqs(f.Entries), []int{9, 8, 7}) {
		t.Fatalf("fail %+v", f)
	}
	if l = ApplyPage(f, page(0, 0, 9)); l.Error != "" || l.Status != "loaded" {
		t.Fatalf("a page after a failure %+v", l)
	}
}

func TestAuditAppendLive(t *testing.T) {
	l := loaded(entry(2, ""), entry(1, ""))
	if n, ok := AppendLive(l, entry(3, ""), hubapi.AuditQuery{}, true); !ok || !slices.Equal(seqs(n.Entries), []int{3, 2, 1}) {
		t.Fatalf("prepend %v %v", seqs(n.Entries), ok)
	}
	if _, ok := AppendLive(l, entry(2, ""), hubapi.AuditQuery{}, true); ok {
		t.Fatal("a seq already shown was added")
	}
	if _, ok := AppendLive(l, entry(3, `{"kind":"config"}`), hubapi.AuditQuery{Kinds: []string{"exec"}}, true); ok {
		t.Fatal("a record the query does not match was added")
	}

	// In order prepends; out of order merges; scrolled away holds.
	l = loaded(entry(5, ""), entry(3, ""))
	l, _ = AppendLive(l, entry(6, ""), hubapi.AuditQuery{}, true)
	l, _ = AppendLive(l, entry(4, ""), hubapi.AuditQuery{}, true)
	if !slices.Equal(seqs(l.Entries), []int{6, 5, 4, 3}) {
		t.Fatalf("in and out of order %v", seqs(l.Entries))
	}
	l, _ = AppendLive(l, entry(8, ""), hubapi.AuditQuery{}, false)
	l, _ = AppendLive(l, entry(7, ""), hubapi.AuditQuery{}, false) // below Held[0], above Entries[0]
	if !slices.Equal(seqs(l.Held), []int{8, 7}) {
		t.Fatalf("held %v", seqs(l.Held))
	}
	if _, ok := AppendLive(l, entry(8, ""), hubapi.AuditQuery{}, true); ok {
		t.Fatal("a held seq was added again")
	}

	// "N new" while scrolled away, then released.
	l = loaded(entry(1, ""))
	for _, s := range []int{2, 3, 3} {
		l, _ = AppendLive(l, entry(s, ""), hubapi.AuditQuery{}, false)
	}
	if !slices.Equal(seqs(l.Entries), []int{1}) || !slices.Equal(seqs(l.Held), []int{3, 2}) {
		t.Fatalf("held %v %v", seqs(l.Entries), seqs(l.Held))
	}
	l = ReleaseHeld(l)
	if !slices.Equal(seqs(l.Entries), []int{3, 2, 1}) || len(l.Held) != 0 {
		t.Fatalf("released %v %v", seqs(l.Entries), seqs(l.Held))
	}
	if r := ReleaseHeld(l); !reflect.DeepEqual(r, l) {
		t.Fatal("releasing nothing changed the list")
	}

	// A live record that lands while a read is in flight is kept once.
	l, _ = AppendLive(StartLoad(AuditList{}), entry(5, ""), hubapi.AuditQuery{}, true)
	l = ApplyPage(l, hubapi.AuditPage{Records: []hubapi.AuditEntry{{Seq: 5, Record: json.RawMessage(`{}`)}, {Seq: 4, Record: json.RawMessage(`{}`)}}, Path: "/p"})
	if !slices.Equal(seqs(l.Entries), []int{5, 4}) {
		t.Fatalf("in flight %v", seqs(l.Entries))
	}

	// Cleared on lock; idle and cleared ignore live records.
	c := ClearOnLock()
	if !reflect.DeepEqual(c, AuditList{Status: "cleared"}) {
		t.Fatalf("cleared %+v", c)
	}
	for _, l := range []AuditList{c, {Status: "idle"}, {Status: "error"}} {
		if _, ok := AppendLive(l, entry(9, ""), hubapi.AuditQuery{}, true); ok {
			t.Errorf("%s took a live record", l.Status)
		}
	}
}

func TestAuditStdinText(t *testing.T) {
	if got := AuditStdinText("func f() {\r\n\treturn\n}\n"); got != "func f() {\u240d\n\treturn\n}\n" {
		t.Errorf("%q", got)
	}
	if got := AuditStdinText("a\u200db\ufeff"); got != `a\u200db\ufeff` {
		t.Errorf("%q", got)
	}
}

func TestAuditDetail(t *testing.T) {
	r := rec(t, `{"reason":"denied by user","client":"e2e","timeoutSec":30,"durationMs":12,"redacted":{"token":1,"password":2}}`)
	if got := AuditFacts(r); !reflect.DeepEqual(got, []AuditFact{{"Reason", "denied by user"}, {"Client", "e2e"}, {"Timeout", "30 s"}, {"Duration", "12 ms"}}) {
		t.Errorf("facts %+v", got)
	}
	if got := AuditMasked(r); got != "Masked: password ×2, token ×1" {
		t.Errorf("masked %q", got)
	}
	if got := AuditFacts(rec(t, `{"reason":"","client":5,"timeoutSec":"30"}`)); !reflect.DeepEqual(got, []AuditFact{{"Client", "5"}}) {
		t.Errorf("wrong types %+v", got)
	}
	if got := AuditMasked(rec(t, `{"redacted":{"x":"y"}}`)); got != "" {
		t.Errorf("nothing masked %q", got)
	}
	if got := AuditRaw(json.RawMessage("{\"b\":1,\"a\":\"x\u202ey\"}")); got != "{\n  \"b\": 1,\n  \"a\": \"x\\u202ey\"\n}" {
		t.Errorf("raw %q", got)
	}
}

func TestAuditLines(t *testing.T) {
	loc := auditNow.Location()
	at := func(seq int, tm time.Time) AuditRow { return entry(seq, fmt.Sprintf(`{"time":%q}`, iso(tm))) }
	lines := AuditLines([]AuditRow{
		at(4, auditNow), at(3, auditNow.Add(-time.Hour)),
		at(2, time.Date(2026, 9, 29, 9, 0, 0, 0, loc)), at(1, time.Date(2026, 9, 1, 9, 0, 0, 0, loc)),
	}, auditNow)
	var got []string
	for _, l := range lines {
		if l.Heading != "" {
			got = append(got, l.Heading)
		} else {
			got = append(got, fmt.Sprint(l.Row.Seq))
		}
	}
	if want := []string{"Today", "4", "3", "Yesterday", "2", "Tue Sep 01 2026", "1"}; !slices.Equal(got, want) {
		t.Fatalf("%q, want %q", got, want)
	}
	if AuditLines(nil, auditNow) != nil {
		t.Fatal("no entries, no lines")
	}
}
