package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/lang315/ssh-mcp/internal/rpc"
)

func TestTermMethodsRejectUnknownIDs(t *testing.T) {
	h, _ := newHub(t, &fakeExec{})
	uiR, hubW := io.Pipe()
	hubR, uiW := io.Pipe()
	go ServeUIDoor(context.Background(), h, hubR, hubW)
	c := rpc.NewClient(uiR, uiW, func(string, json.RawMessage) {})
	t.Cleanup(func() { uiW.Close(); hubW.Close() })

	ctx := context.Background()
	var re *rpc.Error
	err := c.Call(ctx, "term.open", map[string]any{"server": "nope", "rows": 24, "cols": 80}, nil)
	if !errors.As(err, &re) || re.Code == -32601 {
		t.Fatalf("unknown server: want a resolve error, got %v", err)
	}
	err = c.Call(ctx, "term.close", map[string]any{"id": "zzz"}, nil)
	if err == nil || err.Error() != `no terminal "zzz"` {
		t.Fatalf("unknown id on term.close: got %v", err)
	}
	// write/resize/ack are notifications; an unknown id is ignored and must
	// neither crash nor stall the read loop.
	for _, line := range []string{
		`{"jsonrpc":"2.0","method":"term.write","params":{"id":"zzz","data":"aGk="}}`,
		`{"jsonrpc":"2.0","method":"term.resize","params":{"id":"zzz","rows":1,"cols":1}}`,
		`{"jsonrpc":"2.0","method":"term.ack","params":{"id":"zzz","n":5}}`,
		`{"jsonrpc":"2.0","method":"term.write","params":"garbage"}`,
	} {
		if _, err := io.WriteString(uiW, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Call(ctx, "status", nil, nil); err != nil {
		t.Fatalf("door stalled after notifications: %v", err)
	}
}
