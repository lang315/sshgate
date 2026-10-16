package model

import "testing"

func TestDisplayText(t *testing.T) {
	for in, want := range map[string]string{
		"box":             "box",
		"a\u202eb":        "a\\u202eb",
		"x\u200by":        "x\\u200by",
		"tab\there":       "tab\\u0009here",
		"nl\n":            "nl\\u000a",
		"del\u007f":       "del\\u007f",
		"c1\u0085":        "c1\\u0085",
		"iso\u2066\u2069": "iso\\u2066\\u2069",
		"bom\ufeff":       "bom\\ufeff",
		"ls\u2028":        "ls\\u2028",
		"tiếng":           "tiếng",
		"rlm\u200f":       "rlm\\u200f",
		"wj\u2060":        "wj\\u2060",
		"ip\u2064":        "ip\\u2064",
		"ps\u2029":        "ps\\u2029",
		"c1\u009f":        "c1\\u009f",
	} {
		if got := DisplayText(in); got != want {
			t.Errorf("DisplayText(%q) = %q, want %q", in, got, want)
		}
	}
}
