package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
	"github.com/lang315/sshgate/internal/sshx"
	"github.com/lang315/sshgate/internal/tunnel"
)

// liveTunnel is a tunnel that is starting, running, or ended with an error
// (kept so tunnels.list can show why). A stopped tunnel has no entry.
type liveTunnel struct {
	server string
	def    config.Tunnel
	target string // user@host:port, for audit
	status string // starting, running, error
	err    string
	fwd    *tunnel.Forward
	notify *time.Timer // pending conns-only tunnels.state
}

type tunnelSet struct {
	mu      sync.Mutex
	live    map[string]*liveTunnel // server + "\x00" + id
	sink    func(tunnelState)
	sinkGen uint64
}

type tunnelState struct {
	Server string `json:"server"`
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Conns  int    `json:"conns"`
}

func newTunnelSet() *tunnelSet { return &tunnelSet{live: map[string]*liveTunnel{}} }

func tunnelKey(server, id string) string { return server + "\x00" + id }

func (lt *liveTunnel) state() tunnelState {
	st := tunnelState{Server: lt.server, ID: lt.def.ID, Status: lt.status, Error: lt.err}
	if lt.fwd != nil && lt.status == "running" {
		st.Conns = lt.fwd.Conns()
	}
	return st
}

// setSink works like the hub's setLockSink.
func (ts *tunnelSet) setSink(f func(tunnelState)) (release func()) {
	ts.mu.Lock()
	ts.sink = f
	ts.sinkGen++
	gen := ts.sinkGen
	ts.mu.Unlock()
	return func() {
		ts.mu.Lock()
		if ts.sinkGen == gen {
			ts.sink = nil
		}
		ts.mu.Unlock()
	}
}

func (ts *tunnelSet) emit(st tunnelState) {
	ts.mu.Lock()
	sink := ts.sink
	ts.mu.Unlock()
	if sink != nil {
		sink(st)
	}
}

// connsChanged sends at most one conns-only tunnels.state a second.
func (ts *tunnelSet) connsChanged(key string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	lt := ts.live[key]
	if lt == nil || lt.notify != nil {
		return
	}
	lt.notify = time.AfterFunc(time.Second, func() {
		ts.mu.Lock()
		cur := ts.live[key]
		if cur != lt {
			ts.mu.Unlock()
			return
		}
		lt.notify = nil
		st := lt.state()
		ts.mu.Unlock()
		ts.emit(st)
	})
}

// end stops lt if it is still the live entry for its key, whether it is
// running or still starting (a dial in flight has no fwd yet, so there is
// nothing to Close there, but the entry must still be ended rather than
// left to finish invisibly against a deleted or edited server). reason ""
// means stopped by the author (entry removed); anything else leaves an
// error entry, so a StartTunnel still in flight can see why it was cut off.
func (h *Hub) endTunnel(lt *liveTunnel, reason string) {
	ts := h.tunnels
	key := tunnelKey(lt.server, lt.def.ID)
	ts.mu.Lock()
	if ts.live[key] != lt || (lt.status != "running" && lt.status != "starting") {
		ts.mu.Unlock()
		return
	}
	if lt.notify != nil {
		lt.notify.Stop()
		lt.notify = nil
	}
	fwd := lt.fwd // nil while still starting
	if reason == "" {
		delete(ts.live, key)
		lt.status = "stopped"
	} else {
		lt.status, lt.err = "error", reason
	}
	st := lt.state()
	ts.mu.Unlock()
	conns := 0
	if fwd != nil {
		fwd.Close()
		conns = fwd.Total()
	}
	auditReason := reason
	if auditReason == "" {
		auditReason = "stopped"
	}
	h.auditTunnel(broker.TunnelRecord{Phase: "end", Server: lt.server, Target: lt.target, ID: lt.def.ID,
		TunnelKind: lt.def.Kind, Listen: listenAddr(lt.def), To: toAddr(lt.def), Conns: conns, Reason: auditReason})
	ts.emit(st)
}

// endServer ends every running tunnel of server with reason. Callers run it
// before closing the server's connection, as with file jobs.
func (h *Hub) endServerTunnels(server, reason string) {
	h.tunnels.mu.Lock()
	var lts []*liveTunnel
	for _, lt := range h.tunnels.live {
		if lt.server == server {
			lts = append(lts, lt)
		}
	}
	h.tunnels.mu.Unlock()
	for _, lt := range lts {
		h.endTunnel(lt, reason)
	}
}

