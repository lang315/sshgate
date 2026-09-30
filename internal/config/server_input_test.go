package config

import (
	"reflect"
	"strings"
	"testing"
)

func ptr(s string) *string { return &s }

func withSecrets(in ServerInput, pw, su, sudo, kp *string) ServerInput {
	in.Password, in.SuPassword, in.SudoPassword, in.KeyPassphrase = pw, su, sudo, kp
	return in
}

var secretFields = [4]string{"encPassword", "encSuPassword", "encSudoPassword", "encKeyPassphrase"}

// plain decrypts s's four secrets ("" when unset).
func plain(t *testing.T, f *File, s Server, mk []byte) [4]string {
	t.Helper()
	var out [4]string
	for i, blob := range [4]string{s.EncPassword, s.EncSuPassword, s.EncSudoPassword, s.EncKeyPassphrase} {
		if blob == "" {
			continue
		}
		pt, err := Decrypt(mk, s.Name+"/"+secretFields[i], aadFor(f, s, secretFields[i]), blob)
		if err != nil {
			t.Fatalf("%s: %v", secretFields[i], err)
		}
		out[i] = pt
	}
	return out
}

func TestServerInputValidate(t *testing.T) {
	ok := ServerInput{Name: "box-1.a_b", Host: "h.example", Port: 22, User: "u", Auth: "password"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		edit func(*ServerInput)
		want string
	}{
		{func(s *ServerInput) { s.Name = "" }, "name:"},
		{func(s *ServerInput) { s.Name = "a b" }, "name:"},
		{func(s *ServerInput) { s.Name = strings.Repeat("x", 65) }, "name:"},
		{func(s *ServerInput) { s.Host = "" }, "host:"},
		{func(s *ServerInput) { s.Host = "h h" }, "host:"},
		{func(s *ServerInput) { s.Host = "h\x00" }, "host:"},
		{func(s *ServerInput) { s.Host = "h​" }, "host:"},
		{func(s *ServerInput) { s.Port = 0 }, "port:"},
		{func(s *ServerInput) { s.Port = 65536 }, "port:"},
		{func(s *ServerInput) { s.User = "" }, "user:"},
		{func(s *ServerInput) { s.User = "u\t" }, "user:"},
		{func(s *ServerInput) { s.Auth = "kerberos" }, "auth:"},
		{func(s *ServerInput) { s.Auth = "key" }, "keyPath:"},
	} {
		in := ok
		c.edit(&in)
		if err := in.Validate(); err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%+v: want %q..., got %v", in, c.want, err)
		}
	}
}

func TestApplyServerSecretSemantics(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	f := &File{Version: 1}
	base := ServerInput{Name: "box", Host: "h", Port: 22, User: "u", Auth: "password"}
	if _, _, err := ApplyServer(f, "", withSecrets(base, ptr("p1"), ptr("s1"), ptr("d1"), ptr("k1")), mk); err != nil {
		t.Fatal(err)
	}
	if got := plain(t, f, f.Servers[0], mk); got != [4]string{"p1", "s1", "d1", "k1"} {
		t.Fatalf("create: %v", got)
	}
	// nil keeps the stored value; an unchanged AAD keeps the very same ciphertext.
	kept := f.Servers[0]
	if _, _, err := ApplyServer(f, "box", base, mk); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Servers[0], kept) {
		t.Fatalf("nil secrets changed the server:\n%+v\n%+v", kept, f.Servers[0])
	}
	// A value sets; "" clears.
	if _, _, err := ApplyServer(f, "box", withSecrets(base, ptr("p2"), ptr(""), ptr("d2"), ptr("")), mk); err != nil {
		t.Fatal(err)
	}
	if got := plain(t, f, f.Servers[0], mk); got != [4]string{"p2", "", "d2", ""} {
		t.Fatalf("set/clear: %v", got)
	}
}

func TestApplyServerRenameAndUserChangeReencrypt(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	f := &File{Version: 1}
	base := ServerInput{Name: "a", Host: "h", Port: 22, User: "u", Auth: "password"}
	if _, _, err := ApplyServer(f, "", withSecrets(base, ptr("p1"), ptr("s1"), nil, nil), mk); err != nil {
		t.Fatal(err)
	}
	renamed := base
	renamed.Name, renamed.User = "b", "root"
	if _, _, err := ApplyServer(f, "a", renamed, mk); err != nil {
		t.Fatal(err)
	}
	if len(f.Servers) != 1 || f.Servers[0].Name != "b" {
		t.Fatalf("rename: %+v", f.Servers)
	}
	if got := plain(t, f, f.Servers[0], mk); got != [4]string{"p1", "s1", "", ""} {
		t.Fatalf("secrets after rename: %v", got)
	}
}

