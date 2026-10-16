package model

import (
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

// HostKeyPrompt is one tab's unknown host key, waiting for the person.
type HostKeyPrompt struct {
	ID  string // the tab, whose retry reuses this id
	Key hubapi.HostKey
}

// HostKeyPrompts shows host-key prompts one at a time, oldest first,
// across every tab (desktop/src/renderer/hostkeys.ts).
type HostKeyPrompts struct{ q []HostKeyPrompt }

func (p *HostKeyPrompts) Ask(id string, k hubapi.HostKey) {
	p.q = append(p.q, HostKeyPrompt{ID: id, Key: k})
}

// Current is the prompt shown.
func (p *HostKeyPrompts) Current() (HostKeyPrompt, bool) {
	if len(p.q) == 0 {
		return HostKeyPrompt{}, false
	}
	return p.q[0], true
}

// Answer takes the shown prompt off the queue and returns it.
func (p *HostKeyPrompts) Answer() (HostKeyPrompt, bool) {
	c, ok := p.Current()
	if ok {
		p.q = p.q[1:]
	}
	return c, ok
}

// Drop removes a tab's prompt wherever it waits, and reports whether it had one.
func (p *HostKeyPrompts) Drop(id string) bool {
	for i, x := range p.q {
		if x.ID == id {
			p.q = append(p.q[:i:i], p.q[i+1:]...)
			return true
		}
	}
	return false
}

// PromptKey changes whenever the prompt shown changes, which restarts the
// Trust delay.
func PromptKey(p HostKeyPrompt, ok bool) string {
	if !ok {
		return ""
	}
	return p.ID + "|" + p.Key.Fingerprint
}

// TrustEnabled: Trust waits AllowDelay after the prompt shown last changed.
func TrustEnabled(changedAt, now time.Time) bool { return now.Sub(changedAt) >= AllowDelay }
