package termconn

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/internal/hubtest"
)

// TestFloodThroughHub floods a real hub terminal (sshtestd's "flood N") and
// reads it through Conn, as the terminal's read loop does, with acks. It
// passes if every byte arrives and the reader never stalls past 30 s; the
// MB/s goes to the slice-0 report. SSHGATE_FLOOD_BYTES sets N (default 50 MiB).
func TestFloodThroughHub(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	n := 50 << 20
	if v := os.Getenv("SSHGATE_FLOOD_BYTES"); v != "" {
		n, _ = strconv.Atoi(v)
	}
	store := hubtest.SSHD(t)
	bin, _ := hubtest.Binaries(t)
	mux := NewMux()
	p, err := hubproc.Start(context.Background(), hubproc.Launch{Command: bin, Args: []string{"hub", "--store=" + store}, Env: hubtest.Env(t)}, func(m string, raw json.RawMessage) { mux.Note(m, raw) })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	ctx := context.Background()
	if err := p.Client().Call(ctx, "unlock", map[string]string{"password": hubtest.Password}, nil); err != nil {
		t.Fatal(err)
	}
	c, err := Open(ctx, p.Client(), mux, "t1", "box", 40, 120, nil)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			c.Close()
		}
	}()
	c.MarkUser()
	start := time.Now()
	c.Write([]byte("flood " + strconv.Itoa(n) + "\n"))
	total, tail := 0, []byte{}
	buf := make([]byte, 32<<10)
	deadline := time.AfterFunc(30*time.Second, func() { c.Close() })
	defer deadline.Stop()
	for !bytes.Contains(tail, []byte("FLOOD-DONE")) {
		k, err := c.Read(buf)
		if err == io.EOF {
			t.Fatalf("ended after %d bytes", total)
		}
		total += k
		tail = append(tail, buf[:k]...)
		if len(tail) > 64 {
			tail = tail[len(tail)-64:]
		}
	}
	el := time.Since(start)
	if total < n {
		t.Fatalf("got %d bytes, want at least %d", total, n)
	}
	// Close must end the hub terminal: the same id opens again.
	c.Close()
	closed = true
	c2, err := Open(ctx, p.Client(), mux, "t1", "box", 40, 120, nil)
	if err != nil {
		t.Fatalf("reopen after Close: %v", err)
	}
	c2.Close()
	t.Logf("flood through hub: %d bytes in %s = %.0f MB/s", total, el, float64(total)/1e6/el.Seconds())
}
