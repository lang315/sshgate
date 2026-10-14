package view

import (
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

// sendReadyTimeout is how long Send to tab waits for its tab to open and
// print (Electron's waitReady). A var so tests can shorten it.
var sendReadyTimeout = 15 * time.Second

// pendingSend is a Send to tab waiting for its tab. seq tells attempts
// apart: a retry's wait must not end on an earlier attempt's timer.
type pendingSend struct {
	req, tab, command string
	seq               int
}

// sendToTab pastes a request's command into its server's most recent tab,
// or a new one, once that tab is open and the remote printed something,
// then decides sent_to_tab (the Electron order: paste, then decide).
// Nothing runs: no Enter follows the paste.
func (a *App) sendToTab(r hubapi.Request) {
	if a.itemBusy[r.ID] {
		return
	}
	if r.Stdin != "" { // the button is disabled; the hub refuses it too
		a.itemErr[r.ID] = "Send to tab is not available for a command with stdin"
		return
	}
	id := a.tabs.MostRecentFor(r.Server)
	if id == "" {
		id = a.openTab(r.Server)
	} else {
		a.activate(id)
	}
	a.itemBusy[r.ID], a.itemErr[r.ID] = true, ""
	a.sendSeq++
	req, seq := r.ID, a.sendSeq
	a.sends = append(a.sends, pendingSend{req: req, tab: id, command: r.Command, seq: seq})
	time.AfterFunc(sendReadyTimeout, func() { a.Update(func() { a.endSend(req, seq, "terminal did not become ready") }) })
	a.flushSends(id)
}

// flushSends pastes or fails the sends waiting for tab id. A request
// decided elsewhere meanwhile is not pasted.
func (a *App) flushSends(id string) {
	t, ok := a.tabs.Get(id)
	var keep []pendingSend
	for _, s := range a.sends {
		switch {
		case s.tab != id:
			keep = append(keep, s)
		case !ok || t.State == "exited":
			delete(a.itemBusy, s.req)
			a.itemErr[s.req] = "terminal closed"
		case t.State == "open" && t.SawOutput:
			delete(a.itemBusy, s.req)
			if a.waiting(s.req) {
				a.terms[id].term.Paste(s.command)
				a.decide(s.req, hubapi.SentToTab, "")
			}
		default:
			keep = append(keep, s)
		}
	}
	a.sends = keep
}

// endSend fails attempt seq of a send, if it is still waiting.
func (a *App) endSend(req string, seq int, msg string) {
	for i, s := range a.sends {
		if s.req == req && s.seq == seq {
			a.sends = append(a.sends[:i:i], a.sends[i+1:]...)
			delete(a.itemBusy, req)
			a.itemErr[req] = msg
			return
		}
	}
}

// failSends fails every waiting send; the requests stay pending.
func (a *App) failSends(msg string) {
	for _, s := range a.sends {
		delete(a.itemBusy, s.req)
		a.itemErr[s.req] = msg
	}
	a.sends = nil
}

func (a *App) waiting(req string) bool {
	for _, it := range a.items {
		if it.Request.ID == req {
			return true
		}
	}
	return false
}
