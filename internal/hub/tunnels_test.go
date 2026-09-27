package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lang315/sshgate/internal/rpc"
)

type tunnelRow struct {
	Server     string `json:"server"`
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	ListenPort int    `json:"listenPort"`
	Status     string `json:"status"`
	Error      string `json:"error"`
	Conns      int    `json:"conns"`
}

func tunnelEcho(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func tunnelFreePort(t *testing.T) int {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func saveTunnel(t *testing.T, fx filesFixture, server string, tn map[string]any) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	if err := fx.c.Call(context.Background(), "tunnels.save", map[string]any{"server": server, "tunnel": tn}, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.ID) != 16 {
		t.Fatalf("id %q", out.ID)
	}
	return out.ID
}

func listTunnels(t *testing.T, fx filesFixture) []tunnelRow {
	t.Helper()
	var rows []tunnelRow
	if err := fx.c.Call(context.Background(), "tunnels.list", map[string]any{}, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func echoThrough(t *testing.T, port int) {
	t.Helper()
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("ping"))
	b := make([]byte, 4)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c, b); err != nil || string(b) != "ping" {
		t.Fatalf("echo %q %v", b, err)
	}
}

func tunnelsHub(t *testing.T) filesFixture {
	fx := filesHub(t)
	t.Cleanup(fx.h.Close) // Close ends running tunnels
	return fx
}

func TestTunnelSaveListStartStopDelete(t *testing.T) {
	fx := tunnelsHub(t)
	ctx := context.Background()
	lp := tunnelFreePort(t)
	id := saveTunnel(t, fx, "fs", map[string]any{"id": "", "kind": "local", "listenPort": lp,
		"targetHost": "127.0.0.1", "targetPort": tunnelEcho(t), "label": "echo"})
	if rows := listTunnels(t, fx); len(rows) != 1 || rows[0].Status != "stopped" || rows[0].Server != "fs" {
		t.Fatalf("%+v", rows)
	}
	if err := fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "fs", "id": id}, nil); err != nil {
		t.Fatal(err)
	}
	echoThrough(t, lp)
	// starting, then running.
	if st := waitNote(t, fx.notes, "tunnels.state", id); st["status"] != "starting" {
		t.Fatalf("%v", st)
	}
	if st := waitNote(t, fx.notes, "tunnels.state", id); st["status"] != "running" {
		t.Fatalf("%v", st)
	}
	// A running tunnel cannot be edited.
	err := fx.c.Call(ctx, "tunnels.save", map[string]any{"server": "fs", "tunnel": map[string]any{"id": id, "kind": "dynamic", "listenPort": lp}}, nil)
	if err == nil || !strings.Contains(err.Error(), "stop the tunnel first") {
		t.Fatalf("edit while running: %v", err)
	}
	// It keeps running across a lock; stop works while locked.
	fx.h.Lock()
	echoThrough(t, lp)
	if err := fx.c.Call(ctx, "tunnels.save", map[string]any{"server": "fs", "tunnel": map[string]any{"id": "", "kind": "dynamic", "listenPort": tunnelFreePort(t)}}, nil); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("tunnels.save while locked: %v", err)
	}
	sendNote(t, fx.w, "tunnels.stop", map[string]any{"server": "fs", "id": id})
	for st := waitNote(t, fx.notes, "tunnels.state", id); st["status"] != "stopped"; st = waitNote(t, fx.notes, "tunnels.state", id) {
	}
	if _, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(lp)); err == nil {
		t.Fatal("still listening after stop")
	}
	// Start, save, delete need the vault unlocked.
	for _, m := range []string{"tunnels.start", "tunnels.delete"} {
		if err := fx.c.Call(ctx, m, map[string]any{"server": "fs", "id": id}, nil); err == nil || !strings.Contains(err.Error(), "locked") {
			t.Fatalf("%s while locked: %v", m, err)
		}
	}
	unlockForTest(fx.h)
	if err := fx.c.Call(ctx, "tunnels.delete", map[string]any{"server": "fs", "id": id}, nil); err != nil {
		t.Fatal(err)
	}
	if rows := listTunnels(t, fx); len(rows) != 0 {
		t.Fatalf("%+v", rows)
	}
	_, recs := readAudit(t, fx.store)
	var phases []string
	for _, r := range recs {
		if r["kind"] == "tunnel" {
			phases = append(phases, r["phase"].(string))
		}
	}
	if strings.Join(phases, ",") != "start,end" {
		t.Fatalf("tunnel audit %v", phases)
	}
}

