package config

import "testing"

func TestParseArgv(t *testing.T) {
	m := ParseArgv([]string{"--host=1.2.3.4", "--disableSudo", "--port=2222"})
	if *m["host"] != "1.2.3.4" {
		t.Fatalf("host = %v", m["host"])
	}
	if m["disableSudo"] != nil {
		t.Fatalf("flag should map to nil value")
	}
	if _, ok := m["disableSudo"]; !ok {
		t.Fatalf("disableSudo key missing")
	}
	if *m["port"] != "2222" {
		t.Fatalf("port = %v", m["port"])
	}
}

func TestParseMaxChars(t *testing.T) {
	s := func(v string) *string { return &v }
	if ParseMaxChars(nil) != 1000 {
		t.Fatal("default")
	}
	if ParseMaxChars(s("none")) != -1 {
		t.Fatal("none")
	}
	if ParseMaxChars(s("0")) != -1 {
		t.Fatal("zero")
	}
	if ParseMaxChars(s("500")) != 500 {
		t.Fatal("500")
	}
	if ParseMaxChars(s("garbage")) != 1000 {
		t.Fatal("garbage falls back to default")
	}
}

func TestBuildCLIConfigValidation(t *testing.T) {
	s := func(v string) *string { return &v }
	_, err := BuildCLIConfig(map[string]*string{"host": s("h")}) // missing user
	if err == nil {
		t.Fatal("expected missing-user error")
	}
	cfg, err := BuildCLIConfig(map[string]*string{"host": s("h"), "user": s("u")})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 22 || cfg.TimeoutMs != 60000 || cfg.MaxChars != 1000 {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
}
