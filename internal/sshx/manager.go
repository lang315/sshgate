package sshx

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"os"
)

type DialConfig struct {
	Host, User, Password, PrivateKey, Passphrase string
	SuPassword, SudoPassword, HostKey, Auth      string
	Port, TimeoutMs                              int
	Insecure                                     bool
	OnLearnHostKey                               func(fp string)
}

// Manager owns one lazily dialed client. mu guards client, su and kaStop and
// is only ever held briefly (dialing aside): commands run without it, so a
// long Exec never blocks OpenSession, keepalive, or Close. suMu serializes
// use of the single shared su root shell.
type Manager struct {
	cfg    DialConfig
	mu     sync.Mutex
	client *ssh.Client
	su     *suShell
	kaStop chan struct{} // non-nil while a keepalive goroutine runs
	suMu   sync.Mutex
}

type suShell struct {
	client *ssh.Client // the client the shell runs on
	sess   *ssh.Session
	stdin  io.WriteCloser
	stdout io.Reader
	buf    *bufio.Reader
}

func NewManager(cfg DialConfig) *Manager { return &Manager{cfg: cfg} }

func (m *Manager) authMethods() ([]ssh.AuthMethod, error) {
	switch m.cfg.Auth {
	case "agent":
		sock := os.Getenv("SSH_AUTH_SOCK")
		if sock == "" {
			return nil, fmt.Errorf("SSH_AUTH_SOCK not set for agent auth")
		}
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return nil, err
		}
		return []ssh.AuthMethod{ssh.PublicKeysCallback(agent.NewClient(conn).Signers)}, nil
	case "key":
		var signer ssh.Signer
		var err error
		if m.cfg.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(m.cfg.PrivateKey), []byte(m.cfg.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(m.cfg.PrivateKey))
		}
		if err != nil {
			return nil, err
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	default:
		return []ssh.AuthMethod{ssh.Password(m.cfg.Password)}, nil
	}
}

func (m *Manager) ensure() error {
	if m.client != nil {
		return nil
	}
	auth, err := m.authMethods()
	if err != nil {
		return err
	}
	cc := &ssh.ClientConfig{
		User:            m.cfg.User,
		Auth:            auth,
		HostKeyCallback: HostKeyCallback(m.cfg.HostKey, m.cfg.Insecure, m.cfg.OnLearnHostKey),
		Timeout:         30 * time.Second,
	}
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	client, err := ssh.Dial("tcp", addr, cc)
	if err != nil {
		return err
	}
	m.client = client
	go func() {
		client.Wait()
		m.mu.Lock()
		if m.client == client {
			m.client = nil
		}
		m.mu.Unlock()
	}()
	return nil
}

// ExecResult splits a completed command's streams from its exit code. A
// non-zero ExitCode is not an error; only transport, timeout, or
// cancellation problems are.
type ExecResult struct {
	Stdout, Stderr string
	ExitCode       int
}

// ErrCancelled is returned (wrapped) when ctx is cancelled while a command
// is running. The remote process was sent SIGKILL, but delivery is not
// guaranteed, so it may still be running.
var ErrCancelled = errors.New("cancelled; the remote process may still be running")

// runOnce runs cmd on sess, which it owns and closes. It never takes m.mu.
func (m *Manager) runOnce(ctx context.Context, sess *ssh.Session, cmd string, stdin string) (ExecResult, error) {
	defer sess.Close()
	var out, errb bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &errb
	if stdin != "" {
		sess.Stdin = strings.NewReader(stdin)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	// ctx deadline wins; otherwise fall back to the configured timeout.
	var timeout <-chan time.Time
	if _, has := ctx.Deadline(); !has {
		timeout = time.After(time.Duration(m.cfg.TimeoutMs) * time.Millisecond)
	}
	finish := func(runErr error) (ExecResult, error) {
		res := ExecResult{Stdout: out.String(), Stderr: errb.String()}
		var exit *ssh.ExitError
		if errors.As(runErr, &exit) {
			res.ExitCode = exit.ExitStatus()
			return res, nil
		}
		return res, runErr
	}
	select {
	case runErr := <-done:
		return finish(runErr)
	case <-timeout:
		_ = sess.Signal(ssh.SIGKILL)
		sess.Close()
		<-done // wait for sess.Run's writers to stop before reading out
		return ExecResult{Stdout: out.String(), Stderr: errb.String()},
			fmt.Errorf("command timed out after %dms", m.cfg.TimeoutMs)
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		sess.Close()
		<-done
		res := ExecResult{Stdout: out.String(), Stderr: errb.String()}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return res, fmt.Errorf("command timed out: %w", ctx.Err())
		}
		return res, fmt.Errorf("%w: %w", ErrCancelled, ctx.Err())
	}
}

func (m *Manager) Exec(ctx context.Context, cmd string) (ExecResult, error) {
	if m.cfg.SuPassword != "" {
		out, code, err := m.execElevated(ctx, cmd)
		return ExecResult{Stdout: out, ExitCode: code}, err
	}
	sess, err := m.OpenSession()
	if err != nil {
		return ExecResult{}, err
	}
	return m.runOnce(ctx, sess, cmd, "")
}

