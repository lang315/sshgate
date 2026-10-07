package hub

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/lang315/sshgate/internal/sshx"
)

// Fake secrets of three kinds in one command's output: each must stay out
// of what the AI gets and out of the audit log.
const (
	fakeKeyBody = "MIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gun"
	fakeDBPass  = "Wm4tQz8vLp2Rk7Xs"
	fakeBearer  = "9f8e7d6c5b4a39281706f5e4d3c2b1a0"
)

var secretResult = sshx.ExecResult{
	Stdout: "-----BEGIN RSA PRIVATE KEY-----\n" + fakeKeyBody + "\n-----END RSA PRIVATE KEY-----\nDB_PASSWORD=" + fakeDBPass + "\n",
	Stderr: "> Authorization: Bearer " + fakeBearer + "\n",
}

// checkRedacted asserts what the AI got and the run's exec audit record.
func checkRedacted(t *testing.T, res ExecResponse, raw string, rec map[string]any) {
	t.Helper()
	if res.Stdout != "[REDACTED:private_key]\nDB_PASSWORD=[REDACTED:password]\n" || res.Stderr != "> Authorization: Bearer [REDACTED:auth_header]\n" {
		t.Errorf("AI got stdout %q stderr %q", res.Stdout, res.Stderr)
	}
	if want := map[string]int{"private_key": 1, "password": 1, "auth_header": 1}; !maps.Equal(res.Redacted, want) {
		t.Errorf("Redacted = %v, want %v", res.Redacted, want)
	}
	got, _ := rec["redacted"].(map[string]any) // JSON numbers decode as float64
	if len(got) != 3 || got["private_key"] != float64(1) || got["password"] != float64(1) || got["auth_header"] != float64(1) {
		t.Errorf("audit redacted = %v", rec["redacted"])
	}
	for _, s := range []string{fakeKeyBody, fakeDBPass, fakeBearer} {
		if strings.Contains(res.Stdout+res.Stderr, s) || strings.Contains(raw, s) {
			t.Errorf("fake secret %q reached the AI or the audit log", s)
		}
	}
}

func TestExecRedactsPatternsApproved(t *testing.T) {
	h, path := newHub(t, &fakeExec{res: secretResult})
	go allowFirst(t, h.Broker())
	res, err := h.Exec(context.Background(), ExecRequest{Client: "t", Server: "vis", Command: "cat /etc/app.env"})
	if err != nil {
		t.Fatal(err)
	}
	raw, recs := readAudit(t, path)
	checkRedacted(t, res, raw, recs[len(recs)-1])
}

// With a grant nothing decides: if the run fell through to approval it
// would expire (200 ms) and Exec would fail.
func TestExecRedactsPatternsAutoAllowed(t *testing.T) {
	h, path := newHub(t, &fakeExec{res: secretResult})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	res, err := h.Exec(context.Background(), ExecRequest{Client: "t", Server: "vis", Command: "cat /etc/app.env"})
	if err != nil {
		t.Fatal(err)
	}
	raw, recs := readAudit(t, path)
	checkRedacted(t, res, raw, findAutoRecord(t, recs))
}

// A vault secret is masked first, as "***", and counted as kind "secret":
// in the response, in its JSON on the MCP door, and in the audit record.
func TestExecVaultSecretIsCounted(t *testing.T) {
	h, path, _ := newEncHub(t, &fakeExec{res: sshx.ExecResult{Stdout: "DB_PASSWORD=s3cr3t-pw\nhello\n"}})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	go allowFirst(t, h.Broker())
	res, err := h.Exec(context.Background(), ExecRequest{Server: "enc", Command: "cat app.env"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "DB_PASSWORD=***\nhello\n" || !maps.Equal(res.Redacted, map[string]int{"secret": 1}) {
		t.Fatalf("got %+v", res)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"redacted":{"secret":1}`) {
		t.Fatalf("MCP door JSON: %s", b)
	}
	_, recs := readAudit(t, path)
	if r, ok := recs[len(recs)-1]["redacted"].(map[string]any); !ok || r["secret"] != float64(1) {
		t.Fatalf("audit redacted = %v", recs[len(recs)-1]["redacted"])
	}
}
