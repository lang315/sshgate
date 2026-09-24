package hub

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lang315/ssh-mcp/internal/config"
	"github.com/lang315/ssh-mcp/internal/rpc"
	"github.com/lang315/ssh-mcp/internal/sshx/sshtest"
	"golang.org/x/crypto/ssh"
)

type note struct {
	method string
	params json.RawMessage
}

// startTermDoor serves a UI door and returns a client, the raw writer for
// sending notifications, and every notification the hub sends.
func startTermDoor(t *testing.T, h *Hub) (*rpc.Client, io.Writer, chan note) {
	t.Helper()
	uiR, hubW := io.Pipe()
	hubR, uiW := io.Pipe()
	go ServeUIDoor(context.Background(), h, hubR, hubW)
	notes := make(chan note, 1024)
	c := rpc.NewClient(uiR, uiW, func(m string, p json.RawMessage) { notes <- note{m, p} })
	t.Cleanup(func() { uiW.Close(); hubW.Close() })
	return c, uiW, notes
}

func sendNote(t *testing.T, w io.Writer, method string, params any) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if _, err := w.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
}

// fakeServerHub stores one key-auth server "fk" pointing at an in-process
// SSH server whose shell echoes input.
func fakeServerHub(t *testing.T) *Hub {
	t.Helper()
	srv := sshtest.Start(t)
	dir := t.TempDir()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "id")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "servers.json")
	f := &config.File{Version: 1, Servers: []config.Server{
		{Name: "fk", Host: srv.Host, Port: srv.Port, User: "u", Auth: "key", KeyPath: keyPath},
	}}
	if err := config.Save(path, f, nil); err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{StorePath: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Registry().CloseAll)
	return h
}

func TestTermMethodsRejectUnknownIDs(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, w, _ := startTermDoor(t, h)
	ctx := context.Background()
	var re *rpc.Error
	err := c.Call(ctx, "term.open", map[string]any{"id": "a", "server": "nope", "rows": 24, "cols": 80}, nil)
	if !errors.As(err, &re) || re.Code == -32601 || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown server: want a resolve error, got %v", err)
	}
	err = c.Call(ctx, "term.close", map[string]any{"id": "zzz"}, nil)
	if err == nil || err.Error() != `no terminal "zzz"` {
		t.Fatalf("unknown id on term.close: got %v", err)
	}
	// A failed open must free its id.
	err = c.Call(ctx, "term.open", map[string]any{"id": "a", "server": "nope", "rows": 24, "cols": 80}, nil)
	if err == nil || strings.Contains(err.Error(), "already open") {
		t.Fatalf("id not released after failed open: %v", err)
	}
	// write/resize/ack are notifications; an unknown id or malformed params
	// are ignored and must neither crash nor stall the read loop.
	sendNote(t, w, "term.write", map[string]any{"id": "zzz", "data": "aGk="})
	sendNote(t, w, "term.resize", map[string]any{"id": "zzz", "rows": 1, "cols": 1})
	sendNote(t, w, "term.ack", map[string]any{"id": "zzz", "n": 5})
	sendNote(t, w, "term.write", "garbage")
	sendNote(t, w, "term.write", map[string]any{"id": "zzz", "data": "!!not base64"})
	if err := c.Call(ctx, "status", nil, nil); err != nil {
		t.Fatalf("door stalled after notifications: %v", err)
	}
}

func TestTermOpenValidatesParams(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	c, _, _ := startTermDoor(t, h)
	for _, p := range []map[string]any{
		{"server": "vis", "rows": 24, "cols": 80},
		{"id": "", "server": "vis", "rows": 24, "cols": 80},
		{"id": "a b", "server": "vis", "rows": 24, "cols": 80},
		{"id": "a/b", "server": "vis", "rows": 24, "cols": 80},
		{"id": strings.Repeat("x", 65), "server": "vis", "rows": 24, "cols": 80},
		{"id": "ok", "server": "vis", "rows": 0, "cols": 80},
		{"id": "ok", "server": "vis", "rows": 24, "cols": -1},
		{"id": "ok", "server": "vis", "rows": 1001, "cols": 80},
		{"id": "ok", "server": "vis", "rows": 24, "cols": 1001},
	} {
		var re *rpc.Error
		if err := c.Call(context.Background(), "term.open", p, nil); !errors.As(err, &re) || re.Code != -32602 {
			t.Fatalf("%v: want -32602, got %v", p, err)
		}
	}
}

// End to end over an in-process SSH server: client-chosen id, duplicate
// refused, keystroke notifications applied in order, close.
func TestTermOpenWriteInOrderAndClose(t *testing.T) {
	h := fakeServerHub(t)
	c, w, notes := startTermDoor(t, h)
	ctx := context.Background()
	open := map[string]any{"id": "tab-1", "server": "fk", "rows": 24, "cols": 80}
	var res struct {
		ID string `json:"id"`
	}
	if err := c.Call(ctx, "term.open", open, &res); err != nil || res.ID != "tab-1" {
		t.Fatalf("open: %v %+v", err, res)
	}
	if err := c.Call(ctx, "term.open", open, nil); err == nil || !strings.Contains(err.Error(), "already open") {
		t.Fatalf("duplicate id: %v", err)
	}

	var want strings.Builder
	for i := range 100 {
		k := fmt.Sprintf("k%d,", i)
		want.WriteString(k)
		sendNote(t, w, "term.write", map[string]any{"id": "tab-1", "data": []byte(k)})
		if i == 50 { // invalid resize and non-positive ack are ignored in-stream
			sendNote(t, w, "term.resize", map[string]any{"id": "tab-1", "rows": 0, "cols": 80})
			sendNote(t, w, "term.ack", map[string]any{"id": "tab-1", "n": -5})
			sendNote(t, w, "term.resize", map[string]any{"id": "tab-1", "rows": 30, "cols": 100})
		}
	}
	var got strings.Builder
	deadline := time.After(5 * time.Second)
	for got.Len() < want.Len() {
		select {
		case n := <-notes:
			if n.method != "term.data" {
				t.Fatalf("unexpected %s %s", n.method, n.params)
			}
			var d struct {
				ID   string `json:"id"`
				Data []byte `json:"data"`
			}
			json.Unmarshal(n.params, &d)
			if d.ID != "tab-1" {
				t.Fatalf("data for id %q", d.ID)
			}
			got.Write(d.Data)
		case <-deadline:
			t.Fatalf("echo incomplete: %q", got.String())
		}
	}
	if got.String() != want.String() {
		t.Fatalf("keystrokes out of order:\n got %q\nwant %q", got.String(), want.String())
	}

	if err := c.Call(ctx, "term.close", map[string]any{"id": "tab-1"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, "term.close", map[string]any{"id": "tab-1"}, nil); err == nil {
		t.Fatal("second close should error")
	}
	// The id is free again after close.
	if err := c.Call(ctx, "term.open", open, nil); err != nil {
		t.Fatalf("reopen: %v", err)
	}
}
