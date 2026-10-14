package model

import (
	"testing"

	"github.com/lang315/sshgate/app/hubapi"
)

func TestFilterHosts(t *testing.T) {
	list := []hubapi.Server{{Name: "db-primary", Host: "10.0.4.7", User: "postgres", Port: 22}, {Name: "web", Host: "web.example", User: "deploy", Port: 22}}
	names := func(s []hubapi.Server) (out []string) {
		for _, x := range s {
			out = append(out, x.Name)
		}
		return
	}
	for q, want := range map[string]int{"": 2, "  ": 2, "DB": 1, "10.0.4": 1, "postgres": 1, "nothing": 0} {
		if got := FilterHosts(list, q); len(got) != want {
			t.Errorf("%q: %v", q, names(got))
		}
	}
	if got := FilterHosts(list, "22"); len(got) != 0 {
		t.Errorf("port matched: %v", names(got))
	}
}

func TestPasswordChecks(t *testing.T) {
	for _, tc := range []struct {
		pw, again     string
		length, match bool
	}{
		{"short", "short", false, true},
		{"password1", "password2", true, false},
		{"password1", "", true, false},
		{"password1", "password1", true, true},
	} {
		l, m := PasswordChecks(tc.pw, tc.again)
		if l != tc.length || m != tc.match {
			t.Errorf("%q %q: %v %v", tc.pw, tc.again, l, m)
		}
	}
}

func TestAddrAndTile(t *testing.T) {
	if Addr(hubapi.Server{User: "u", Host: "h", Port: 2222}) != "u@h:2222" {
		t.Fatal("Addr")
	}
	if Tile("box") != "B" || Tile("ébène") != "É" || Tile("") != "" {
		t.Fatal("Tile")
	}
}
