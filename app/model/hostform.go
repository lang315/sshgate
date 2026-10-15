package model

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lang315/sshgate/app/hubapi"
)

// Secret is one of a server's four write-only secrets, in the editor's
// order (desktop/src/renderer/hostForm.ts SECRET_FIELDS).
type Secret int

const (
	SecretPassword Secret = iota
	SecretSu
	SecretSudo
	SecretKeyPassphrase
)

func (f Secret) Label() string {
	return [...]string{"Password", "su password", "sudo password", "Key passphrase"}[f]
}

// Saved reports whether s stores this secret; s is nil for a new host.
func (f Secret) Saved(s *hubapi.Server) bool {
	return s != nil && [...]bool{s.HasPassword, s.HasSuPassword, s.HasSudoPassword, s.HasKeyPassphrase}[f]
}

// SecretEdit is what the person did to one secret: typed Value, or Cleared.
type SecretEdit struct {
	Value   string
	Cleared bool
}

// HostDraft is the host editor's form. Port stays text until it is saved;
// Secrets is indexed by Secret and never holds a stored value.
type HostDraft struct {
	Name, Host, Port, User, Auth, KeyPath string
	AIVisible                             bool
	AutoAllow                             string // "forever" for a forever host, paused too; else "off"
	AutoAllowRoot, AutoAllowSudo          bool
	Secrets                               [4]SecretEdit
}

// DraftFrom is the form for s, or for a new host when s is nil.
func DraftFrom(s *hubapi.Server) HostDraft {
	if s == nil {
		return HostDraft{Port: "22", Auth: "password", AutoAllow: "off"}
	}
	mode := "off"
	if s.AutoAllow != nil && s.AutoAllow.Forever {
		mode = "forever"
	}
	return HostDraft{Name: s.Name, Host: s.Host, Port: strconv.Itoa(s.Port), User: s.User, Auth: s.Auth, KeyPath: s.KeyPath,
		AIVisible: s.AIVisible, AutoAllow: mode, AutoAllowRoot: s.AutoAllowRoot, AutoAllowSudo: s.AutoAllowSudo}
}

// draftPort is Electron's Number(port): 0 for text that is not a number,
// which the hub refuses.
func draftPort(p string) int {
	n, err := strconv.Atoi(strings.TrimSpace(p))
	if err != nil {
		return 0
	}
	return n
}

// PortError is the port field's message, "" when the port can be saved.
func PortError(p string) string {
	if n := draftPort(p); n < 1 || n > 65535 {
		return "Port must be 1-65535."
	}
	return ""
}

// ToInput is servers.save's server: the label defaults to the address, the
// key path is sent only for key auth, and a secret is omitted (kept) when
// untouched, "" when cleared, and its value when typed.
func ToInput(d HostDraft) hubapi.ServerInput {
	in := hubapi.ServerInput{Name: strings.TrimSpace(d.Name), Host: strings.TrimSpace(d.Host), Port: draftPort(d.Port), User: strings.TrimSpace(d.User),
		Auth: d.Auth, AIVisible: d.AIVisible, AutoAllowRoot: d.AutoAllowRoot, AutoAllowSudo: d.AutoAllowSudo}
	if in.Name == "" {
		in.Name = in.Host
	}
	if d.Auth == "key" {
		in.KeyPath = strings.TrimSpace(d.KeyPath)
	}
	out := [...]**string{&in.Password, &in.SuPassword, &in.SudoPassword, &in.KeyPassphrase}
	for i, e := range d.Secrets {
		switch {
		case e.Cleared:
			v := ""
			*out[i] = &v
		case e.Value != "":
			v := e.Value
			*out[i] = &v
		}
	}
	return in
}

// LabelHint asks for a label when the address cannot be a name: the hub's
// name rule (A-Z a-z 0-9 . _ -) rejects an IPv6 address.
func LabelHint(d HostDraft) string {
	if strings.TrimSpace(d.Name) == "" && strings.Contains(d.Host, ":") {
		return "An IPv6 address cannot be a name: add a label."
	}
	return ""
}

// SecretHint says what a save does to a secret: "will be cleared" for a
// cleared one, or a saved one the hub drops because the endpoint changed;
// "saved" for a kept one; "" when nothing is stored.
func SecretHint(saved bool, e SecretEdit, endpointChanged bool) string {
	switch {
	case e.Cleared:
		return "will be cleared"
	case !saved:
		return ""
	case endpointChanged && e.Value == "":
		return "will be cleared"
	}
	return "saved"
}

// EndpointChanged: a new host or port drops the pin and every secret not
// typed again.
func EndpointChanged(s *hubapi.Server, d HostDraft) bool {
	return s != nil && (strings.TrimSpace(d.Host) != s.Host || draftPort(d.Port) != s.Port)
}

// ClosesTabs mirrors the hub: a save closes the server's connection when
// anything but Visible to AI and the auto-allow fields changed.
func ClosesTabs(s *hubapi.Server, d HostDraft) bool {
	if s == nil {
		return false
	}
	in := ToInput(d)
	return in.Name != s.Name || in.Host != s.Host || in.Port != s.Port || in.User != s.User || in.Auth != s.Auth || in.KeyPath != s.KeyPath ||
		in.Password != nil || in.SuPassword != nil || in.SudoPassword != nil || in.KeyPassphrase != nil
}

func CloseWarning(openTabs, transfers int) string {
	tabs := fmt.Sprintf("close %d open tab%s", openTabs, plural(openTabs))
	jobs := fmt.Sprintf("cancel %d transfer%s", transfers, plural(transfers))
	switch {
	case openTabs > 0 && transfers > 0:
		return "Saving will " + tabs + " and " + jobs + "."
	case openTabs > 0:
		return "Saving will " + tabs + "."
	}
	return "Saving will " + jobs + "."
}

// EditorWarnings are the lines of the editor's one warning box, none when
// the save moves nothing (Electron's EditorWarnings).
func EditorWarnings(s *hubapi.Server, d HostDraft, openTabs, transfers int) []string {
	var w []string
	if EndpointChanged(s, d) {
		w = append(w, "Changing host or port forgets the host key and saved passwords unless you re-enter them.")
	}
	if (openTabs > 0 || transfers > 0) && ClosesTabs(s, d) {
		w = append(w, CloseWarning(openTabs, transfers))
	}
	return w
}

// NextChecked is the import sheet's checked aliases: every ready row on the
// first scan (prev nil); on a rescan, only rows still ready that were checked.
func NextChecked(cs []hubapi.Candidate, prev map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, c := range cs {
		if c.Status == "ready" && (prev == nil || prev[c.Alias]) {
			out[c.Alias] = true
		}
	}
	return out
}
