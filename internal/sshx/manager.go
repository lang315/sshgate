package sshx

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

type Manager struct {
	cfg    DialConfig
	mu     sync.Mutex
	client *ssh.Client
	su     *suShell
}

type suShell struct {
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

func (m *Manager) runOnce(ctx context.Context, cmd string, stdin string) (string, error) {
	sess, err := m.client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var out bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &out
	if stdin != "" {
		sess.Stdin = strings.NewReader(stdin)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	timeout := time.Duration(m.cfg.TimeoutMs) * time.Millisecond
	select {
	case err := <-done:
		return out.String(), err
	case <-time.After(timeout):
		sess.Close()
		<-done // wait for sess.Run's writers to stop before reading out
		return out.String(), fmt.Errorf("command timed out after %dms", m.cfg.TimeoutMs)
	case <-ctx.Done():
		sess.Close()
		<-done
		return out.String(), ctx.Err()
	}
}

func (m *Manager) Exec(ctx context.Context, cmd string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensure(); err != nil {
		return "", err
	}
	if m.cfg.SuPassword != "" {
		return m.execElevated(ctx, cmd)
	}
	return m.runOnce(ctx, cmd, "")
}

func (m *Manager) ExecSudo(ctx context.Context, cmd string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensure(); err != nil {
		return "", err
	}
	if m.cfg.SudoPassword == "" {
		return m.runOnce(ctx, WrapSudoNoPassword(cmd), "")
	}
	return m.runOnce(ctx, WrapSudoWithPassword(cmd), m.cfg.SudoPassword+"\n")
}

func nonce() string {
	b := make([]byte, 16)
	rand.Read(b)
	return "SSHMCP" + hex.EncodeToString(b)
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client != nil {
		m.client.Close()
		m.client = nil
	}
	if m.su != nil {
		m.su.sess.Close()
		m.su = nil
	}
}

func (m *Manager) ensureElevated() (*suShell, error) {
	if m.su != nil {
		return m.su, nil
	}
	sess, err := m.client.NewSession()
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
	sh := &suShell{sess: sess, stdin: stdin, stdout: stdout, buf: bufio.NewReader(stdout)}
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
	m.su = sh
	return sh, nil
}

func (m *Manager) execElevated(ctx context.Context, cmd string) (string, error) {
	sh, err := m.ensureElevated()
	if err != nil {
		return "", err
	}
	n := nonce()
	marker := n + ":"
	fmt.Fprintf(sh.stdin, "%s\n", FrameSuCommand(cmd, n))
	out, err := readCommandOutput(sh.buf, time.Duration(m.cfg.TimeoutMs)*time.Millisecond, marker)
	if err != nil {
		// poisoned shell: tear down so next call re-elevates
		sh.sess.Close()
		m.su = nil
		return "", err
	}
	return out, nil
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

func readCommandOutput(r *bufio.Reader, timeout time.Duration, marker string) (string, error) {
	deadline := time.Now().Add(timeout)
	var acc strings.Builder
	for time.Now().Before(deadline) {
		line, err := r.ReadString('\n')
		acc.WriteString(line)
		if i := strings.Index(acc.String(), marker); i >= 0 {
			full := acc.String()
			// output is everything before the marker line; strip the echoed command's first line
			body := full[:i]
			if nl := strings.IndexByte(body, '\n'); nl >= 0 {
				body = body[nl+1:]
			}
			return body, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("command timed out")
}
