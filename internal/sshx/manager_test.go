package sshx

import (
	"context"
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
	out, err := m.Exec(context.Background(), "echo hello-ssh")
	if err != nil {
		t.Fatal(err)
	}
	if got := out; got == "" || !contains(got, "hello-ssh") {
		t.Fatalf("out = %q", got)
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