func TestTunnelStartRefusals(t *testing.T) {
	fx := tunnelsHub(t)
	ctx := context.Background()
	id := saveTunnel(t, fx, "new", map[string]any{"id": "", "kind": "dynamic", "listenPort": tunnelFreePort(t)})
	err := fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "new", "id": id}, nil)
	if err == nil || err.Error() != "open a terminal to this host once to trust its host key" {
		t.Fatalf("unpinned: %v", err)
	}
	busy, _ := net.Listen("tcp", "127.0.0.1:0")
	defer busy.Close()
	bp := busy.Addr().(*net.TCPAddr).Port
	id2 := saveTunnel(t, fx, "fs", map[string]any{"id": "", "kind": "dynamic", "listenPort": bp})
	err = fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "fs", "id": id2}, nil)
	if err == nil || err.Error() != "port "+strconv.Itoa(bp)+" is already in use" {
		t.Fatalf("busy port: %v", err)
	}
	// tunnels.list is not ordered by save or error time; find id2 by ID
	// rather than assuming it sorts last (it does not: rows follow the
	// vault's server order, "fs" then "new", so id2's row comes first).
	rows := listTunnels(t, fx)
	i := slices.IndexFunc(rows, func(r tunnelRow) bool { return r.ID == id2 })
	if i < 0 || rows[i].Status != "error" {
		t.Fatalf("%+v", rows)
	}
	// A failed start is audited too: kind "tunnel", phase "end", with the
	// detail (not the masked message) as the reason.
	_, recs := readAudit(t, fx.store)
	found := false
	for _, r := range recs {
		if r["kind"] == "tunnel" && r["id"] == id2 {
			if r["phase"] != "end" {
				t.Fatalf("failed-start audit phase: %+v", r)
			}
			reason, _ := r["reason"].(string)
			if reason == "" || reason == "port "+strconv.Itoa(bp)+" is already in use" {
				t.Fatalf("failed-start audit reason should be the detail, not the masked message: %+v", r)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("no tunnel audit record for failed start of %s", id2)
	}
	fx.srv.RefuseForward()
	id3 := saveTunnel(t, fx, "fs", map[string]any{"id": "", "kind": "remote", "listenPort": tunnelFreePort(t), "targetHost": "127.0.0.1", "targetPort": 1})
	err = fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "fs", "id": id3}, nil)
	if err == nil || err.Error() != "the server refused the remote forward" {
		t.Fatalf("refused remote: %v", err)
	}
	if err := fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "fs", "id": "0000000000000000"}, nil); err == nil || err.Error() != "tunnel not found" {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestTunnelEndedByServerChangeAndConnectionLoss(t *testing.T) {
	fx := tunnelsHub(t)
	ctx := context.Background()
	start := func() string {
		id := saveTunnel(t, fx, "fs", map[string]any{"id": "", "kind": "dynamic", "listenPort": tunnelFreePort(t)})
		if err := fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "fs", "id": id}, nil); err != nil {
			t.Fatal(err)
		}
		return id
	}
	waitErr := func(id, want string) {
		t.Helper()
		for {
			st := waitNote(t, fx.notes, "tunnels.state", id)
			if st["status"] == "error" {
				if st["error"] != want {
					t.Fatalf("error %v, want %s", st["error"], want)
				}
				return
			}
		}
	}
	id := start()
	fx.h.Registry().Close("fs") // the client dies
	waitErr(id, "connection lost")

	id = start()
	// Change the port in the vault: the hub ends the tunnel before closing the connection.
	if err := fx.c.Call(ctx, "servers.save", map[string]any{"original": "fs", "server": map[string]any{
		"name": "fs", "host": fx.srv.Host, "port": fx.srv.Port + 1, "user": "u", "auth": "agent", "keyPath": "", "aiVisible": false}}, nil); err != nil {
		t.Fatal(err)
	}
	waitErr(id, "server changed")
	// The tunnels are still saved on the server.
	if rows := listTunnels(t, fx); len(rows) != 2 {
		t.Fatalf("tunnels lost on save: %+v", rows)
	}
}

// TestTunnelStartingEndedByServerDeleteDuringDial: a servers.delete landing
// while StartTunnel's dial is still in flight (status "starting") must end
// the tunnel and close whatever it goes on to bind, rather than leaving an
// untracked listener nothing can stop (round 1 review finding).
func TestTunnelStartingEndedByServerDeleteDuringDial(t *testing.T) {
	fx := tunnelsHub(t)
	ctx := context.Background()
	lp := tunnelFreePort(t)
	id := saveTunnel(t, fx, "fs", map[string]any{"id": "", "kind": "dynamic", "listenPort": lp})

	done := make(chan error, 1)
	afterTunnelDialed = func() {
		done <- fx.c.Call(context.Background(), "servers.delete", map[string]any{"name": "fs"}, nil)
	}
	t.Cleanup(func() { afterTunnelDialed = nil })

	err := fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "fs", "id": id}, nil)
	if err == nil || err.Error() != "server changed" {
		t.Fatalf("start racing a server delete: %v", err)
	}
	if delErr := <-done; delErr != nil {
		t.Fatal(delErr)
	}
	if _, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(lp)); err == nil {
		t.Fatal("still listening after a server delete raced the dial")
	}
}