func (h *Hub) endAllTunnels(reason string) {
	h.tunnels.mu.Lock()
	lts := make([]*liveTunnel, 0, len(h.tunnels.live))
	for _, lt := range h.tunnels.live {
		lts = append(lts, lt)
	}
	h.tunnels.mu.Unlock()
	for _, lt := range lts {
		h.endTunnel(lt, reason)
	}
}

func (h *Hub) auditTunnel(r broker.TunnelRecord) {
	if h.audit != nil {
		r.Time = time.Now()
		_ = h.audit.WriteTunnel(r)
	}
}

func listenAddr(t config.Tunnel) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(t.ListenPort))
}

func toAddr(t config.Tunnel) string {
	if t.Kind == "dynamic" {
		return ""
	}
	return net.JoinHostPort(t.TargetHost, strconv.Itoa(t.TargetPort))
}

func newTunnelID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// savedTunnel finds server's tunnel id in the loaded store.
func (h *Hub) savedTunnel(server, id string) (config.Tunnel, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.deps.File == nil {
		return config.Tunnel{}, false
	}
	for _, s := range h.deps.File.Servers {
		if s.Name == server {
			for _, t := range s.Tunnels {
				if t.ID == id {
					return t, true
				}
			}
		}
	}
	return config.Tunnel{}, false
}

type tunnelView struct {
	Server string `json:"server"`
	config.Tunnel
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Conns  int    `json:"conns"`
}

func (h *Hub) listTunnels() []tunnelView {
	out := []tunnelView{}
	h.mu.Lock()
	var defs []tunnelView
	if h.deps.File != nil && h.deps.File.KDF != nil {
		for _, s := range h.deps.File.Servers {
			for _, t := range s.Tunnels {
				defs = append(defs, tunnelView{Server: s.Name, Tunnel: t, Status: "stopped"})
			}
		}
	}
	h.mu.Unlock()
	h.tunnels.mu.Lock()
	defer h.tunnels.mu.Unlock()
	for _, v := range defs {
		if lt := h.tunnels.live[tunnelKey(v.Server, v.ID)]; lt != nil {
			st := lt.state()
			v.Status, v.Error, v.Conns = st.Status, st.Error, st.Conns
		}
		out = append(out, v)
	}
	return out
}

func (h *Hub) tunnelRunning(server, id string) bool {
	h.tunnels.mu.Lock()
	defer h.tunnels.mu.Unlock()
	lt := h.tunnels.live[tunnelKey(server, id)]
	return lt != nil && (lt.status == "running" || lt.status == "starting")
}

// SaveTunnel adds (empty ID) or replaces one of server's tunnels. It refuses
// to edit one that is running or starting, but that check is not atomic
// with the write below: a tunnels.start can still land in between and claim
// the id. So the cleanup here only ever clears a stale "error" entry (the
// last start attempt's failure); it never removes a running or starting
// entry, which would drop the hub's only handle on its Forward and orphan
// the listener.
func (h *Hub) SaveTunnel(server string, t config.Tunnel) (config.Tunnel, error) {
	key, err := h.writeKey()
	if err != nil {
		return t, err
	}
	defer clear(key)
	if t.ID != "" && h.tunnelRunning(server, t.ID) {
		return t, errors.New("stop the tunnel first")
	}
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		i := slices.IndexFunc(f.Servers, func(s config.Server) bool { return s.Name == server })
		if i < 0 {
			return serverNotFound(server)
		}
		s := &f.Servers[i]
		for _, o := range s.Tunnels {
			if o.ID != t.ID && o.Kind == t.Kind && o.ListenPort == t.ListenPort {
				return fmt.Errorf("another tunnel already listens on %s port %d", t.Kind, t.ListenPort)
			}
		}
		if t.ID == "" {
			if len(s.Tunnels) >= config.MaxTunnels {
				return errors.New("a server holds at most 32 tunnels")
			}
			t.ID = newTunnelID()
			s.Tunnels = append(s.Tunnels, t)
			return nil
		}
		j := slices.IndexFunc(s.Tunnels, func(o config.Tunnel) bool { return o.ID == t.ID })
		if j < 0 {
			return errors.New("tunnel not found")
		}
		s.Tunnels[j] = t
		return nil
	})
	if err != nil {
		return t, err
	}
	reloadErr := h.Reload()
	h.tunnels.mu.Lock()
	if cur := h.tunnels.live[tunnelKey(server, t.ID)]; cur != nil && cur.status == "error" {
		delete(h.tunnels.live, tunnelKey(server, t.ID)) // an old error entry
	}
	h.tunnels.mu.Unlock()
	h.auditConfig(broker.ConfigRecord{Action: "tunnelSave", Server: server,
		Changed: []string{fmt.Sprintf("%s %s %s → %s", t.ID, t.Kind, listenAddr(t), toAddr(t))}})
	return t, reloadErr
}

