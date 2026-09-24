package hub

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/lang315/ssh-mcp/internal/broker"
)

// ReadPassword, when set, reads the master password for the "u" command
// without echoing it (runHub sets it to a golang.org/x/term.ReadPassword
// reader when stdin is a real terminal). The nil default falls back to a
// plain scanned line, used for pipes and tests.
var ReadPassword func() (string, error)

// RunCLIApprover is the terminal stand-in for the Electron approval panel.
// It exercises the same broker the UI door will use. It returns when ctx is
// cancelled, in is exhausted, or the user types "q".
func RunCLIApprover(ctx context.Context, h *Hub, in io.Reader, out io.Writer) error {
	// The event sink runs on whatever goroutine calls h.Exec, concurrently
	// with this function's own prompt/command output; serialize both onto
	// out so a "pending" print can't tear against them.
	out = &syncWriter{w: out}
	release := h.setEventSink(func(e broker.Event) {
		if e.Kind == "pending" {
			printRequest(out, e.Request)
		}
	})
	defer release()
	fmt.Fprintln(out, "ssh-mcp hub (CLI approver). Commands: a [id]=allow  d [id] [reason]=deny  D=deny all  s [id]=send to tab  u=unlock  p=list pending  q=quit (id required when 2+ pending; p lists ids)")

	sc := bufio.NewScanner(in)
	for {
		line, err := readLine(ctx, sc, false)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cmd, rest, _ := strings.Cut(line, " ")
		pend := h.broker.Pending()
		switch cmd {
		case "a":
			if r, _, ok := resolveTarget(out, pend, rest); ok {
				h.broker.Decide(r.ID, broker.Decision{Outcome: broker.Allowed})
			}
		case "d":
			if r, reason, ok := resolveTarget(out, pend, rest); ok {
				h.broker.Decide(r.ID, broker.Decision{Outcome: broker.Denied, Reason: reason})
			}
		case "s":
			if r, _, ok := resolveTarget(out, pend, rest); ok {
				fmt.Fprintf(out, "paste into your terminal (not run here):\n  %s\n", r.Command)
				h.broker.Decide(r.ID, broker.Decision{Outcome: broker.SentToTab})
			}
		case "D":
			h.broker.DenyAll(rest)
		case "u":
			fmt.Fprint(out, "master password: ")
			pw, err := readLine(ctx, sc, true)
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if ReadPassword != nil {
				fmt.Fprintln(out) // ReadPassword doesn't echo the user's Enter
			}
			if err := h.Unlock(strings.TrimSpace(pw)); err != nil {
				fmt.Fprintln(out, "unlock failed:", err)
			} else {
				fmt.Fprintln(out, "unlocked")
			}
		case "p":
			for _, r := range pend {
				printRequest(out, r)
			}
		case "q":
			return nil
		default:
			fmt.Fprintln(out, "unknown command")
		}
	}
}

// lineResult carries a scan or ReadPassword outcome back from the reader
// goroutine readLine starts.
type lineResult struct {
	text string
	err  error
}

// readLine reads one line from sc, or, when pw is true and ReadPassword is
// set, one line without echo via ReadPassword instead of sc. The read runs
// on its own goroutine so this can select on ctx.Done(): the scanner (or
// ReadPassword) may be blocked on a Read that never returns, e.g. idle
// stdin, and must not stop ctx cancellation from unblocking the caller. The
// goroutine leaks in that case; it exits on its own if in is later closed.
//
// Return value: on a clean end of input (sc.Scan() returns false with no
// error, same as the brief's `for sc.Scan() { ... }; return sc.Err()`
// loop), the error is io.EOF; on a real scanner error, that error; on ctx
// cancellation, ctx.Err().
func readLine(ctx context.Context, sc *bufio.Scanner, pw bool) (string, error) {
	ch := make(chan lineResult, 1)
	go func() {
		if pw && ReadPassword != nil {
			s, err := ReadPassword()
			ch <- lineResult{text: s, err: err}
			return
		}
		if sc.Scan() {
			ch <- lineResult{text: sc.Text()}
			return
		}
		err := sc.Err()
		if err == nil {
			err = io.EOF
		}
		ch <- lineResult{err: err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		return r.text, r.err
	}
}

// syncWriter serializes writes from RunCLIApprover's own goroutine and the
// event-sink goroutine (whichever calls h.Exec) onto a single io.Writer.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// resolveTarget interprets the argument after a/d/s (R31): an explicit
// request id, or, when omitted, the sole pending request if there is
// exactly one. It never guesses among several pending requests. With 2+
// pending and no id, or an id that matches nothing, it prints guidance to
// out and returns ok=false without deciding anything. reason is the text
// after the id (or, for the single-pending shorthand, the whole rest, so
// e.g. "d not now" with one request pending denies it with reason "not
// now" without requiring an id).
func resolveTarget(out io.Writer, pend []broker.Request, rest string) (r broker.Request, reason string, ok bool) {
	tok, remainder, _ := strings.Cut(rest, " ")
	if len(pend) == 1 && (tok == "" || !hasPendingID(pend, tok)) {
		return pend[0], rest, true
	}
	if tok == "" {
		if len(pend) == 0 {
			fmt.Fprintln(out, "nothing pending")
			return broker.Request{}, "", false
		}
		for _, p := range pend {
			printRequest(out, p)
		}
		fmt.Fprintln(out, "several requests pending: use a <id> / d <id> / s <id>")
		return broker.Request{}, "", false
	}
	for _, p := range pend {
		if p.ID == tok {
			return p, remainder, true
		}
	}
	fmt.Fprintf(out, "no pending request %s\n", tok)
	return broker.Request{}, "", false
}

func hasPendingID(pend []broker.Request, id string) bool {
	for _, p := range pend {
		if p.ID == id {
			return true
		}
	}
	return false
}

func printRequest(out io.Writer, r broker.Request) {
	sudo := ""
	if r.Sudo {
		sudo = " [SUDO]"
	}
	fmt.Fprintf(out, "\n=== pending id=%s from %q (unverified)%s\n", r.ID, r.Client, sudo)
	fmt.Fprintf(out, "server : %s\n", r.Server)
	fmt.Fprintf(out, "command: %s\n", r.Command)
	fmt.Fprintf(out, "timeout: %ds\n", r.TimeoutSec)
	if r.Description != "" {
		fmt.Fprintf(out, "AI says (unverified): %s\n", r.Description)
	}
	fmt.Fprint(out, "> ")
}
