//go:build unix

package view

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/internal/hubtest"
	"github.com/lang315/sshgate/app/model"
)

// TestLiveTunnels ports desktop/e2e/live-tunnels.spec.ts: a local and a
// dynamic (SOCKS5) tunnel on a real host, SSHGATE_LIVE_HOST, from a copy of
// your vault (SSHGATE_STORE, else ~/.config/sshgate/servers.json), across a
// lock; the real vault is never written. Both reach
// SSHGATE_LIVE_TUNNEL_TARGET as seen from the server (default
// 127.0.0.1:22, the host's own sshd), whose "SSH-" banner proves bytes came
// through. It reads the master password from the terminal without echo
// and types it into the app twice: at the start and after the lock. Both
// tunnels listen on 127.0.0.1 only, and are stopped before the hub ends.
//
//	SSHGATE_LIVE_HOST=<server> go test ./view -run TestLiveTunnels -v -count=1 -timeout 20m
func TestLiveTunnels(t *testing.T) {
	host := os.Getenv("SSHGATE_LIVE_HOST")
	if host == "" {
		t.Skip("set SSHGATE_LIVE_HOST to a server in your vault")
	}
	if testing.Short() {
		t.Skip("integration")
	}
	target := os.Getenv("SSHGATE_LIVE_TUNNEL_TARGET")
	if target == "" {
		target = "127.0.0.1:22"
	}
	targetHost, portText, err := net.SplitHostPort(target)
	targetPort, perr := strconv.Atoi(portText)
	if err != nil || perr != nil || targetHost == "" || len(targetHost) > 255 {
		t.Fatalf("SSHGATE_LIVE_TUNNEL_TARGET=%q: want host:port", target)
	}
	src := os.Getenv("SSHGATE_STORE")
	if src == "" {
		home, _ := os.UserHomeDir()
		src = filepath.Join(home, ".config", "sshgate", "servers.json")
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal(err)
	}
	pw := ttyPassword(t, "Master password for "+src+": ")
	f, _ := liveFlow(t, src, pw)
	f.liveTunnels(host, pw, targetHost, targetPort)
}

// TestFlowLiveTunnels runs TestLiveTunnels' checks on sshtestd, whose own
// port gives the "SSH-" banner, so the opt-in test cannot rot unseen.
func TestFlowLiveTunnels(t *testing.T) {
	f := flow(t)
	f.unlock()
	f.liveTunnels("box", hubtest.Password, "127.0.0.1", f.port)
}

// liveTunnels is live-tunnels.spec.ts on an unlocked flow: a local and a
// SOCKS5 tunnel to targetHost:targetPort, as seen from host, carry its
// banner before and during a lock; unlocked again with pw, both stop.
func (f *flowT) liveTunnels(host, pw, targetHost string, targetPort int) {
	t := f.t
	t.Helper()
	// Tall: a host on auto-allow adds the paused banner above the cards.
	// This test never opens the host editor, so no Auto-allow dialog asks.
	f.tt.SetSize(1000, 1200)
	f.openTunnels(host)

	labels := []string{"e2e local", "e2e dynamic"}
	t.Cleanup(func() { // stop what this test started, on the hub that runs it
		for _, l := range labels {
			if x, ok := f.tunnel(host, l); ok && model.TunnelLive(x.Status) {
				id := x.ID
				f.a.Update(func() { f.a.tunnelsStop(host, id) })
			}
		}
		f.untilFor(time.Minute, func() bool {
			for _, l := range labels {
				if x, ok := f.tunnel(host, l); ok && model.TunnelLive(x.Status) {
					return false
				}
			}
			return true
		}, "the tunnels to stop")
	})
	lp, dp := freePort(t), freePort(t)
	f.addTunnel(host, "Local", lp, targetHost, targetPort, labels[0])
	f.addTunnel(host, "Dynamic", dp, "", 0, labels[1])
	for _, l := range labels {
		f.click("Start " + l)
		f.tunnelIs(host, l, "running")
	}
	t.Logf(">>> local %s → %s, SOCKS5 %s", loopback(lp), net.JoinHostPort(targetHost, strconv.Itoa(targetPort)), loopback(dp))
	banners := func() {
		t.Helper()
		if b := readN(t, connect(t, lp), 4); string(b) != "SSH-" {
			t.Fatalf("local tunnel: %q", b)
		}
		if b := liveSocksBanner(t, dp, targetHost, targetPort); string(b) != "SSH-" {
			t.Fatalf("SOCKS5 tunnel: %q", b)
		}
	}
	banners()
	f.click("Hosts")
	f.until(func() bool { return f.tt.HasText("2 ⇄") }, "the badge 2 ⇄")

	f.click("Lock")
	f.until(func() bool { return f.tt.HasText("Master password") }, "the unlock screen")
	banners()
	t.Log(">>> both tunnels still work while the vault is locked")
	f.unlockWith(pw)

	f.click(model.DisplayText(host) + " · tunnels")
	for _, l := range labels {
		f.click("Stop " + l)
		f.tunnelIs(host, l, "stopped")
	}
	refused(t, lp)
	refused(t, dp)
}

// liveSocksBanner is a SOCKS5 CONNECT by domain name, resolved on the
// server, then the target's first 4 bytes.
func liveSocksBanner(t *testing.T, port int, host string, target int) []byte {
	t.Helper()
	c := connect(t, port)
	defer c.Close()
	c.Write([]byte{5, 1, 0})
	if b := readN(t, c, 2); b[0] != 5 || b[1] != 0 {
		t.Fatalf("greeting %v", b)
	}
	req := append([]byte{5, 1, 0, 3, byte(len(host))}, host...)
	c.Write(append(req, byte(target>>8), byte(target)))
	rep := readN(t, c, 14) // a 10-byte reply, then at least "SSH-"
	if rep[1] != 0 {
		t.Fatalf("connect reply %v", rep[:10])
	}
	return rep[10:]
}
