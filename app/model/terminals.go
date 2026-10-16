package model

import (
	"fmt"
	"slices"
)

// AuditTab is TabSet.Active while the fixed Audit tab shows (slice 6).
const AuditTab = "audit"

// Tab is one terminal tab (desktop/src/renderer/terminals.ts). Files and
// tunnels tabs, and their kind, come with slices 4 and 5.
type Tab struct {
	ID, Server string
	State      string // "opening", "open" or "exited"
	ExitReason string
	SawOutput  bool // the remote printed something: Send to tab may paste
}

// TabSet is the tab strip's state: Tabs in open order, Active ("" is the
// Hosts home), and the activation order closing a tab falls back on.
type TabSet struct {
	Tabs   []Tab
	Active string
	order  []string
	next   int
}

// Open adds a terminal tab at the end and activates it. Ids are "t1",
// "t2", …: within the hub's id rule, and never reused while the app runs.
func (s *TabSet) Open(server string) string {
	s.next++
	id := fmt.Sprintf("t%d", s.next)
	s.Tabs = append(s.Tabs, Tab{ID: id, Server: server, State: "opening"})
	s.Activate(id)
	return id
}

func (s *TabSet) find(id string) *Tab {
	for i := range s.Tabs {
		if s.Tabs[i].ID == id {
			return &s.Tabs[i]
		}
	}
	return nil
}

// Get returns the tab id, if it is open in the strip.
func (s *TabSet) Get(id string) (Tab, bool) {
	if t := s.find(id); t != nil {
		return *t, true
	}
	return Tab{}, false
}

// Opened moves an opening tab to open; any other state stays.
func (s *TabSet) Opened(id string) {
	if t := s.find(id); t != nil && t.State == "opening" {
		t.State = "open"
	}
}

func (s *TabSet) Output(id string) {
	if t := s.find(id); t != nil {
		t.SawOutput = true
	}
}

// Exited ends a tab; it stays in the strip with its reason.
func (s *TabSet) Exited(id, reason string) {
	if t := s.find(id); t != nil {
		t.State, t.ExitReason = "exited", reason
	}
}

func (s *TabSet) Activate(id string) {
	s.Active = id
	s.order = append(slices.DeleteFunc(s.order, func(o string) bool { return o == id }), id)
}

func (s *TabSet) ShowHome()  { s.Active = "" }
func (s *TabSet) ShowAudit() { s.Active = AuditTab }

// Close removes a tab. Only when it was the active one does the previous
// tab in activation order (or the Hosts home) become active.
func (s *TabSet) Close(id string) {
	s.Tabs = slices.DeleteFunc(s.Tabs, func(t Tab) bool { return t.ID == id })
	s.order = slices.DeleteFunc(s.order, func(o string) bool { return o == id })
	if s.Active == id {
		s.Active = ""
		if n := len(s.order); n > 0 {
			s.Active = s.order[n-1]
		}
	}
}

// MostRecentFor is the server's most recently activated tab that has not
// exited (an opening one counts), or "".
func (s *TabSet) MostRecentFor(server string) string {
	for i := len(s.order) - 1; i >= 0; i-- {
		if t := s.find(s.order[i]); t != nil && t.Server == server && t.State != "exited" {
			return t.ID
		}
	}
	return ""
}

// OpenCount is how many of the server's tabs have not exited.
func (s *TabSet) OpenCount(server string) int {
	n := 0
	for _, t := range s.Tabs {
		if t.Server == server && t.State != "exited" {
			n++
		}
	}
	return n
}