func (h *Hub) DeleteTunnel(server, id string) error {
	key, err := h.writeKey()
	if err != nil {
		return err
	}
	defer clear(key)
	h.tunnels.mu.Lock()
	lt := h.tunnels.live[tunnelKey(server, id)]
	h.tunnels.mu.Unlock()
	if lt != nil {
		h.endTunnel(lt, "")
	}
	err = config.Update(h.o.StorePath, key, func(f *config.File) error {
		i := slices.IndexFunc(f.Servers, func(s config.Server) bool { return s.Name == server })
		if i < 0 {
			return serverNotFound(server)
		}
		s := &f.Servers[i]
		j := slices.IndexFunc(s.Tunnels, func(o config.Tunnel) bool { return o.ID == id })
		if j < 0 {
			return errors.New("tunnel not found")
		}
		s.Tunnels = slices.Delete(s.Tunnels, j, j+1)
		return nil
	})
	if err != nil {
		return err
	}
	reloadErr := h.Reload()
	// A tunnels.start may have raced in between our endTunnel above and the
	// write and claimed a fresh live entry for this id; end that one too
	// (endTunnel closes its Forward) rather than dropping it unclosed below.
	h.tunnels.mu.Lock()
	lt = h.tunnels.live[tunnelKey(server, id)]
	h.tunnels.mu.Unlock()
	if lt != nil {
		h.endTunnel(lt, "")
	}
	h.tunnels.mu.Lock()
	delete(h.tunnels.live, tunnelKey(server, id)) // any leftover error entry
	h.tunnels.mu.Unlock()
	h.auditConfig(broker.ConfigRecord{Action: "tunnelDelete", Server: server, Changed: []string{id}})
	return reloadErr
}

// afterTunnelDialed runs, if set, right after a tunnel's dial succeeds and
// before its listener is created — a test seam for driving a concurrent
// servers.delete/servers.save/tunnels.delete while the tunnel is still
// "starting" (see filejobs.go's afterExpiryDecided for the same pattern).
var afterTunnelDialed func()

