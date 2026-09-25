package sshx

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startSSH(t *testing.T) (host string, port int, cleanup func()) {
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "lscr.io/linuxserver/openssh-server:latest",
		ExposedPorts: []string{"2222/tcp"},
		Env: map[string]string{
			"PASSWORD_ACCESS": "true", "USER_NAME": "test", "USER_PASSWORD": "testpass",
		},
		WaitingFor: wait.ForListeningPort("2222/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	h, _ := c.Host(ctx)
	p, _ := c.MappedPort(ctx, "2222")
	return h, int(p.Num()), func() { c.Terminate(ctx) }
}

// startSSHWithRoot starts the same SSH-accessible container as startSSH, then
// sets a known root password inside it so `su -` can elevate. If Docker is
// unavailable, or the image can't be prepared with a root password, it skips
// the calling test rather than failing.
func startSSHWithRoot(t *testing.T) (host string, port int, cleanup func()) {
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "lscr.io/linuxserver/openssh-server:latest",
		ExposedPorts: []string{"2222/tcp"},
		Env: map[string]string{
			"PASSWORD_ACCESS": "true", "USER_NAME": "test", "USER_PASSWORD": "testpass",
		},
		WaitingFor: wait.ForListeningPort("2222/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	// The image ships su without the setuid bit ("su: must be suid to work properly").
	code, _, err := c.Exec(ctx, []string{"sh", "-c", `echo 'root:rootpass' | chpasswd && chmod u+s "$(readlink -f "$(command -v su)")"`})
	if err != nil || code != 0 {
		c.Terminate(ctx)
		t.Skipf("could not prepare image for su (set root password): err=%v code=%d", err, code)
	}
	h, _ := c.Host(ctx)
	p, _ := c.MappedPort(ctx, "2222")
	return h, int(p.Num()), func() { c.Terminate(ctx) }
}

func TestExecEcho(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSH(t)
	defer cleanup()
	m := NewManager(DialConfig{
		Host: host, Port: port, User: "test", Password: "testpass",
		Auth: "password", Insecure: true, TimeoutMs: 30000,
	})
	defer m.Close()
	res, err := m.Exec(context.Background(), "echo hello-ssh")
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Stdout; got == "" || !contains(got, "hello-ssh") {
		t.Fatalf("out = %q", got)
	}
}

func TestExecSplitsStreamsAndExitCode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSH(t)
	defer cleanup()
	m := NewManager(DialConfig{Host: host, Port: port, User: "test", Password: "testpass", Auth: "password", Insecure: true, TimeoutMs: 30000})
	defer m.Close()
	res, err := m.Exec(context.Background(), "echo out; echo err 1>&2; exit 3")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "out" || strings.TrimSpace(res.Stderr) != "err" || res.ExitCode != 3 {
		t.Fatalf("got %+v", res)
	}
}

func TestExecCancelReturnsErrCancelled(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSH(t)
	defer cleanup()
	m := NewManager(DialConfig{Host: host, Port: port, User: "test", Password: "testpass", Auth: "password", Insecure: true, TimeoutMs: 30000})
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(500 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := m.Exec(ctx, "sleep 30")
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("want ErrCancelled, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancel did not return promptly")
	}
}

func TestExecCtxDeadlineBeatsConfigTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	host, port, cleanup := startSSH(t)
	defer cleanup()
	m := NewManager(DialConfig{Host: host, Port: port, User: "test", Password: "testpass", Auth: "password", Insecure: true, TimeoutMs: 60000})
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := m.Exec(ctx, "sleep 30")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want timeout error, got %v", err)
	}
}

// waitCommandOutput must not block forever on a reader that never produces
// a newline (the su pty's stdout pipe when a silent command hangs), and
// must race ctx/timeout independently of the inner read. These use a raw
// io.Pipe so they need no SSH session and no Docker.

func TestWaitCommandOutputCtxCancelReturnsErrCancelled(t *testing.T) {
	pr, pw := io.Pipe()
	r := bufio.NewReader(pr)
	kill := func() { pw.CloseWithError(errors.New("killed")) }

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()

	start := time.Now()
	_, _, err := waitCommandOutput(ctx, r, 30000, "marker", kill)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("want ErrCancelled, got %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want errors.Is(err, context.Canceled), got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancel did not return promptly")
	}
}

// A ctx deadline longer than timeoutMs must win outright, including over
// the inner read's own backstop timeout — not just over the outer
// fallback channel (which is trivially nil'd out when ctx has a
// deadline). Regression for a bug where readCommandOutput was still given
// timeoutMs internally, so a slow-but-alive command was killed at
// timeoutMs even though ctx allowed much longer.
func TestWaitCommandOutputCtxDeadlineWinsOverShorterTimeoutMs(t *testing.T) {
	pr, pw := io.Pipe()
	r := bufio.NewReader(pr)
	kill := func() { pw.CloseWithError(errors.New("killed")) }
	defer kill()

	go func() {
		time.Sleep(150 * time.Millisecond)
		pw.Write([]byte("line\n"))
		time.Sleep(50 * time.Millisecond)
		pw.Write([]byte("marker:0\n"))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, code, err := waitCommandOutput(ctx, r, 100, "marker", kill)
	if err != nil {
		t.Fatalf("ctx deadline (5s) should have won over timeoutMs (100ms), got err=%v", err)
	}
	if code != 0 || !strings.Contains(out, "line") {
		t.Fatalf("got out=%q code=%d", out, code)
	}
}

func TestWaitCommandOutputCtxDeadlineReturnsTimeoutError(t *testing.T) {
	pr, pw := io.Pipe()
	r := bufio.NewReader(pr)
	kill := func() { pw.CloseWithError(errors.New("killed")) }

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := waitCommandOutput(ctx, r, 30000, "marker", kill)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want timeout error, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("deadline did not return promptly")
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
