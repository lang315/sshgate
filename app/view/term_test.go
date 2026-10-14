package view

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// rec is a Conn that records writes and marks, for the IME and clipboard
// assertions of desktop/e2e/ime.spec.ts and clipboard.spec.ts.
type rec struct {
	mu     sync.Mutex
	writes []string
	marks  int
	block  chan struct{}
}

func (r *rec) Read(p []byte) (int, error) { <-r.block; return 0, os.ErrClosed }
func (r *rec) Write(p []byte) (int, error) {
	r.mu.Lock()
	r.writes = append(r.writes, string(p))
	r.mu.Unlock()
	return len(p), nil
}
func (r *rec) Close() error { return nil }

func needLib(t *testing.T) {
	if os.Getenv("MYGO_GHOSTTY_VT") == "" {
		t.Skip("MYGO_GHOSTTY_VT unset: run scripts/build-ghostty-vt.sh; never download egoist's build")
	}
}

func TestIMECommitSentOnceAsUser(t *testing.T) {
	needLib(t)
	r := &rec{block: make(chan struct{})}
	term, err := terminal.New(terminal.Options{Conn: r, OnInput: func(string) { r.mu.Lock(); r.marks++; r.mu.Unlock() }})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	tt := ui.NewTester(func(c *ui.Context) { terminal.View(c, term).Fill().AutoFocus() }, 400, 200)
	tt.Frame()
	tt.Compose("tiê", 3)
	tt.Compose("tiến", 4)
	tt.Type("tiếng")
	tt.Frame()
	r.mu.Lock()
	defer r.mu.Unlock()
	if got := strings.Join(r.writes, ""); got != "tiếng" {
		t.Fatalf("sent %q, want the committed word once", got)
	}
	if r.marks != 1 {
		t.Fatalf("OnInput marked %d times", r.marks)
	}
}

func TestCopyAndPaste(t *testing.T) {
	needLib(t)
	r := &rec{block: make(chan struct{})}
	pasted := 0
	term, err := terminal.New(terminal.Options{Conn: r, OnPaste: func(string) bool { pasted++; return false }})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	term.Feed([]byte("hello world"))
	tt := ui.NewTester(func(c *ui.Context) { terminal.View(c, term).Fill().AutoFocus() }, 400, 200)
	tt.Frame()
	tt.Command("selectAll")
	tt.Command("copy")
	if !strings.Contains(tt.Clipboard(), "hello world") {
		t.Fatalf("clipboard %q", tt.Clipboard())
	}
	tt.SetClipboard("pasted text")
	tt.Command("paste")
	tt.Frame()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !strings.Contains(strings.Join(r.writes, ""), "pasted text") || pasted != 1 {
		t.Fatalf("writes %q, OnPaste %d", r.writes, pasted)
	}
}
