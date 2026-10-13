package hubproc

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/lang315/sshgate/app/internal/hubtest"
)

func hubLaunch(t *testing.T, store string) Launch {
	bin, _ := hubtest.Binaries(t)
	return Launch{Command: bin, Args: []string{"hub", "--store=" + store}, Env: hubtest.Env(t)}
}

func TestStartHelloAndUnlock(t *testing.T) {
	store := hubtest.SSHD(t)
	p, err := Start(context.Background(), hubLaunch(t, store), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	if err := p.Client().Call(context.Background(), "unlock", map[string]string{"password": hubtest.Password}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestStartFailsOnMissingBinary(t *testing.T) {
	_, err := Start(context.Background(), Launch{Command: "/nonexistent/sshgate", Args: []string{"hub"}}, nil)
	if err == nil {
		t.Fatal("started a missing binary")
	}
}

func TestStartFailsOnWrongProtocol(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	// A "hub" that answers hello with protocol 11, then waits.
	script := `read line; id=$(printf '%s' "$line" | sed 's/.*"id":\([0-9]*\).*/\1/'); printf '{"jsonrpc":"2.0","id":%s,"result":{"protocol":11}}\n' "$id"; sleep 5`
	_, err := Start(context.Background(), Launch{Command: "/bin/sh", Args: []string{"-c", script}}, nil)
	if err == nil || !strings.Contains(err.Error(), "protocol 11") {
		t.Fatalf("err = %v", err)
	}
}