func (m *Manager) ExecSudo(ctx context.Context, cmd string) (ExecResult, error) {
	sess, err := m.OpenSession()
	if err != nil {
		return ExecResult{}, err
	}
	if m.cfg.SudoPassword == "" {
		return m.runOnce(ctx, sess, WrapSudoNoPassword(cmd), "")
	}
	return m.runOnce(ctx, sess, WrapSudoWithPassword(cmd), m.cfg.SudoPassword+"\n")
}

func nonce() string {
	b := make([]byte, 16)
	rand.Read(b)
	return "SSHMCP" + hex.EncodeToString(b)
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.kaStop != nil {
		close(m.kaStop)
		m.kaStop = nil
	}
	if m.client != nil {
		m.client.Close()
		m.client = nil
	}
	if m.su != nil {
		m.su.sess.Close()
		m.su = nil
	}
}

// OpenSession returns a new channel on the shared client. The mutex is held
// only while ensuring the client exists; the caller owns the session.
func (m *Manager) OpenSession() (*ssh.Session, error) {
	m.mu.Lock()
	err := m.ensure()
	c := m.client
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return c.NewSession()
}

// StartKeepalive pings the server every interval until Close. A ping that
// fails or gets no reply within interval closes the client, which ends every
// channel on it (terminals see EOF and report their own exit); onDead, if
// set, is then called. The next Exec/OpenSession redials. Idempotent.
func (m *Manager) StartKeepalive(interval time.Duration, onDead func(reason string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.kaStop != nil {
		return
	}
	stop := make(chan struct{})
	m.kaStop = stop
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		var last *ssh.Client // alive at the previous tick
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			m.mu.Lock()
			c := m.client
			stopped := m.kaStop != stop // Close ran while we waited for mu
			m.mu.Unlock()
			if stopped {
				return
			}
			if c == nil {
				// ensure's watcher may have dropped a client that died
				// between ticks; pinging it fails and reports the death.
				c = last
			}
			last = nil
			if c == nil {
				continue
			}
			if err := ping(c, interval); err != nil {
				m.mu.Lock()
				if m.client == c {
					m.client = nil
				}
				m.mu.Unlock()
				c.Close()
				select {
				case <-stop: // failed because Close closed the client
					return
				default:
				}
				if onDead != nil {
					onDead("keepalive failed: " + err.Error())
				}
				continue
			}
			last = c
		}
	}()
}

// ping sends keepalive@openssh.com. Any reply, even a refusal, proves the
// connection is alive. A hung connection is cut off after timeout; closing
// the client then unblocks the abandoned SendRequest.
func ping(c *ssh.Client, timeout time.Duration) error {
	errc := make(chan error, 1)
	go func() {
		_, _, err := c.SendRequest("keepalive@openssh.com", true, nil)
		errc <- err
	}()
	select {
	case err := <-errc:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("no reply within %s", timeout)
	}
}