// StartTunnel checks, in order: vault, unlocked, server, tunnel, not
// already running, pin. It answers once the forward listens.
func (h *Hub) StartTunnel(server, id string) error {
	_ = h.Reload()
	dc, err := h.resolveForTerm(server)
	if err != nil {
		return err
	}
	def, ok := h.savedTunnel(server, id)
	if !ok {
		return errors.New("tunnel not found")
	}
	key := tunnelKey(server, id)
	lt := &liveTunnel{server: server, def: def, target: fmt.Sprintf("%s@%s:%d", dc.User, dc.Host, dc.Port), status: "starting"}
	h.tunnels.mu.Lock()
	if cur := h.tunnels.live[key]; cur != nil && (cur.status == "running" || cur.status == "starting") {
		h.tunnels.mu.Unlock()
		return errors.New("the tunnel is already running")
	}
	h.tunnels.live[key] = lt
	h.tunnels.mu.Unlock()
	h.tunnels.emit(lt.state())

	fail := func(msg string, detail error) error {
		if detail != nil {
			fmt.Fprintf(os.Stderr, "tunnel %s/%s: %v\n", server, id, detail)
		}
		h.tunnels.mu.Lock()
		if h.tunnels.live[key] == lt {
			lt.status, lt.err = "error", msg
		}
		st := lt.state()
		h.tunnels.mu.Unlock()
		reason := msg
		if detail != nil {
			reason = detail.Error()
		}
		h.auditTunnel(broker.TunnelRecord{Phase: "end", Server: server, Target: lt.target, ID: id,
			TunnelKind: def.Kind, Listen: listenAddr(def), To: toAddr(def), Reason: reason})
		h.tunnels.emit(st)
		return errors.New(msg)
	}
	if dc.HostKey == "" {
		return fail("open a terminal to this host once to trust its host key", nil)
	}
	mgr := h.Registry().Get(server, dc)
	mgr.StartKeepalive(30*time.Second, nil)
	client, err := mgr.Client()
	var unknown *sshx.HostKeyUnknownError
	switch {
	case errors.As(err, &unknown):
		return fail("open a terminal to this host once to trust its host key", err)
	case errors.Is(err, sshx.ErrHostKeyMismatch):
		return fail("host key changed", err)
	case err != nil:
		return fail("connection failed", err)
	}
	if afterTunnelDialed != nil {
		afterTunnelDialed()
	}
	onConns := func() { h.tunnels.connsChanged(key) }
	var fwd *tunnel.Forward
	switch def.Kind {
	case "local":
		fwd, err = tunnel.Local(client, listenAddr(def), toAddr(def), onConns)
	case "remote":
		fwd, err = tunnel.Remote(client, listenAddr(def), toAddr(def), onConns)
	default:
		fwd, err = tunnel.Dynamic(client, listenAddr(def), onConns)
	}
	switch {
	// ponytail: on Windows the OS error is WSAEADDRINUSE, which
	// errors.Is(err, syscall.EADDRINUSE) may not match; the fallback below
	// (err.Error()) then surfaces the OS text instead of this message.
	case err != nil && errors.Is(err, syscall.EADDRINUSE):
		return fail(fmt.Sprintf("port %d is already in use", def.ListenPort), err)
	case err != nil && def.Kind == "remote":
		return fail("the server refused the remote forward", err)
	case err != nil:
		return fail(err.Error(), err)
	}
	h.tunnels.mu.Lock()
	if h.tunnels.live[key] != lt { // deleted or replaced meanwhile
		h.tunnels.mu.Unlock()
		fwd.Close()
		return errors.New("tunnel not found")
	}
	if lt.status != "starting" { // ended (server changed) while the dial ran
		reason := lt.err
		h.tunnels.mu.Unlock()
		fwd.Close()
		if reason == "" {
			reason = "tunnel not found"
		}
		return errors.New(reason)
	}
	lt.fwd, lt.status = fwd, "running"
	st := lt.state()
	h.tunnels.mu.Unlock()
	h.auditTunnel(broker.TunnelRecord{Phase: "start", Server: server, Target: lt.target, ID: id,
		TunnelKind: def.Kind, Listen: listenAddr(def), To: toAddr(def)})
	h.tunnels.emit(st)
	go func() { client.Wait(); h.endTunnel(lt, "connection lost") }()
	go func() {
		<-fwd.Done()
		if fwd.Err() != nil {
			h.endTunnel(lt, "connection lost")
		}
	}()
	return nil
}

func (h *Hub) StopTunnel(server, id string) {
	h.tunnels.mu.Lock()
	lt := h.tunnels.live[tunnelKey(server, id)]
	h.tunnels.mu.Unlock()
	if lt != nil {
		h.endTunnel(lt, "")
	}
}

// registerTunnelMethods adds tunnels.* to one UI door. Running tunnels are
// the hub's, not the door's: closing the door leaves them running.
func registerTunnelMethods(s *rpc.Server, h *Hub) (release func()) {
	release = h.tunnels.setSink(func(st tunnelState) { s.Notify("tunnels.state", st) })
	type ref struct {
		Server string `json:"server"`
		ID     string `json:"id"`
	}
	s.HandleRequest("tunnels.list", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct{}
		if err := strictParams(raw, &p); err != nil {
			return nil, err
		}
		_ = h.Reload()
		return h.listTunnels(), nil
	})
	s.HandleRequest("tunnels.save", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct {
			Server string          `json:"server"`
			Tunnel json.RawMessage `json:"tunnel"`
		}
		if err := strictParams(raw, &p, "server", "tunnel"); err != nil {
			return nil, err
		}
		var t config.Tunnel
		if err := strictParams(p.Tunnel, &t, "id", "kind", "listenPort", "targetHost", "targetPort", "label"); err != nil {
			return nil, err
		}
		if err := t.Validate(); err != nil {
			return nil, &rpc.Error{Code: -32602, Message: err.Error()}
		}
		return h.SaveTunnel(p.Server, t)
	})
	s.HandleRequest("tunnels.delete", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p ref
		if err := strictParams(raw, &p, "server", "id"); err != nil {
			return nil, err
		}
		return map[string]any{}, h.DeleteTunnel(p.Server, p.ID)
	})
	s.HandleRequest("tunnels.start", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p ref
		if err := strictParams(raw, &p, "server", "id"); err != nil {
			return nil, err
		}
		return map[string]any{}, h.StartTunnel(p.Server, p.ID)
	})
	s.Handle("tunnels.stop", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p ref
		if err := strictParams(raw, &p, "server", "id"); err != nil {
			return nil, err
		}
		h.StopTunnel(p.Server, p.ID)
		return map[string]any{}, nil
	})
	return release
}