func TestApplyServerNewEndpointDropsPinAndUnsuppliedSecrets(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	base := ServerInput{Name: "a", Host: "h", Port: 22, User: "u", Auth: "password"}
	for name, move := range map[string]func(*ServerInput){
		"port": func(s *ServerInput) { s.Port = 2222 },
		"host": func(s *ServerInput) { s.Host = "h2" },
	} {
		f := &File{Version: 1}
		if _, _, err := ApplyServer(f, "", withSecrets(base, ptr("p1"), ptr("s1"), ptr("d1"), nil), mk); err != nil {
			t.Fatal(err)
		}
		f.Servers[0].HostKey, f.Servers[0].HostKeyAlgo = "SHA256:x", "ssh-ed25519"
		in := withSecrets(base, nil, ptr("s2"), nil, nil)
		move(&in)
		if _, _, err := ApplyServer(f, "a", in, mk); err != nil {
			t.Fatal(err)
		}
		s := f.Servers[0]
		if s.HostKey != "" || s.HostKeyAlgo != "" {
			t.Fatalf("%s: pin kept: %+v", name, s)
		}
		if got := plain(t, f, s, mk); got != [4]string{"", "s2", "", ""} {
			t.Fatalf("%s: secrets %v", name, got)
		}
	}
}

func TestApplyServerAIVisibleOnlyKeepsEverythingElse(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	f := &File{Version: 1}
	base := ServerInput{Name: "a", Host: "h", Port: 22, User: "u", Auth: "password"}
	if _, _, err := ApplyServer(f, "", withSecrets(base, ptr("p1"), nil, nil, nil), mk); err != nil {
		t.Fatal(err)
	}
	f.Servers[0].HostKey, f.Servers[0].HostKeyAlgo = "SHA256:x", "ssh-ed25519"
	want := f.Servers[0]
	want.AIVisible = true
	in := base
	in.AIVisible = true
	_, after, err := ApplyServer(f, "a", in, mk)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, want) || !reflect.DeepEqual(f.Servers[0], want) {
		t.Fatalf("got %+v, want %+v", after, want)
	}
}

// Every save turns auto-allow off, whether or not the caller mentions it.
func TestApplyServerDropsAutoAllow(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	f := &File{Version: 1, Servers: []Server{{Name: "a", Host: "h", Port: 22, User: "u", Auth: "agent", AutoAllow: true}}}
	_, after, err := ApplyServer(f, "a", ServerInput{Name: "a", Host: "h", Port: 22, User: "u", Auth: "agent", AIVisible: true}, mk)
	if err != nil {
		t.Fatal(err)
	}
	if after.AutoAllow {
		t.Fatalf("autoAllow survived a save: %+v", after)
	}
}

// Unlike AutoAllow (the forever vault flag), the two opt-ins are ordinary
// editor fields: ApplyServer copies them straight from the input.
func TestApplyServerCopiesAutoAllowOptIns(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	f := &File{Version: 1}
	in := ServerInput{Name: "a", Host: "h", Port: 22, User: "u", Auth: "agent", AutoAllowRoot: true, AutoAllowSudo: true}
	_, after, err := ApplyServer(f, "", in, mk)
	if err != nil {
		t.Fatal(err)
	}
	if !after.AutoAllowRoot || !after.AutoAllowSudo {
		t.Fatalf("opt-ins not copied: %+v", after)
	}
}

func TestApplyServerNamesKeyPathAndKeylessSecrets(t *testing.T) {
	_, mk, _ := NewKDF("pw")
	f := &File{Version: 1}
	a := ServerInput{Name: "a", Host: "h", Port: 22, User: "u", Auth: "agent", KeyPath: "/stale"}
	b := a
	b.Name = "b"
	if _, s, err := ApplyServer(f, "", a, mk); err != nil || s.KeyPath != "" {
		t.Fatalf("keyPath must be dropped unless auth is key: %v %+v", err, s)
	}
	if _, _, err := ApplyServer(f, "", b, mk); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyServer(f, "", a, mk); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate create: %v", err)
	}
	if _, _, err := ApplyServer(f, "a", b, mk); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("rename onto another server: %v", err)
	}
	if _, _, err := ApplyServer(f, "nope", a, mk); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing original: %v", err)
	}
	if _, _, err := ApplyServer(f, "a", withSecrets(a, ptr("x"), nil, nil, nil), nil); err == nil {
		t.Fatal("a secret was stored without a vault key")
	}
	if len(f.Servers) != 2 || f.Servers[0].EncPassword != "" {
		t.Fatalf("a failed apply changed the file: %+v", f.Servers)
	}
}

func TestApplyServerKeepsTunnels(t *testing.T) {
	tun := []Tunnel{{ID: "0123456789abcdef", Kind: "dynamic", ListenPort: 1080}}
	f := &File{Version: 1, Servers: []Server{{Name: "a", Host: "h", Port: 22, User: "u", Auth: "agent", Tunnels: tun}}}
	// A host change and a rename: the tunnels still come along.
	_, after, err := ApplyServer(f, "a", ServerInput{Name: "b", Host: "h2", Port: 22, User: "u", Auth: "agent"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Tunnels) != 1 || after.Tunnels[0] != tun[0] || len(f.Servers[0].Tunnels) != 1 {
		t.Fatalf("tunnels lost: %+v", after)
	}
}
