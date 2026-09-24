package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/lang315/ssh-mcp/internal/rpc"
)

type execParams struct {
	RequestID   string `json:"requestId,omitempty"`
	Client      string `json:"client"`
	Server      string `json:"server"`
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	TimeoutSec  int    `json:"timeoutSec,omitempty"`
}

// ServeMCPDoor accepts connections from the bridge. Every connection is
// peer-checked, then served a method table that contains only the three
// AI-facing calls plus cancel. Nothing here can unlock, decide, or read
// secrets: those methods are simply not registered. Cancelling ctx closes ln.
func ServeMCPDoor(ctx context.Context, ln net.Listener, h *Hub) error {
	stop := context.AfterFunc(ctx, func() { ln.Close() })
	defer stop()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := checkPeer(conn); err != nil {
			fmt.Fprintln(os.Stderr, "mcp door: rejected peer:", err)
			conn.Close()
			continue
		}
		go serveMCPConn(ctx, conn, h)
	}
}

func serveMCPConn(ctx context.Context, conn net.Conn, h *Hub) {
	defer conn.Close()

	var mu sync.Mutex
	inflight := map[string]context.CancelFunc{}

	s := rpc.NewServer()
	s.Handle("listServers", func(context.Context, json.RawMessage) (any, error) {
		_ = h.Reload()
		list := h.ServersForMCP()
		if list == nil {
			list = []ServerInfo{}
		}
		return list, nil
	})
	exec := func(sudo bool) rpc.Handler {
		// ctx is cancelled by rpc.Server when the connection closes, which
		// withdraws the pending approval or stops the running command.
		return func(ctx context.Context, raw json.RawMessage) (any, error) {
			var p execParams
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, &rpc.Error{Code: -32602, Message: "invalid params"}
			}
			_ = h.Reload()
			reqCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			if p.RequestID != "" {
				mu.Lock()
				if _, dup := inflight[p.RequestID]; dup {
					mu.Unlock()
					return nil, &rpc.Error{Code: -32602, Message: "duplicate requestId"}
				}
				inflight[p.RequestID] = cancel
				mu.Unlock()
				defer func() { mu.Lock(); delete(inflight, p.RequestID); mu.Unlock() }()
			}
			return h.Exec(reqCtx, ExecRequest{Client: p.Client, Server: p.Server, Command: p.Command, Description: p.Description, Sudo: sudo, TimeoutSec: p.TimeoutSec})
		}
	}
	s.Handle("exec", exec(false))
	s.Handle("sudoExec", exec(true))
	// cancel may arrive as a notification, which runs on the read loop: it
	// only calls a CancelFunc, so it never blocks.
	s.Handle("cancel", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			RequestID string `json:"requestId"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: "invalid params"}
		}
		mu.Lock()
		if c, ok := inflight[p.RequestID]; ok {
			c()
		}
		mu.Unlock()
		return map[string]bool{"ok": true}, nil
	})
	_ = s.Serve(ctx, conn, conn)
}
