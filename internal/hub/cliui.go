package hub

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"

	"github.com/lang315/sshgate/internal/broker"
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
		switch e.Kind {
		case "pending":
			printRequest(out, e.Request)
		case "decided":
			printDecided(out, e)
		}
	})
	defer release()
	fmt.Fprintln(out, "sshgate hub (CLI approver). Commands: a [id]=allow  d [id] [reason]=deny  D=deny all  s [id]=send to tab  u=unlock  p=list pending  q=quit (id required when 2+ pending; p lists ids)")

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
			if r, _, ok := resolveTarget(out, pend, rest, false); ok {
				h.broker.Decide(r.ID, broker.Decision{Outcome: broker.Allowed})
			}
		case "d":
			if r, reason, ok := resolveTarget(out, pend, rest, true); ok {
				h.broker.Decide(r.ID, broker.Decision{Outcome: broker.Denied, Reason: reason})
			}
		case "s":
			if r, _, ok := resolveTarget(out, pend, rest, false); ok {
				if r.Stdin != "" {
					fmt.Fprintln(out, broker.ErrSendToTabStdin)
					break
				}
				fmt.Fprintf(out, "paste into your terminal (not run here):\n  %s\n", r.Command)
				h.broker.Decide(r.ID, broker.Decision{Outcome: broker.SentToTab})
			}
		case "D":
			h.broker.DenyAll(rest)
		case "u":
			if !h.hasVault() {
				fmt.Fprintln(out, "no vault yet: create one in the desktop app")
				continue
			}
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
// exactly one. It never guesses: an id that is not pending (e.g. one that
// was withdrawn while the human read it) decides nothing, even when another
// request is now the only one pending. With 2+ pending and no id it prints
// guidance and returns ok=false. reason is the text after the id. For d
// (withReason) with exactly one pending, a first word that is not
// id-shaped starts the reason, so "d not now" denies it with "not now".
func resolveTarget(out io.Writer, pend []broker.Request, rest string, withReason bool) (r broker.Request, reason string, ok bool) {
	tok, remainder, _ := strings.Cut(rest, " ")
	switch {
	case len(pend) == 0:
		fmt.Fprintln(out, "nothing pending")
		return broker.Request{}, "", false
	case tok == "" && len(pend) == 1:
		return pend[0], "", true
	case tok == "":
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
	if withReason && len(pend) == 1 && !idShaped.MatchString(tok) {
		return pend[0], rest, true
	}
	fmt.Fprintf(out, "no pending request %s\n", tok)
	return broker.Request{}, "", false
}

// idShaped matches broker request ids (8 random bytes, hex).
var idShaped = regexp.MustCompile(`^[0-9a-f]{16}$`)

func printDecided(out io.Writer, e broker.Event) {
	fmt.Fprintf(out, "\n=== %s id=%s (%s)", e.Decision.Outcome, e.Request.ID, e.Request.Command)
	if e.Decision.Reason != "" {
		fmt.Fprintf(out, ": %s", e.Decision.Reason)
	}
	fmt.Fprint(out, "\n> ")
}

func printRequest(out io.Writer, r broker.Request) {
	sudo := ""
	if r.Sudo {
		sudo = " [SUDO]"
	}
	fmt.Fprintf(out, "\n=== pending id=%s from %q (unverified)%s\n", r.ID, r.Client, sudo)
	fmt.Fprintf(out, "server : %s\n", r.Server)
	fmt.Fprintf(out, "target : %s\n", r.Target)
	fmt.Fprintf(out, "command: %s\n", r.Command)
	if r.Stdin != "" {
		fmt.Fprintf(out, "stdin (%d bytes):\n", len(r.Stdin))
		for _, line := range strings.Split(strings.TrimSuffix(r.Stdin, "\n"), "\n") {
			fmt.Fprintf(out, "| %s\n", strings.ReplaceAll(line, "\r", "␍"))
		}
	}
	fmt.Fprintf(out, "timeout: %ds\n", r.TimeoutSec)
	if r.Description != "" {
		fmt.Fprintf(out, "AI says (unverified): %s\n", r.Description)
	}
	fmt.Fprint(out, "> ")
}
