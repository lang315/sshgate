package model

import (
	"reflect"
	"regexp"
	"testing"
)

func TestTabIDsFollowTheHubRule(t *testing.T) {
	var s TabSet
	a, b := s.Open("box"), s.Open("box")
	rule := regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	if !rule.MatchString(a) || !rule.MatchString(b) || a == b {
		t.Fatalf("ids %q %q", a, b)
	}
}

func TestTabSetCountsOpenTabsPerServer(t *testing.T) {
	var s TabSet
	a := s.Open("box")
	s.Open("box")
	s.Open("other")
	s.Exited(a, "bye")
	if s.OpenCount("box") != 1 || s.OpenCount("none") != 0 {
		t.Fatal(s.OpenCount("box"), s.OpenCount("none"))
	}
}

func TestTabSetLifecycleAndActivationOrder(t *testing.T) {
	var s TabSet
	a, b, c := s.Open("box"), s.Open("box"), s.Open("other")
	if s.Active != c {
		t.Fatal(s.Active)
	}
	s.Activate(a)
	if s.MostRecentFor("box") != a {
		t.Fatal(s.MostRecentFor("box"))
	}
	s.Opened(a)
	s.Output(a)
	if got, _ := s.Get(a); got.State != "open" || !got.SawOutput {
		t.Fatalf("%+v", got)
	}
	s.Exited(a, "bye")
	if s.MostRecentFor("box") != b {
		t.Fatal("an exited tab is not a Send to tab target")
	}
	s.Activate(c)
	s.Close(c)
	ids := []string{}
	for _, x := range s.Tabs {
		ids = append(ids, x.ID)
	}
	if !reflect.DeepEqual(ids, []string{a, b}) || s.Active != a {
		t.Fatalf("tabs %v active %q: want the previous in activation order", ids, s.Active)
	}
	s.Opened(a) // exited stays exited
	if got, _ := s.Get(a); got.State != "exited" || got.ExitReason != "bye" {
		t.Fatalf("%+v", got)
	}
}

func TestTabSetHostsHome(t *testing.T) {
	var s TabSet
	a := s.Open("box")
	if s.Active != a {
		t.Fatal(s.Active)
	}
	s.ShowHome()
	if s.Active != "" {
		t.Fatal(s.Active)
	}
	s.Activate(a)
	s.Close(a)
	if s.Active != "" {
		t.Fatal("closing the last tab must show the Hosts home:", s.Active)
	}
}

func TestTabSetAuditIsNeverInTheActivationOrder(t *testing.T) {
	var s TabSet
	a := s.Open("box")
	s.ShowAudit()
	if s.Active != AuditTab {
		t.Fatal(s.Active)
	}
	s.Close(a)
	if s.Active != AuditTab {
		t.Fatal("closing a tab behind Audit left Audit:", s.Active)
	}
	b := s.Open("box")
	s.ShowAudit()
	s.Activate(b)
	s.Close(b)
	if s.Active != "" {
		t.Fatal("want the Hosts home, not Audit:", s.Active)
	}
}

func TestTabSetClosingAnotherTabKeepsActive(t *testing.T) {
	var s TabSet
	a, b := s.Open("box"), s.Open("db")
	s.Close(a)
	if s.Active != b {
		t.Fatal(s.Active)
	}
	if _, ok := s.Get(a); ok {
		t.Fatal("closed tab still there")
	}
}

// A Files tab is open at once, never a Send to tab target, and not in the
// editor's open-tabs count (Electron filters kind === 'term').
func TestFilesTabsAreNotTerminals(t *testing.T) {
	var s TabSet
	term := s.Open("box")
	f := s.OpenFiles("box")
	g := s.OpenFiles("box")
	if f == g || s.Active != g {
		t.Fatalf("ids %q %q, active %q", f, g, s.Active)
	}
	if tab, _ := s.Get(f); tab.Kind != FilesKind || tab.State != "open" {
		t.Fatalf("%+v", tab)
	}
	if s.MostRecentFor("box") != term || s.OpenCount("box") != 1 {
		t.Fatal(s.MostRecentFor("box"), s.OpenCount("box"))
	}
	s.Close(term)
	if s.MostRecentFor("box") != "" || s.OpenCount("box") != 0 {
		t.Fatal(s.MostRecentFor("box"), s.OpenCount("box"))
	}
}

// terminals.test.ts "finds a tab by server and kind": one Tunnels tab per
// host, open at once; like a Files tab, never a Send to tab target nor in
// the editor's open-tabs count.
func TestTunnelsTabOnePerHost(t *testing.T) {
	var s TabSet
	term := s.Open("x")
	a := s.OpenTunnels("x")
	s.Open("x")
	if got := s.OpenTunnels("x"); got != a || s.Active != a || len(s.Tabs) != 3 {
		t.Fatalf("again: %q active %q, %d tabs", got, s.Active, len(s.Tabs))
	}
	if b := s.OpenTunnels("y"); b == a || len(s.Tabs) != 4 {
		t.Fatalf("another host shared %q", b)
	}
	if tab, _ := s.Get(a); tab.Kind != TunnelsKind || tab.State != "open" {
		t.Fatalf("%+v", tab)
	}
	s.Exited(term, "bye")
	if s.MostRecentFor("x") == a || s.OpenCount("x") != 1 {
		t.Fatal(s.MostRecentFor("x"), s.OpenCount("x"))
	}
	s.Close(a)
	if c := s.OpenTunnels("x"); c == a {
		t.Fatal("a closed tab's id came back")
	}
}
