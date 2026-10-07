package hub

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
)

func TestExecStdinApprovedPath(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHub(t, fe)
	go func() {
		if !waitFor(t, "a pending request", func() bool { return len(h.Broker().Pending()) > 0 }) {
			return
		}
		r := h.Broker().Pending()[0]
		if r.Stdin != "a\nb\n" {
			t.Errorf("approver saw stdin %q", r.Stdin)
		}
		h.Broker().Decide(r.ID, broker.Decision{Outcome: broker.Allowed})
	}()
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "cat > f", Stdin: "a\nb\n"}); err != nil {
		t.Fatal(err)
	}
	if len(fe.calls) != 1 || fe.calls[0] != "stdin:cat > f" || fe.stdin != "a\nb\n" {
		t.Fatalf("calls = %v stdin = %q", fe.calls, fe.stdin)
	}
	_, recs := readAudit(t, path)
	if last := recs[len(recs)-1]; last["stdin"] != "a\nb\n" || last["outcome"] != "allowed" {
		t.Fatalf("audit: %v", last)
	}
}

func TestExecStdinAutoPath(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHub(t, fe)
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	type call struct {
		method string
		params map[string]any
	}
	calls := make(chan call, 1)
	release := h.setAutoSink(func(method string, params any) { calls <- call{method, params.(map[string]any)} })
	defer release()
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "cat > f", Stdin: "abcd"}); err != nil {
		t.Fatal(err)
	}
	if len(h.Broker().Pending()) != 0 || len(fe.calls) != 1 || fe.calls[0] != "stdin:cat > f" {
		t.Fatalf("pending %d calls %v", len(h.Broker().Pending()), fe.calls)
	}
	_, recs := readAudit(t, path)
	if last := findAutoRecord(t, recs); last["approval"] != "auto" || last["stdin"] != "abcd" {
		t.Fatalf("audit: %v", last)
	}
	c := <-calls
	if c.method != "autoAllow.ran" || c.params["stdinBytes"] != 4 {
		t.Fatalf("ran = %+v", c)
	}
	if _, ok := c.params["stdin"]; ok {
		t.Fatal("autoAllow.ran carries stdin content")
	}
}

// Refused before the broker and the auto path: no pending entry, no run,
// no audit record (like the other validation errors).
func TestExecStdinRefusals(t *testing.T) {
	fe := &fakeExec{}
	h, path := newHub(t, fe)
	addServer(t, h, path, encServer(t, "suPw", "encSuPassword", "su-pw"))
	raw0, _ := readAudit(t, path)
	cases := []struct {
		name string
		r    ExecRequest
		want error
	}{
		{"sudo", ExecRequest{Server: "vis", Command: "cat > f", Stdin: "x", Sudo: true}, config.ErrStdinSudo},
		{"su host", ExecRequest{Server: "suPw", Command: "cat > f", Stdin: "x"}, config.ErrStdinSu},
	}
	for _, c := range cases {
		if _, err := h.Exec(context.Background(), c.r); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "cat", Stdin: "a\x1bb"}); err == nil {
		t.Error("ESC in stdin accepted")
	}
	if len(h.Broker().Pending()) != 0 || len(fe.calls) != 0 {
		t.Fatalf("refused stdin reached the broker or ran: %v", fe.calls)
	}
	if raw, _ := readAudit(t, path); raw != raw0 {
		t.Fatalf("refusals were audited:\n%s", raw[len(raw0):])
	}
}

func TestExecStdinVaultSecretMaskedInAudit(t *testing.T) {
	fe := &fakeExec{}
	h, path, _ := newEncHub(t, fe)
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	go allowFirst(t, h.Broker())
	if _, err := h.Exec(context.Background(), ExecRequest{Server: "enc", Command: "cat > .env", Stdin: "PASS=s3cr3t-pw\n"}); err != nil {
		t.Fatal(err)
	}
	if fe.stdin != "PASS=s3cr3t-pw\n" {
		t.Fatalf("command got %q", fe.stdin)
	}
	_, recs := readAudit(t, path)
	if got := recs[len(recs)-1]["stdin"]; got != "PASS=***\n" {
		t.Fatalf("audit stdin = %v", got)
	}
}

func TestUIDoorRefusesSendToTabWithStdin(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, _ := startUIRaw(t, h)
	go h.Exec(context.Background(), ExecRequest{Server: "vis", Command: "cat > f", Stdin: "x"})
	waitPending(t, h.Broker(), 1)
	id := h.Broker().Pending()[0].ID
	err := c.Call(context.Background(), "decide", map[string]string{"id": id, "outcome": "sent_to_tab"}, nil)
	if err == nil || !strings.Contains(err.Error(), "send to tab is not available for a command with stdin") {
		t.Fatalf("got %v", err)
	}
	h.Broker().Decide(id, broker.Decision{Outcome: broker.Denied})
}
