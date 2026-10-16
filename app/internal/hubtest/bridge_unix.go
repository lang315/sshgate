//go:build unix

package hubtest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// Bridge is the real MCP bridge, sshgate run with no arguments, as an AI
// client runs it: newline JSON-RPC over its stdio. Its tools reach the hub
// whose MCP door is in the runtime directory it was started with.
type Bridge struct {
	in   io.WriteCloser
	mu   sync.Mutex
	id   int
	wait map[int]chan bridgeReply // closed unanswered when the bridge exits
	dead bool
}

type bridgeReply struct {
	Result json.RawMessage
	Error  *struct{ Message string }
}

// StartBridge runs the bridge until the test ends, and initializes it. env
// is the hub's, from Runtime: the same SSHGATE_RUNTIME_DIR, so the bridge
// reaches that hub's MCP door, and the same temp HOME and empty
// SSH_AUTH_SOCK, so it never sees the real ones.
func StartBridge(t *testing.T, env []string) *Bridge {
	t.Helper()
	if env == nil {
		t.Fatal("StartBridge needs the hub's env from Runtime; nil would inherit the real one")
	}
	bin, _ := Binaries(t)
	cmd := exec.Command(bin)
	cmd.Env = env
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close(); cmd.Process.Kill(); cmd.Wait() })
	b := &Bridge{in: in, wait: map[int]chan bridgeReply{}}
	go b.read(out)
	ch, err := b.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "app-flow", "version": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case r, ok := <-ch:
		if !ok || r.Error != nil {
			t.Fatalf("bridge initialize: %+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("bridge initialize: no reply in 10 s")
	}
	b.mu.Lock()
	err = b.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{}})
	b.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// read hands each reply to its caller; notifications have no id.
func (b *Bridge) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		var m struct {
			ID *int `json:"id"`
			bridgeReply
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == nil {
			continue
		}
		b.mu.Lock()
		ch := b.wait[*m.ID]
		delete(b.wait, *m.ID)
		b.mu.Unlock()
		if ch != nil {
			ch <- m.bridgeReply
		}
	}
	b.mu.Lock()
	b.dead = true
	for id, ch := range b.wait {
		close(ch)
		delete(b.wait, id)
	}
	b.mu.Unlock()
}

// send writes one line; the caller holds mu, so lines never interleave.
func (b *Bridge) send(msg any) error {
	line, _ := json.Marshal(msg)
	_, err := b.in.Write(append(line, '\n'))
	return err
}

func (b *Bridge) request(method string, params any) (chan bridgeReply, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dead {
		return nil, errors.New("bridge exited")
	}
	b.id++
	ch := make(chan bridgeReply, 1)
	b.wait[b.id] = ch
	if err := b.send(map[string]any{"jsonrpc": "2.0", "id": b.id, "method": method, "params": params}); err != nil {
		delete(b.wait, b.id)
		return nil, err
	}
	return ch, nil
}

// Tool calls an MCP tool; the result arrives once the hub answers, after a
// human decision unless auto-allow ran it. The tool's text is Stdout, with
// ExitCode from its "exit code: N" line; a tool error's text is Err.
func (b *Bridge) Tool(name string, args map[string]any) <-chan ExecResult {
	out := make(chan ExecResult, 1)
	ch, err := b.request("tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		out <- ExecResult{Err: err}
		return out
	}
	go func() {
		var r bridgeReply
		var ok bool
		select {
		case r, ok = <-ch:
		case <-time.After(30 * time.Second): // longer than any approval wait in the tests
			out <- ExecResult{Err: errors.New("bridge: no reply in 30 s")}
			return
		}
		if !ok {
			out <- ExecResult{Err: errors.New("bridge exited")}
			return
		}
		if r.Error != nil {
			out <- ExecResult{Err: errors.New(r.Error.Message)}
			return
		}
		var res struct {
			Content []struct{ Text string }
			IsError bool
		}
		if err := json.Unmarshal(r.Result, &res); err != nil {
			out <- ExecResult{Err: err}
			return
		}
		var text strings.Builder
		for _, c := range res.Content {
			text.WriteString(c.Text)
		}
		if res.IsError {
			out <- ExecResult{Err: errors.New(text.String())}
			return
		}
		e := ExecResult{Stdout: text.String()}
		fmt.Sscanf(e.Stdout, "exit code: %d", &e.ExitCode)
		out <- e
	}()
	return out
}
