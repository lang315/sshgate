package model

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lang315/sshgate/app/hubapi"
)

// box is hostForm.test.ts's fixture.
var box = hubapi.Server{Name: "box", Host: "h", Port: 22, User: "u", Auth: "password", HostKey: "SHA256:x", HostKeyAlgo: "ssh-ed25519", HasPassword: true}

func TestToInputSendsOnlyTouchedSecrets(t *testing.T) {
	d := DraftFrom(&box)
	d.Secrets[SecretPassword] = SecretEdit{Cleared: true}
	d.Secrets[SecretSu] = SecretEdit{Value: "su!"}
	b, _ := json.Marshal(ToInput(d))
	want := `{"name":"box","host":"h","port":22,"user":"u","auth":"password","keyPath":"","aiVisible":false,"autoAllowRoot":false,"autoAllowSudo":false,"password":"","suPassword":"su!"}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

func TestSecretHint(t *testing.T) {
	for _, tc := range []struct {
		saved, moved bool
		e            SecretEdit
		want         string
	}{
		{true, false, SecretEdit{}, "saved"},
		{false, false, SecretEdit{}, ""},
		{true, false, SecretEdit{Cleared: true}, "will be cleared"},
		{true, true, SecretEdit{}, "will be cleared"},
		{false, true, SecretEdit{}, ""},
		{true, true, SecretEdit{Value: "new"}, "saved"},
	} {
		if got := SecretHint(tc.saved, tc.e, tc.moved); got != tc.want {
			t.Errorf("SecretHint(%v, %+v, %v) = %q, want %q", tc.saved, tc.e, tc.moved, got, tc.want)
		}
	}
}

func TestSecretsSavedAndLabels(t *testing.T) {
	s := hubapi.Server{HasSudoPassword: true, HasKeyPassphrase: true}
	var got []string
	for f := SecretPassword; f <= SecretKeyPassphrase; f++ {
		if f.Saved(&s) {
			got = append(got, f.Label())
		}
		if f.Saved(nil) {
			t.Fatal("a new host has no saved secret")
		}
	}
	if !reflect.DeepEqual(got, []string{"sudo password", "Key passphrase"}) {
		t.Fatal(got)
	}
}

func TestDraftFromAutoAllowAndOptIns(t *testing.T) {
	with := func(a *hubapi.AutoAllow) *hubapi.Server { s := box; s.AutoAllow = a; return &s }
	for _, tc := range []struct {
		s    *hubapi.Server
		want string
	}{
		{with(&hubapi.AutoAllow{Forever: true}), "forever"},
		{with(&hubapi.AutoAllow{Forever: true, Paused: true}), "forever"},
		{with(&hubapi.AutoAllow{Until: "2026-01-01T00:00:00Z"}), "off"},
		{&box, "off"},
		{nil, "off"},
	} {
		if got := DraftFrom(tc.s).AutoAllow; got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.s, got, tc.want)
		}
	}
	s := box
	s.AutoAllowRoot, s.AutoAllowSudo = true, true
	if d := DraftFrom(&s); !d.AutoAllowRoot || !d.AutoAllowSudo {
		t.Fatalf("%+v", d)
	}
	if d := DraftFrom(nil); d.AutoAllowRoot || d.AutoAllowSudo || d.Port != "22" || d.Auth != "password" || d.AIVisible {
		t.Fatalf("new host %+v", d)
	}
	d := DraftFrom(&box)
	d.AutoAllowRoot, d.AutoAllowSudo = true, true
	if in := ToInput(d); !in.AutoAllowRoot || !in.AutoAllowSudo {
		t.Fatalf("%+v", in)
	}
}

func TestLabelDefaultsToTheAddress(t *testing.T) {
	d := DraftFrom(nil)
	d.Host, d.User = " 10.0.4.21 ", "u"
	if n := ToInput(d).Name; n != "10.0.4.21" {
		t.Fatal(n)
	}
	d.Name = " web "
	if n := ToInput(d).Name; n != "web" {
		t.Fatal(n)
	}
	for _, tc := range []struct{ name, host, want string }{
		{"", "db.example.com", ""},
		{"", "2001:db8::1", "An IPv6 address cannot be a name: add a label."},
		{"v6box", "2001:db8::1", ""},
	} {
		d := DraftFrom(nil)
		d.Name, d.Host = tc.name, tc.host
		if got := LabelHint(d); got != tc.want {
			t.Errorf("%q %q: %q", tc.name, tc.host, got)
		}
	}
}

func TestKeyPathOnlyForKeyAuthAndPortText(t *testing.T) {
	d := DraftFrom(&box)
	d.KeyPath = " ~/.ssh/id "
	if in := ToInput(d); in.KeyPath != "" {
		t.Fatal("key path sent for password auth:", in.KeyPath)
	}
	d.Auth = "key"
	if in := ToInput(d); in.KeyPath != "~/.ssh/id" {
		t.Fatal(in.KeyPath)
	}
	for p, want := range map[string]string{"22": "", " 2222 ": "", "65535": "", "0": "Port must be 1-65535.", "65536": "Port must be 1-65535.", "": "Port must be 1-65535.", "22a": "Port must be 1-65535."} {
		if got := PortError(p); got != want {
			t.Errorf("PortError(%q) = %q", p, got)
		}
	}
}

func TestEndpointChangedAndWarnings(t *testing.T) {
	d := DraftFrom(&box)
	user, port, host := d, d, d
	user.User, port.Port, host.Host = "root", "2222", "h2"
	if EndpointChanged(&box, d) || EndpointChanged(&box, user) || !EndpointChanged(&box, port) || !EndpointChanged(&box, host) || EndpointChanged(nil, d) {
		t.Fatal("endpointChanged rows")
	}
	if got := EditorWarnings(&box, port, 2, 0); !reflect.DeepEqual(got, []string{"Changing host or port forgets the host key and saved passwords unless you re-enter them.", "Saving will close 2 open tabs."}) {
		t.Fatalf("%q", got)
	}
	if got := EditorWarnings(&box, port, 1, 0); len(got) != 2 || got[1] != "Saving will close 1 open tab." {
		t.Fatalf("%q", got)
	}
}

func TestClosesTabsOnlyForConnectionChanges(t *testing.T) {
	d := DraftFrom(&box)
	vis, user, sudo := d, d, d
	vis.AIVisible, user.User = true, "root"
	sudo.Secrets[SecretSudo] = SecretEdit{Value: "x"}
	if ClosesTabs(&box, vis) || !ClosesTabs(&box, user) || !ClosesTabs(&box, sudo) || ClosesTabs(nil, user) {
		t.Fatal("closesTabs rows")
	}
	if got := EditorWarnings(&box, vis, 2, 0); len(got) != 0 {
		t.Fatalf("Visible to AI alone warns: %q", got)
	}
}

func TestCloseWarning(t *testing.T) {
	for _, tc := range []struct {
		tabs, jobs int
		want       string
	}{
		{2, 0, "Saving will close 2 open tabs."},
		{1, 1, "Saving will close 1 open tab and cancel 1 transfer."},
		{0, 3, "Saving will cancel 3 transfers."},
	} {
		if got := CloseWarning(tc.tabs, tc.jobs); got != tc.want {
			t.Errorf("%d %d: %q", tc.tabs, tc.jobs, got)
		}
	}
}

func TestNextChecked(t *testing.T) {
	cs := []hubapi.Candidate{{Alias: "web", Status: "ready"}, {Alias: "db", Status: "ready"}, {Alias: "jump", Status: "skipped"}}
	if got := NextChecked(cs, nil); !reflect.DeepEqual(got, map[string]bool{"web": true, "db": true}) {
		t.Fatal(got)
	}
	if got := NextChecked(cs, map[string]bool{"db": true, "jump": true, "gone": true}); !reflect.DeepEqual(got, map[string]bool{"db": true}) {
		t.Fatal(got)
	}
}