func TestTunnelStrictParamsAndValidation(t *testing.T) {
	fx := tunnelsHub(t)
	ctx := context.Background()
	for _, c := range []struct{ method, raw string }{
		{"tunnels.start", `{"server":"fs","ID":"x"}`},
		{"tunnels.start", `{"server":"fs","id":"x","id":"y"}`},
		{"tunnels.save", `{"server":"fs","tunnel":{"kind":"dynamic","listenPort":0}}`},
		{"tunnels.list", `{"server":"fs"}`},
	} {
		var re *rpc.Error
		if err := fx.c.Call(ctx, c.method, json.RawMessage(c.raw), nil); !errors.As(err, &re) || re.Code != -32602 {
			t.Errorf("%s %s: %v", c.method, c.raw, err)
		}
	}
	lp := tunnelFreePort(t)
	saveTunnel(t, fx, "fs", map[string]any{"id": "", "kind": "dynamic", "listenPort": lp})
	err := fx.c.Call(ctx, "tunnels.save", map[string]any{"server": "fs", "tunnel": map[string]any{"id": "", "kind": "dynamic", "listenPort": lp}}, nil)
	if err == nil || err.Error() != "another tunnel already listens on dynamic port "+strconv.Itoa(lp) {
		t.Fatalf("dup: %v", err)
	}
}

// tunnelEnds counts id's "end" tunnel audit records.
func tunnelEnds(t *testing.T, store, id string) int {
	t.Helper()
	_, recs := readAudit(t, store)
	n := 0
	for _, r := range recs {
		if r["kind"] == "tunnel" && r["id"] == id && r["phase"] == "end" {
			n++
		}
	}
	return n
}

// lastTunnelState waits for a "stopped" tunnels.state for id, then returns
// the last one that arrives within a quiet period after it.
func lastTunnelState(t *testing.T, fx filesFixture, id string) string {
	t.Helper()
	for st := waitNote(t, fx.notes, "tunnels.state", id); st["status"] != "stopped"; st = waitNote(t, fx.notes, "tunnels.state", id) {
	}
	last := "stopped"
	for {
		select {
		case n := <-fx.notes:
			var p map[string]any
			json.Unmarshal(n.params, &p)
			if n.method == "tunnels.state" && p["id"] == id {
				last, _ = p["status"].(string)
			}
		case <-time.After(300 * time.Millisecond):
			return last
		}
	}
}

// TestTunnelStopWhileStarting: a tunnels.stop landing while the start is
// still in flight ends it once: the start answers without an error, the
// last tunnels.state is "stopped", one end audit record, the port closed.
func TestTunnelStopWhileStarting(t *testing.T) {
	fx := tunnelsHub(t)
	lp := tunnelFreePort(t)
	id := saveTunnel(t, fx, "fs", map[string]any{"id": "", "kind": "dynamic", "listenPort": lp})
	afterTunnelDialed = func() { sendNote(t, fx.w, "tunnels.stop", map[string]any{"server": "fs", "id": id}) }
	t.Cleanup(func() { afterTunnelDialed = nil })
	if err := fx.c.Call(context.Background(), "tunnels.start", map[string]any{"server": "fs", "id": id}, nil); err != nil {
		t.Fatalf("start stopped while starting: %v", err)
	}
	if st := lastTunnelState(t, fx, id); st != "stopped" {
		t.Fatalf("last tunnels.state %q, want stopped", st)
	}
	if n := tunnelEnds(t, fx.store, id); n != 1 {
		t.Fatalf("%d end audit records, want 1", n)
	}
	if _, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(lp)); err == nil {
		t.Fatal("still listening after stop")
	}
	if rows := listTunnels(t, fx); len(rows) != 1 || rows[0].Status != "stopped" {
		t.Fatalf("%+v", rows)
	}
}

// TestTunnelDeleteRunning: tunnels.delete of a running tunnel ends it.
func TestTunnelDeleteRunning(t *testing.T) {
	fx := tunnelsHub(t)
	ctx := context.Background()
	lp := tunnelFreePort(t)
	id := saveTunnel(t, fx, "fs", map[string]any{"id": "", "kind": "dynamic", "listenPort": lp})
	if err := fx.c.Call(ctx, "tunnels.start", map[string]any{"server": "fs", "id": id}, nil); err != nil {
		t.Fatal(err)
	}
	if err := fx.c.Call(ctx, "tunnels.delete", map[string]any{"server": "fs", "id": id}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(lp)); err == nil {
		t.Fatal("still listening after delete")
	}
	if n := tunnelEnds(t, fx.store, id); n != 1 {
		t.Fatalf("%d end audit records, want 1", n)
	}
	if rows := listTunnels(t, fx); len(rows) != 0 {
		t.Fatalf("%+v", rows)
	}
}
