package sshx

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
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
	go func() { client.Wait(); m.mu.Lock(); m.client = nil; m.mu.Unlock() }()
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
		return out.String(), fmt.Errorf("command timed out after %dms", m.cfg.TimeoutMs)
	case <-ctx.Done():
		sess.Close()
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
	out, err := m.runOnce(ctx, cmd, "")
	return out, wrapExit(err)
}

func (m *Manager) ExecSudo(ctx context.Context, cmd string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensure(); err != nil {
		return "", err
	}
	if m.cfg.SudoPassword == "" {
		out, err := m.runOnce(ctx, WrapSudoNoPassword(cmd), "")
		return out, wrapExit(err)
	}
	out, err := m.runOnce(ctx, WrapSudoWithPassword(cmd), m.cfg.SudoPassword+"\n")
	return out, wrapExit(err)
}

func wrapExit(err error) error {
	if err == nil {
		return nil
	}
	var ee *ssh.ExitError
	if bytes.Contains([]byte(err.Error()), []byte("exited")) {
		return err
	}
	_ = ee
	return err
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
}

// execElevated is implemented in Task 10 (su-shell elevation).
func (m *Manager) execElevated(ctx context.Context, cmd string) (string, error) {
	return "", fmt.Errorf("su elevation not yet implemented")
}
