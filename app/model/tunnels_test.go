package model

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lang315/sshgate/app/hubapi"
)

// The cases of desktop/test/tunnels.test.ts.
func TestTunnelSummary(t *testing.T) {
	for _, tc := range []struct {
		t    hubapi.Tunnel
		want string
	}{
		{hubapi.Tunnel{Kind: "local", ListenPort: 5433, TargetHost: "db", TargetPort: 5432}, "L 127.0.0.1:5433 → db:5432"},
		{hubapi.Tunnel{Kind: "remote", ListenPort: 8080, TargetHost: "localhost", TargetPort: 3000}, "R server 127.0.0.1:8080 → localhost:3000"},
		{hubapi.Tunnel{Kind: "dynamic", ListenPort: 1080}, "D 127.0.0.1:1080 SOCKS5"},
		{hubapi.Tunnel{Kind: "local", ListenPort: 1, TargetHost: "::1", TargetPort: 2}, "L 127.0.0.1:1 → [::1]:2"},
	} {
		if got := TunnelSummary(tc.t); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.t, got, tc.want)
		}
	}
}

func TestParseTunnelForm(t *testing.T) {
	base := TunnelForm{Kind: "local", ListenPort: "5433", TargetHost: "db", TargetPort: "5432"}
	f := base
	f.TargetHost, f.Label = " db ", " pg "
	if got, err := ParseTunnelForm(f, ""); err != "" || got != (hubapi.Tunnel{Kind: "local", ListenPort: 5433, TargetHost: "db", TargetPort: 5432, Label: "pg"}) {
		t.Fatalf("trims: %+v %q", got, err)
	}
	f = base
	f.Kind = "dynamic"
	if got, err := ParseTunnelForm(f, "x"); err != "" || got != (hubapi.Tunnel{ID: "x", Kind: "dynamic", ListenPort: 5433}) {
		t.Fatalf("dynamic drops the target: %+v %q", got, err)
	}
	const (
		listen = "Listen port must be 1-65535"
		host   = "Target host is required, with no spaces or control characters"
		target = "Target port must be 1-65535"
		label  = "Label is at most 64 bytes, with no control characters"
	)
	for _, tc := range []struct {
		patch func(*TunnelForm)
		want  string
	}{
		{func(f *TunnelForm) { f.ListenPort = "0" }, listen},
		{func(f *TunnelForm) { f.ListenPort = "5x" }, listen},
		{func(f *TunnelForm) { f.ListenPort = "123456" }, listen},
		{func(f *TunnelForm) { f.TargetHost = "" }, host},
		{func(f *TunnelForm) { f.TargetHost = "a b" }, host},
		{func(f *TunnelForm) { f.TargetHost = "db\u0007" }, host},
		{func(f *TunnelForm) { f.TargetHost = "d\u200bb" }, host},
		{func(f *TunnelForm) { f.TargetHost = strings.Repeat("a", 254) }, host},
		{func(f *TunnelForm) { f.TargetPort = "70000" }, target},
		{func(f *TunnelForm) { f.Label = strings.Repeat("x", 65) }, label},
		{func(f *TunnelForm) { f.Label = "\u0007test" }, label},
		{func(f *TunnelForm) { f.Label = "test\u200bend" }, label},
		{func(f *TunnelForm) { f.Label = strings.Repeat("é", 33) }, label}, // 66 bytes
	} {
		f := base
		tc.patch(&f)
		if got, err := ParseTunnelForm(f, ""); err != tc.want {
			t.Errorf("%+v: %+v %q, want %q", f, got, err, tc.want)
		}
	}
	f = base
	f.Label = strings.Repeat("é", 22) // 44 bytes
	if got, err := ParseTunnelForm(f, ""); err != "" || got.Label != f.Label {
		t.Fatalf("22 é: %+v %q", got, err)
	}
}

