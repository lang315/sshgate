//go:build unix

package view

import (
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// echoServer serves 127.0.0.1:0, writing back what each connection sends,
// until the test ends.
func echoServer(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

// freePort is a loopback port nothing listens on right now.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func loopback(port int) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) }

// connect opens a connection to 127.0.0.1:port, closed when the test ends.
func connect(t *testing.T, port int) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", loopback(port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// readN reads n bytes from c, failing after 15 s.
func readN(t *testing.T, c net.Conn, n int) []byte {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(15 * time.Second))
	b := make([]byte, n)
	if _, err := io.ReadFull(c, b); err != nil {
		t.Fatalf("read %d bytes: %v", n, err)
	}
	return b
}

// pingOn sends "ping" on c and wants it back (the e2e's ping).
func pingOn(t *testing.T, c net.Conn) {
	t.Helper()
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if b := readN(t, c, 4); string(b) != "ping" {
		t.Fatalf("got %q", b)
	}
}

// socks opens a SOCKS5 CONNECT to 127.0.0.1:target through the proxy on port.
func socks(t *testing.T, port, target int) net.Conn {
	t.Helper()
	c := connect(t, port)
	c.Write([]byte{5, 1, 0})
	if b := readN(t, c, 2); b[0] != 5 || b[1] != 0 {
		t.Fatalf("greeting %v", b)
	}
	c.Write([]byte{5, 1, 0, 1, 127, 0, 0, 1, byte(target >> 8), byte(target)})
	if b := readN(t, c, 10); b[1] != 0 {
		t.Fatalf("connect reply %v", b)
	}
	return c
}

// tunnelsFlow is an unlocked flow in a window tall enough for the dialog.
func tunnelsFlow(t *testing.T) *flowT {
	f := flow(t)
	f.tt.SetSize(1000, 900)
	f.unlock()
	return f
}

// openTunnels opens server's Tunnels tab from its card.
func (f *flowT) openTunnels(server string) {
	f.t.Helper()
	if f.a.tabs.Active != "" {
		f.click("Hosts")
	}
	f.click("Tunnels " + model.DisplayText(server))
	f.until(func() bool { return f.tt.HasText("Tunnels on " + model.DisplayText(server)) }, "the Tunnels tab")
}

// addTunnel fills the Add tunnel dialog of server's tab, as the e2e's
// addTunnel, and waits for the row; target 0 leaves the target out.
func (f *flowT) addTunnel(server, kind string, listen int, targetHost string, target int, label string) {
	f.t.Helper()
	f.click("Add tunnel")
	f.until(func() bool { return f.tt.HasText("Listens on 127.0.0.1 only.") }, "the Add tunnel dialog")
	f.click(kind)
	f.fill("Listen port", strconv.Itoa(listen))
	if target != 0 {
		f.fill("Target host", targetHost)
		f.fill("Target port", strconv.Itoa(target))
	}
	f.fill("Label", label)
	f.click("Save")
	f.untilFor(time.Minute, func() bool { _, ok := f.tunnel(server, label); return ok && f.tt.HasText("Start "+label) }, "the "+label+" row")
}

// tunnel is server's tunnel labelled label, as the app holds it.
func (f *flowT) tunnel(server, label string) (hubapi.TunnelView, bool) {
	for _, x := range f.a.tunnels {
		if x.Server == server && x.Label == label {
			return x, true
		}
	}
	return hubapi.TunnelView{}, false
}

// tunnelIs waits until server's row label is in status (its error text,
// for an error) and its Start or Stop shows. A minute: a real host may be
// slow to dial.
func (f *flowT) tunnelIs(server, label, status string) {
	f.t.Helper()
	button := "Start "
	if model.TunnelLive(status) {
		button = "Stop "
	}
	f.untilFor(time.Minute, func() bool {
		x, _ := f.tunnel(server, label)
		shown := x.Status
		if shown == "error" {
			shown = x.Error
		}
		return shown == status && f.tt.HasText(button+label) && f.tt.HasText(status)
	}, label+" "+status)
}

// refused fails unless nothing listens on 127.0.0.1:port.
func refused(t *testing.T, port int) {
	t.Helper()
	if c, err := net.DialTimeout("tcp", loopback(port), time.Second); err == nil {
		c.Close()
		t.Fatalf("port %d still listens", port)
	}
}

func isTunnel(r auditRec) bool { return r.Kind == "tunnel" }

// tunnels.spec.ts: local and dynamic tunnels run, survive a lock, and stop.
func TestFlowTunnels(t *testing.T) {
	f := tunnelsFlow(t)
	echo := echoServer(t)
	f.openTunnels("box")
	if tab, _ := f.a.tabs.Get(f.a.tabs.Active); tab.Kind != model.TunnelsKind || !f.tt.HasText("box · tunnels") {
		t.Fatalf("%+v", tab)
	}
	lp, dp := freePort(t), freePort(t)
	f.addTunnel("box", "Local", lp, "127.0.0.1", echo, "local")
	f.addTunnel("box", "Dynamic", dp, "", 0, "dynamic")
	if n := len(f.a.tunnels); n != 2 {
		t.Fatalf("%d rows", n)
	}
	for _, label := range []string{"local", "dynamic"} {
		f.click("Start " + label)
		f.tunnelIs("box", label, "running")
	}
	lc := connect(t, lp)
	pingOn(t, lc)
	f.until(func() bool { x, _ := f.tunnel("box", "local"); return x.Conns == 1 }, "1 connection on local")
	sc := socks(t, dp, echo)
	pingOn(t, sc)

	f.click("Hosts")
	f.until(func() bool { return f.tt.HasText("2 ⇄") }, "the badge 2 ⇄")

	f.click("Lock")
	f.until(func() bool { return f.tt.HasText("Master password") }, "the unlock screen")
	pingOn(t, lc)
	pingOn(t, sc)
	pingOn(t, connect(t, lp)) // and a new connection
	lc.Close()
	sc.Close()
	f.unlock()

	f.click("box · tunnels")
	for _, label := range []string{"local", "dynamic"} {
		f.click("Stop " + label)
		f.tunnelIs("box", label, "stopped")
	}
	refused(t, lp)
	refused(t, dp)
	f.click("Hosts")
	if f.tt.HasText("⇄") {
		t.Fatal("the badge outlived the tunnels")
	}
	for _, label := range []string{"local", "dynamic"} {
		x, _ := f.tunnel("box", label)
		start := f.lastRec(func(r auditRec) bool { return isTunnel(r) && r.ID == x.ID && r.Phase == "start" }, "the start of "+label)
		end := f.lastRec(func(r auditRec) bool { return isTunnel(r) && r.ID == x.ID && r.Phase == "end" }, "the end of "+label)
		if start.Listen != loopback(x.ListenPort) || end.Reason != "stopped" || end.Conns < 1 {
			t.Fatalf("%s: start %+v end %+v", label, start, end)
		}
	}
}

// A remote tunnel reaches this machine from the server: the Auto-allow
// dialog lists it while it runs (slice 3b plan Decision 2), and a save that
// changes the server ends it, which the row shows.
func TestFlowTunnelsRemoteWarnsAndEndsOnServerChange(t *testing.T) {
	f := tunnelsFlow(t)
	echo := echoServer(t)
	f.openTunnels("box")
	rp := freePort(t)
	f.addTunnel("box", "Remote", rp, "127.0.0.1", echo, "back")
	f.click("Start back")
	f.tunnelIs("box", "back", "running")
	pingOn(t, connect(t, rp)) // sshtestd listens on this machine for the server

	f.editAuto("box", "15 min")
	want := "R server " + loopback(rp) + " → " + loopback(echo)
	f.until(func() bool {
		return f.tt.HasText("Remote tunnels running on this host reach your machine:") && f.tt.HasText(want)
	}, "the remote tunnel in the Auto-allow dialog")
	f.tt.Key(0, ui.KeyEscape) // back to the editor
	f.until(func() bool { return f.a.editor != nil && f.a.editor.confirm == nil }, "the editor")
	choose(t, f.tt, "15 min", "Off")
	f.fill("Port", strconv.Itoa(f.port+1))
	f.click("Save")
	f.until(func() bool { return f.a.editor == nil }, "the save")
	f.click("box · tunnels")
	f.tunnelIs("box", "back", "server changed")
	refused(t, rp)
	x, _ := f.tunnel("box", "back")
	if r := f.lastRec(func(r auditRec) bool { return isTunnel(r) && r.ID == x.ID && r.Phase == "end" }, "the end of back"); r.Reason != "server changed" || r.TunnelKind != "remote" {
		t.Fatalf("%+v", r)
	}
}
