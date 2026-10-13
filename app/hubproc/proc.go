package hubproc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/lang315/sshgate/app/door"
)

// Protocol is the UI door protocol this app speaks.
const Protocol = 12

const stderrTail = 50

// ErrProtocol is wrapped by Start's error when the hub speaks another UI door
// protocol; it is fatal, a restart would meet the same hub.
var ErrProtocol = errors.New("hub protocol mismatch")

// Proc is one running hub.
type Proc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	client *door.Client
	done   chan struct{}

	mu      sync.Mutex
	tail    []string
	partial []byte // an unfinished stderr line
}

// Start spawns the hub and checks its protocol with hello. onNote gets the
// hub's notifications on the read goroutine and must not block.
func Start(ctx context.Context, l Launch, onNote func(string, json.RawMessage)) (*Proc, error) {
	cmd := exec.Command(l.Command, l.Args...)
	cmd.Env = l.Env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p := &Proc{cmd: cmd, stdin: stdin, done: make(chan struct{})}
	cmd.Stderr = (*tailWriter)(p) // a Writer, so Wait waits for the last stderr lines
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p.client = door.New(stdout, stdin, onNote)
	go func() {
		<-p.client.Done() // Wait closes the pipe, so let the reader reach EOF first
		cmd.Wait()
		close(p.done)
	}()

	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var hello struct{ Protocol int }
	if err := p.client.Call(hctx, "hello", map[string]any{}, &hello); err != nil {
		p.Stop()
		if tail := p.StderrTail(); len(tail) > 0 {
			return nil, fmt.Errorf("hub hello: %w\n%s", err, strings.Join(tail, "\n"))
		}
		return nil, fmt.Errorf("hub hello: %w", err)
	}
	if hello.Protocol != Protocol {
		p.Stop()
		return nil, fmt.Errorf("%w: hub speaks protocol %d, this app speaks %d", ErrProtocol, hello.Protocol, Protocol)
	}
	return p, nil
}

// tailWriter keeps the last stderrTail complete lines written to it.
type tailWriter Proc

func (w *tailWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial = append(w.partial, b...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		w.tail = append(w.tail, strings.TrimSuffix(string(w.partial[:i]), "\r"))
		w.partial = w.partial[i+1:]
	}
	if len(w.tail) > stderrTail {
		w.tail = w.tail[len(w.tail)-stderrTail:]
	}
	return len(b), nil
}

func (p *Proc) Client() *door.Client  { return p.client }
func (p *Proc) Done() <-chan struct{} { return p.done }
func (p *Proc) Pid() int              { return p.cmd.Process.Pid }

// StderrTail is the hub's last stderr lines.
func (p *Proc) StderrTail() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	tail := append([]string(nil), p.tail...)
	if len(p.partial) > 0 {
		tail = append(tail, string(p.partial))
	}
	return tail
}

// Stop closes the hub's stdin, which ends its UI door and the hub, and
// kills it if it has not exited 5 s later.
func (p *Proc) Stop() error {
	p.stdin.Close()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		p.cmd.Process.Kill()
		<-p.done
	}
	return nil
}
