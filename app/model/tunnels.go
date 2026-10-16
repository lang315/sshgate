package model

import (
	"net"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/lang315/sshgate/app/hubapi"
)

// TunnelForm is the Add or Edit tunnel dialog's fields as typed
// (desktop/src/renderer/tunnels.ts).
type TunnelForm struct {
	Kind, ListenPort, TargetHost, TargetPort, Label string
}

// TunnelSummary is a tunnel's forward in one line: "L 127.0.0.1:5433 →
// db:5432", "R server 127.0.0.1:8080 → localhost:3000" or "D
// 127.0.0.1:1080 SOCKS5". An IPv6 target is bracketed.
func TunnelSummary(t hubapi.Tunnel) string {
	listen := "127.0.0.1:" + strconv.Itoa(t.ListenPort)
	to := net.JoinHostPort(t.TargetHost, strconv.Itoa(t.TargetPort))
	switch t.Kind {
	case "local":
		return "L " + listen + " → " + to
	case "remote":
		return "R server " + listen + " → " + to
	default:
		return "D " + listen + " SOCKS5"
	}
}

// tunnelPort is s as a port, or 0 when it is not 1-65535.
func tunnelPort(s string) int {
	s = strings.TrimSpace(s)
	if len(s) < 1 || len(s) > 5 || strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		return 0
	}
	n, _ := strconv.Atoi(s)
	if n > 65535 {
		return 0
	}
	return n
}

func controlOrFormat(r rune) bool { return unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) }

// ParseTunnelForm checks the dialog's fields as config.Tunnel.Validate does
// (the hub checks again) and gives the tunnel to save under id, or the
// first error to show.
func ParseTunnelForm(f TunnelForm, id string) (hubapi.Tunnel, string) {
	listen := tunnelPort(f.ListenPort)
	if listen == 0 {
		return hubapi.Tunnel{}, "Listen port must be 1-65535"
	}
	label := strings.TrimSpace(f.Label)
	if len(label) > 64 || strings.ContainsFunc(label, controlOrFormat) {
		return hubapi.Tunnel{}, "Label is at most 64 bytes, with no control characters"
	}
	if f.Kind == "dynamic" {
		return hubapi.Tunnel{ID: id, Kind: "dynamic", ListenPort: listen, Label: label}, ""
	}
	host := strings.TrimSpace(f.TargetHost)
	if host == "" || len(host) > 253 || strings.ContainsFunc(host, func(r rune) bool { return unicode.IsSpace(r) || controlOrFormat(r) }) {
		return hubapi.Tunnel{}, "Target host is required, with no spaces or control characters"
	}
	target := tunnelPort(f.TargetPort)
	if target == 0 {
		return hubapi.Tunnel{}, "Target port must be 1-65535"
	}
	return hubapi.Tunnel{ID: id, Kind: f.Kind, ListenPort: listen, TargetHost: host, TargetPort: target, Label: label}, ""
}

// FormFor fills the dialog: t's fields, or a new local tunnel to localhost.
func FormFor(t *hubapi.Tunnel) TunnelForm {
	if t == nil {
		return TunnelForm{Kind: "local", TargetHost: "localhost"}
	}
	f := TunnelForm{Kind: t.Kind, ListenPort: strconv.Itoa(t.ListenPort), TargetHost: t.TargetHost, Label: t.Label}
	if t.TargetPort != 0 {
		f.TargetPort = strconv.Itoa(t.TargetPort)
	}
	return f
}

// TunnelLive is a tunnel that runs or is starting: it can be stopped, not
// edited or deleted.
func TunnelLive(status string) bool { return status == "running" || status == "starting" }

// ApplyTunnelState sets the matching row's status, error and conns (an
// error from before goes); a state for a tunnel not listed changes nothing.
func ApplyTunnelState(list []hubapi.TunnelView, s hubapi.TunnelState) []hubapi.TunnelView {
	i := slices.IndexFunc(list, func(t hubapi.TunnelView) bool { return t.Server == s.Server && t.ID == s.ID })
	if i < 0 {
		return list
	}
	next := slices.Clone(list)
	next[i].Status, next[i].Error, next[i].Conns = s.Status, s.Error, s.Conns
	return next
}

// ReplayTunnelStates applies the states that arrived while a tunnels.list
// was in flight to its reply, in order, so an older snapshot never
// overwrites a newer status.
func ReplayTunnelStates(list []hubapi.TunnelView, events []hubapi.TunnelState) []hubapi.TunnelView {
	for _, e := range events {
		list = ApplyTunnelState(list, e)
	}
	return list
}

// RunningTunnels counts server's running tunnels: the host card's badge.
func RunningTunnels(list []hubapi.TunnelView, server string) int {
	n := 0
	for _, t := range list {
		if t.Server == server && t.Status == "running" {
			n++
		}
	}
	return n
}

// RemoteTunnels are the summaries of server's running remote tunnels, which
// the Auto-allow dialog warns about: they reach this machine from the
// server.
func RemoteTunnels(list []hubapi.TunnelView, server string) []string {
	var out []string
	for _, t := range list {
		if t.Server == server && t.Kind == "remote" && t.Status == "running" {
			out = append(out, TunnelSummary(t.Tunnel))
		}
	}
	return out
}
