package config

import (
	"strings"
	"testing"
)

func TestTunnelValidate(t *testing.T) {
	ok := []Tunnel{
		{Kind: "local", ListenPort: 5433, TargetHost: "db", TargetPort: 5432},
		{Kind: "remote", ListenPort: 8080, TargetHost: "localhost", TargetPort: 3000, Label: "web"},
		{Kind: "dynamic", ListenPort: 1080},
		{Kind: "local", ListenPort: 1, TargetHost: "::1", TargetPort: 65535},
	}
	for _, tn := range ok {
		if err := tn.Validate(); err != nil {
			t.Errorf("%+v: %v", tn, err)
		}
	}
	bad := map[string]Tunnel{
		"kind":        {Kind: "socks", ListenPort: 1080},
		"listenPort":  {Kind: "dynamic", ListenPort: 0},
		"listenPort ": {Kind: "dynamic", ListenPort: 65536},
		"targetHost":  {Kind: "local", ListenPort: 1, TargetHost: "a b", TargetPort: 1},
		"targetHost ": {Kind: "local", ListenPort: 1, TargetHost: strings.Repeat("a", 254), TargetPort: 1},
		"targetPort":  {Kind: "remote", ListenPort: 1, TargetHost: "h", TargetPort: 0},
		"dynamic":     {Kind: "dynamic", ListenPort: 1, TargetHost: "h", TargetPort: 1},
		"label":       {Kind: "dynamic", ListenPort: 1, Label: strings.Repeat("x", 65)},
		"label ":      {Kind: "dynamic", ListenPort: 1, Label: "a\x1bb"},
	}
	for field, tn := range bad {
		err := tn.Validate()
		if err == nil || !strings.HasPrefix(err.Error(), strings.TrimSpace(field)) {
			t.Errorf("%s: %+v: got %v", field, tn, err)
		}
	}
}