func TestFormFor(t *testing.T) {
	if got := FormFor(&hubapi.Tunnel{ID: "i", Kind: "remote", ListenPort: 1, TargetHost: "h", TargetPort: 2, Label: "l"}); got != (TunnelForm{Kind: "remote", ListenPort: "1", TargetHost: "h", TargetPort: "2", Label: "l"}) {
		t.Fatalf("%+v", got)
	}
	if got := FormFor(&hubapi.Tunnel{Kind: "dynamic", ListenPort: 1080}); got != (TunnelForm{Kind: "dynamic", ListenPort: "1080"}) {
		t.Fatalf("%+v", got)
	}
	if got := FormFor(nil); got != (TunnelForm{Kind: "local", TargetHost: "localhost"}) {
		t.Fatalf("%+v", got)
	}
}

func dynView(server, id string, port int, status string, conns int) hubapi.TunnelView {
	return hubapi.TunnelView{Server: server, Tunnel: hubapi.Tunnel{ID: id, Kind: "dynamic", ListenPort: port}, Status: status, Conns: conns}
}

func TestTunnelStates(t *testing.T) {
	list := []hubapi.TunnelView{dynView("s", "a", 1, "stopped", 0), dynView("s", "b", 2, "running", 1), dynView("t", "a", 3, "running", 0)}
	before := slices.Clone(list)
	next := ApplyTunnelState(list, hubapi.TunnelState{Server: "s", ID: "a", Status: "error", Error: "port 1 is already in use"})
	if next[0].Status != "error" || next[0].Error != "port 1 is already in use" || next[2] != list[2] || !reflect.DeepEqual(list, before) {
		t.Fatalf("applied %+v; list %+v", next, list)
	}
	if got := ApplyTunnelState(list, hubapi.TunnelState{Server: "x", ID: "a", Status: "running"}); !reflect.DeepEqual(got, list) {
		t.Fatal("a state for another server changed the list")
	}
	if got := ApplyTunnelState(next, hubapi.TunnelState{Server: "s", ID: "a", Status: "running"}); got[0].Error != "" {
		t.Fatal("a new status kept the old error")
	}
	if RunningTunnels(list, "s") != 1 || RunningTunnels(list, "u") != 0 {
		t.Fatal(RunningTunnels(list, "s"), RunningTunnels(list, "u"))
	}
	snap := []hubapi.TunnelView{dynView("s", "a", 1, "stopped", 0)}
	got := ReplayTunnelStates(snap, []hubapi.TunnelState{{Server: "s", ID: "a", Status: "running"}, {Server: "s", ID: "a", Status: "running", Conns: 2}})
	if got[0].Status != "running" || got[0].Conns != 2 {
		t.Fatalf("newest per tunnel wins: %+v", got)
	}
	if got := ReplayTunnelStates(snap, []hubapi.TunnelState{{Server: "x", ID: "a", Status: "running"}}); !reflect.DeepEqual(got, snap) {
		t.Fatal(got)
	}
	if got := ReplayTunnelStates(snap, nil); !reflect.DeepEqual(got, snap) {
		t.Fatal(got)
	}
}

// App.tsx:314: what the Auto-allow dialog lists, the server's running
// remote tunnels only.
func TestRemoteTunnels(t *testing.T) {
	r := func(server, id, status string, port int) hubapi.TunnelView {
		return hubapi.TunnelView{Server: server, Tunnel: hubapi.Tunnel{ID: id, Kind: "remote", ListenPort: port, TargetHost: "localhost", TargetPort: 22}, Status: status}
	}
	list := []hubapi.TunnelView{r("box", "a", "running", 2222), r("box", "b", "stopped", 3333), r("db", "c", "running", 4444), dynView("box", "d", 1080, "running", 0), r("box", "e", "running", 5555)}
	if got := RemoteTunnels(list, "box"); !reflect.DeepEqual(got, []string{"R server 127.0.0.1:2222 → localhost:22", "R server 127.0.0.1:5555 → localhost:22"}) {
		t.Fatalf("%q", got)
	}
	if got := RemoteTunnels(list, "none"); got != nil {
		t.Fatalf("%q", got)
	}
}
