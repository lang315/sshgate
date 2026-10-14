package model

import (
	"testing"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

func TestHostKeyPromptsOneAtATime(t *testing.T) {
	var p HostKeyPrompts
	p.Ask("t1", hubapi.HostKey{Fingerprint: "SHA256:one"})
	p.Ask("t2", hubapi.HostKey{Fingerprint: "SHA256:two"})
	if c, ok := p.Current(); !ok || c.ID != "t1" {
		t.Fatalf("%+v %v", c, ok)
	}
	if a, ok := p.Answer(); !ok || a.ID != "t1" {
		t.Fatalf("%+v %v", a, ok)
	}
	if c, _ := p.Current(); c.ID != "t2" {
		t.Fatalf("%+v", c)
	}
	if !p.Drop("t2") || p.Drop("t2") {
		t.Fatal("Drop must remove t2 once")
	}
	if _, ok := p.Current(); ok {
		t.Fatal("queue not empty")
	}
	p.Ask("t3", hubapi.HostKey{})
	p.Ask("t4", hubapi.HostKey{})
	p.Drop("t4") // a closed tab drops its own prompt, not the one shown
	if c, _ := p.Current(); c.ID != "t3" {
		t.Fatalf("%+v", c)
	}
}

func TestTrustDelayRestartsWhenThePromptChanges(t *testing.T) {
	t0 := time.Unix(1000, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	var p HostKeyPrompts
	p.Ask("t1", hubapi.HostKey{Fingerprint: "SHA256:one"})
	p.Ask("t2", hubapi.HostKey{Fingerprint: "SHA256:two"})
	lc := NewListChanges(PromptKey(p.Current()), at(0))
	if TrustEnabled(lc.At, at(499)) || !TrustEnabled(lc.At, at(500)) {
		t.Fatal("the delay is 500 ms")
	}
	p.Answer()
	lc.SetKey(PromptKey(p.Current()), at(600))
	if TrustEnabled(lc.At, at(1000)) || !TrustEnabled(lc.At, at(1100)) {
		t.Fatal("the next prompt must restart the delay")
	}
	if PromptKey(HostKeyPrompt{}, false) != "" || PromptKey(HostKeyPrompt{ID: "t1", Key: hubapi.HostKey{Fingerprint: "f"}}, true) != "t1|f" {
		t.Fatal("PromptKey")
	}
}