// ensureElevated returns the shared su shell, starting one if needed. The
// caller holds suMu; m.mu is held only around state reads and writes, not
// while the su handshake runs.
func (m *Manager) ensureElevated() (*suShell, error) {
	m.mu.Lock()
	if err := m.ensure(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if m.su != nil && m.su.client != m.client { // left over from a dropped connection
		m.su.sess.Close()
		m.su = nil
	}
	if sh := m.su; sh != nil {
		m.mu.Unlock()
		return sh, nil
	}
	client := m.client
	m.mu.Unlock()

	sh, err := m.startSu(client)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client != client { // Close or a reconnect happened meanwhile
		sh.sess.Close()
		return nil, errors.New("connection closed while elevating")
	}
	m.su = sh
	return sh, nil
}

// dropSu closes sh and forgets it unless it was already replaced.
func (m *Manager) dropSu(sh *suShell) {
	sh.sess.Close()
	m.mu.Lock()
	if m.su == sh {
		m.su = nil
	}
	m.mu.Unlock()
}

// startSu opens a PTY shell on client and elevates it with su. It touches no
// Manager state.
func (m *Manager) startSu(client *ssh.Client) (*suShell, error) {
	sess, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	modes := ssh.TerminalModes{ssh.ECHO: 0}
	if err := sess.RequestPty("xterm", 24, 80, modes); err != nil {
		sess.Close()
		return nil, err
	}
	stdin, _ := sess.StdinPipe()
	stdout, _ := sess.StdoutPipe()
	if err := sess.Shell(); err != nil {
		sess.Close()
		return nil, err
	}
	sh := &suShell{client: client, sess: sess, stdin: stdin, stdout: stdout, buf: bufio.NewReader(stdout)}
	fmt.Fprint(stdin, "export LANG=C LC_ALL=C\n")
	fmt.Fprint(stdin, "su -\n")
	if err := readUntilAny(sh.buf, 10*time.Second, []string{"assword"}); err != nil {
		sess.Close()
		return nil, fmt.Errorf("su password prompt not seen: %w", err)
	}
	fmt.Fprint(stdin, m.cfg.SuPassword+"\n")
	sentinel := nonce()
	fmt.Fprintf(stdin, "PS1='%s'\n", sentinel)
	if err := readUntilAny(sh.buf, 10*time.Second, []string{sentinel}); err != nil {
		sess.Close()
		return nil, fmt.Errorf("su elevation failed (wrong password?): %w", err)
	}
	// Positively confirm elevation. A wrong password drops back to the
	// unprivileged shell, which also echoes the sentinel — so the sentinel
	// alone is not proof. Require uid 0.
	check := nonce()
	fmt.Fprintf(stdin, "%s\n", FrameSuCommand("id -u", check))
	out, code, err := readCommandOutput(sh.buf, 10*time.Second, check)
	if err != nil || code != 0 || lastNonEmptyLine(out) != "0" {
		sess.Close()
		return nil, fmt.Errorf("su elevation failed: not root (uid=%q, err=%v)", lastNonEmptyLine(out), err)
	}
	return sh, nil
}

// execElevated returns the su shell's captured output and exit code. A
// non-zero code is data, not an error; only transport/timeout/cancellation
// problems (which poison the persistent su shell) are returned as errors.
func (m *Manager) execElevated(ctx context.Context, cmd string) (string, int, error) {
	m.suMu.Lock() // one command at a time on the shared root shell
	defer m.suMu.Unlock()
	sh, err := m.ensureElevated()
	if err != nil {
		return "", 0, err
	}
	n := nonce()
	fmt.Fprintf(sh.stdin, "%s\n", FrameSuCommand(cmd, n))
	kill := func() {
		_ = sh.sess.Signal(ssh.SIGKILL)
		sh.sess.Close()
	}
	out, code, err := waitCommandOutput(ctx, sh.buf, m.cfg.TimeoutMs, n, kill)
	if err != nil {
		// poisoned shell — a half-finished or killed command would corrupt
		// the next capture, so it must be re-established next time.
		m.dropSu(sh)
		return "", 0, err
	}
	return out, code, nil
}

// waitCommandOutput races readCommandOutput (run in a goroutine, since its
// underlying Read has no deadline of its own and can block forever on a
// silent command) against ctx and a fallback timeout. ctx's deadline wins
// when set; timeoutMs applies only when ctx has no deadline — same rule as
// runOnce. On cancellation or timeout, kill is called (expected to SIGKILL
// and close the underlying session so the blocked read unblocks) and the
// call returns promptly without waiting for the abandoned goroutine.
func waitCommandOutput(ctx context.Context, r *bufio.Reader, timeoutMs int, marker string, kill func()) (string, int, error) {
	type result struct {
		out  string
		code int
		err  error
	}
	// ctx's deadline wins when set — including over the inner read's own
	// backstop timeout, which must therefore use the same effective
	// duration; otherwise a longer ctx deadline would still get cut short
	// by TimeoutMs inside readCommandOutput.
	d := time.Duration(timeoutMs) * time.Millisecond
	var timeout <-chan time.Time
	if dl, has := ctx.Deadline(); has {
		d = time.Until(dl)
	} else {
		timeout = time.After(d)
	}
	done := make(chan result, 1)
	go func() {
		out, code, err := readCommandOutput(r, d, marker)
		done <- result{out, code, err}
	}()

	select {
	case res := <-done:
		return res.out, res.code, res.err
	case <-timeout:
		kill()
		return "", 0, fmt.Errorf("command timed out after %dms", timeoutMs)
	case <-ctx.Done():
		kill()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", 0, fmt.Errorf("command timed out: %w", ctx.Err())
		}
		return "", 0, fmt.Errorf("%w: %w", ErrCancelled, ctx.Err())
	}
}

func readUntilAny(r *bufio.Reader, timeout time.Duration, needles []string) error {
	deadline := time.Now().Add(timeout)
	var acc strings.Builder
	for time.Now().Before(deadline) {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		acc.WriteByte(b)
		s := acc.String()
		for _, n := range needles {
			if strings.Contains(s, n) {
				return nil
			}
		}
		if strings.Contains(strings.ToLower(s), "su: ") && strings.Contains(strings.ToLower(s), "fail") {
			return fmt.Errorf("su reported failure")
		}
	}
	return fmt.Errorf("timeout waiting for prompt")
}

func readCommandOutput(r *bufio.Reader, timeout time.Duration, marker string) (string, int, error) {
	deadline := time.Now().Add(timeout)
	var body strings.Builder
	for time.Now().Before(deadline) {
		line, rerr := r.ReadString('\n')
		// Anchor on line start: the marker is only real output when a line
		// begins with "<nonce>:". The echoed command (if any) contains the
		// nonce mid-line and must not match.
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, marker+":") {
			codeStr := strings.TrimSpace(strings.TrimPrefix(trimmed, marker+":"))
			code, err := strconv.Atoi(codeStr)
			if err != nil {
				return "", 0, fmt.Errorf("garbled exit code marker %q: %w", trimmed, err)
			}
			return body.String(), code, nil
		}
		body.WriteString(line)
		if rerr != nil {
			return "", 0, rerr
		}
	}
	return "", 0, fmt.Errorf("command timed out")
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}
