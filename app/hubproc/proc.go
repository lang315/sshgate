package hubproc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/lang315/sshgate/app/door"
)

// Protocol is the UI door protocol this app speaks.
const Protocol = 12

const stderrTail = 50

// Proc is one running hub.
type Proc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	client *door.Client
	done   chan struct{}

	mu   sync.Mutex
	tail []string
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
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &Proc{cmd: cmd, stdin: stdin, done: make(chan struct{})}
	go p.readStderr(stderr)
	p.client = door.New(stdout, stdin, onNote)
	go func() { cmd.Wait(); close(p.done) }()

	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var hello struct{ Protocol int }
	if err := p.client.Call(hctx, "hello", map[string]any{}, &hello); err != nil {
		p.Stop()
		return nil, fmt.Errorf("hub hello: %w", err)
	}
	if hello.Protocol != Protocol {
		p.Stop()
		return nil, fmt.Errorf("hub speaks protocol %d, this app speaks %d", hello.Protocol, Protocol)
	}
	return p, nil
}

func (p *Proc) readStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		p.mu.Lock()
		p.tail = append(p.tail, sc.Text())
		if len(p.tail) > stderrTail {
			p.tail = p.tail[len(p.tail)-stderrTail:]
		}
		p.mu.Unlock()
	}
}

func (p *Proc) Client() *door.Client  { return p.client }
func (p *Proc) Done() <-chan struct{} { return p.done }
func (p *Proc) Pid() int              { return p.cmd.Process.Pid }

// StderrTail is the hub's last stderr lines.
func (p *Proc) StderrTail() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.tail...)
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
