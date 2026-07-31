package main

import "testing"

func TestRoute(t *testing.T) {
	cases := []struct {
		in   []string
		mode string
	}{
		{[]string{"web", "--port", "9000"}, "web"},
		{[]string{"--host=1.2.3.4", "--user=root"}, "mcp"},
		{[]string{}, "mcp"},
	}
	for _, c := range cases {
		got, _ := route(c.in)
		if got != c.mode {
			t.Fatalf("route(%v) = %q, want %q", c.in, got, c.mode)
		}
	}
}
