package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/lang315/sshgate/internal/files"
	"github.com/lang315/sshgate/internal/rpc"
)

const maxEarlyCancels = 64

type execParams struct {
	RequestID   string `json:"requestId,omitempty"`
	Client      string `json:"client"`
	Server      string `json:"server"`
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	Stdin       string `json:"stdin,omitempty"`
	TimeoutSec  int    `json:"timeoutSec,omitempty"`
}

// ServeMCPDoor accepts connections from the bridge. Every connection is
// peer-checked, then served a method table that contains only the
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
	// cancelled remembers cancels that arrived before their exec registered,
	// so the exec is refused instead of the cancel being lost. Bounded to the
	// most recent maxEarlyCancels ids.
	cancelled := map[string]bool{}
	var cancelOrder []string

	reload := func() {
		if err := h.Reload(); err != nil {
			fmt.Fprintln(os.Stderr, "mcp door: reload:", err)
		}
	}

	// track registers a cancellable request under id (empty id: no
	// bookkeeping). A duplicate id is refused; an id cancelled earlier yields
	// context.Canceled. release must be deferred.
	track := func(ctx context.Context, id string) (context.Context, func(), error) {
		reqCtx, cancel := context.WithCancel(ctx)
		if id == "" {
			return reqCtx, cancel, nil
		}
		mu.Lock()
		if _, dup := inflight[id]; dup {
			mu.Unlock()
			cancel()
			return nil, nil, &rpc.Error{Code: -32602, Message: "duplicate requestId"}
		}
		early := cancelled[id]
		delete(cancelled, id)
		inflight[id] = cancel
		mu.Unlock()
		release := func() { cancel(); mu.Lock(); delete(inflight, id); mu.Unlock() }
		if early {
			release()
			return nil, nil, context.Canceled
		}
		return reqCtx, release, nil
	}

	// listServers, exec and sudoExec are request-only: a notification-form
	// exec would otherwise block the read loop for the whole approval wait.
	s := rpc.NewServer()
	s.HandleRequest("listServers", func(context.Context, json.RawMessage) (any, error) {
		reload()
		list := h.ServersForMCP()
		if list == nil {
			list = []ServerInfo{}
		}
		return list, nil
	})
	// Stdin travels only on execStdin, so a hub that predates it answers
	// "method not found" instead of running the command without its stdin.
	exec := func(sudo, withStdin bool) rpc.Handler {
		// ctx is cancelled by rpc.Server when the connection closes, which
		// withdraws the pending approval or stops the running command.
		return func(ctx context.Context, raw json.RawMessage) (any, error) {
			var p execParams
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, &rpc.Error{Code: -32602, Message: "invalid params"}
			}
			if p.Stdin != "" && !withStdin {
				return nil, &rpc.Error{Code: -32602, Message: "stdin is sent with execStdin"}
			}
			reqCtx, release, err := track(ctx, p.RequestID)
			if err != nil {
				return nil, err
			}
			defer release()
			reload()
			return h.Exec(reqCtx, ExecRequest{Client: p.Client, Server: p.Server, Command: p.Command, Description: p.Description, Stdin: p.Stdin, Sudo: sudo, TimeoutSec: p.TimeoutSec})
		}
	}
	s.HandleRequest("exec", exec(false, false))
	s.HandleRequest("execStdin", exec(false, true))
	s.HandleRequest("sudoExec", exec(true, false))
	transfer := func(op files.Op) rpc.Handler {
		return func(ctx context.Context, raw json.RawMessage) (any, error) {
			var p struct {
				RequestID   string   `json:"requestId"`
				Client      string   `json:"client"`
				Server      string   `json:"server"`
				Root        string   `json:"root"`
				Local       []string `json:"local"`
				Remote      []string `json:"remote"`
				Overwrite   bool     `json:"overwrite"`
				Description string   `json:"description"`
				TimeoutSec  int      `json:"timeoutSec"`
			}
			if err := strictParams(raw, &p, "requestId", "client", "server", "root", "local", "remote", "overwrite", "description", "timeoutSec"); err != nil {
				return nil, err
			}
			reqCtx, release, err := track(ctx, p.RequestID)
			if err != nil {
				return nil, err
			}
			defer release()
			reload()
			return h.Transfer(reqCtx, TransferRequest{Client: p.Client, Server: p.Server, Op: op, Root: p.Root, Local: p.Local, Remote: p.Remote,
				Overwrite: p.Overwrite, Description: p.Description, TimeoutSec: p.TimeoutSec})
		}
	}
	s.HandleRequest("upload", transfer(files.OpUpload))
	s.HandleRequest("download", transfer(files.OpDownload))
	// cancel may arrive as a notification, which runs on the read loop: it
	// only takes a mutex and calls a CancelFunc, so it never blocks.
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
		} else if p.RequestID != "" && !cancelled[p.RequestID] {
			cancelled[p.RequestID] = true
			cancelOrder = append(cancelOrder, p.RequestID)
			if len(cancelOrder) > maxEarlyCancels {
				delete(cancelled, cancelOrder[0])
				cancelOrder = cancelOrder[1:]
			}
		}
		mu.Unlock()
		return map[string]bool{"ok": true}, nil
	})
	_ = s.Serve(ctx, conn, conn)
}
