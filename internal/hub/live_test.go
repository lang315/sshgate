package hub

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/rpc"
	"github.com/lang315/sshgate/internal/sshx"
)

// TestLiveImportConnect imports your real ~/.ssh/config into a throwaway vault
// and opens a terminal on every imported host, the way the app does. It dials
// real hosts, so it runs only when SSHGATE_LIVE_SSH is set: "1" for every
// host, or a comma-separated list of aliases.
//
//	SSHGATE_LIVE_SSH=1 go test ./internal/hub -run TestLiveImportConnect -v
func TestLiveImportConnect(t *testing.T) {
	only := os.Getenv("SSHGATE_LIVE_SSH")
	if only == "" {
		t.Skip("set SSHGATE_LIVE_SSH=1 (or a list of aliases) to dial the hosts in ~/.ssh/config")
	}
	h, _ := newHubAt(t, nil, nil)
	c, _, _ := startTermDoor(t, h)
	ctx := context.Background()
	if err := c.Call(ctx, "vault.create", map[string]string{"password": "longenough"}, nil); err != nil {
		t.Fatal(err)
	}
	var scan scanResult
	if err := c.Call(ctx, "import.scan", nil, &scan); err != nil {
		t.Fatal(err)
	}
	var aliases []string
	for _, cd := range scan.Candidates {
		if only != "1" && !slices.Contains(strings.Split(only, ","), cd.Alias) {
			continue
		}
		if cd.Status != "ready" {
			t.Logf("%s: not imported (%s: %s)", cd.Alias, cd.Status, cd.Reason)
			continue
		}
		aliases = append(aliases, cd.Alias)
	}
	if len(aliases) == 0 {
		t.Fatalf("no ready host to dial (note %q)", scan.Note)
	}
	var res applyResult
	if err := c.Call(ctx, "import.apply", map[string]any{"aliases": aliases}, &res); err != nil {
		t.Fatal(err)
	}
	for _, s := range res.Skipped {
		t.Logf("%s: skipped by apply (%s)", s.Alias, s.Reason)
	}
	for _, alias := range res.Imported {
		t.Run(alias, func(t *testing.T) { liveOpen(t, c, alias) })
	}
}

// TestLiveVaultConnect opens a terminal on every server in a copy of your real
// vault (see openLiveVault), so it uses what you saved in the app:
// passwords, passphrases, pins. It runs only when SSHGATE_LIVE_VAULT is set:
// "1" for every server, or a comma-separated list of names.
//
//	SSHGATE_LIVE_VAULT=1 go test ./internal/hub -run TestLiveVaultConnect -v -count=1
func TestLiveVaultConnect(t *testing.T) {
	only := os.Getenv("SSHGATE_LIVE_VAULT")
	if only == "" {
		t.Skip("set SSHGATE_LIVE_VAULT=1 (or a list of server names) to dial the servers in your vault")
	}
	_, c, names := openLiveVault(t)
	n := 0
	for _, name := range names {
		if only != "1" && !slices.Contains(strings.Split(only, ","), name) {
			continue
		}
		n++
		t.Run(name, func(t *testing.T) { liveOpen(t, c, name) })
	}
	if n == 0 {
		t.Fatal("no matching server in the vault")
	}
}

// openLiveVault copies your real vault (SSHGATE_STORE, else
// ~/.config/sshgate/servers.json) to a temp dir, reads its master password
// from the terminal without echo, and unlocks a hub on the copy; the real
// vault is never written. It returns the hub, a UI-door client, and every
// server name in the vault.
func openLiveVault(t *testing.T) (*Hub, *rpc.Client, []string) {
	t.Helper()
	src := os.Getenv("SSHGATE_STORE")
	if src == "" {
		home, _ := os.UserHomeDir()
		src = filepath.Join(home, ".config", "sshgate", "servers.json")
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "servers.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no terminal to read the master password from")
	}
	defer tty.Close()
	fmt.Fprintf(tty, "Master password for %s: ", src)
	pw, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(tty)
	if err != nil {
		t.Fatal(err)
	}

	h, err := New(Options{StorePath: path, IdleLock: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	t.Cleanup(h.Registry().CloseAll)
	c, _, _ := startTermDoor(t, h)
	ctx := context.Background()
	if err := c.Call(ctx, "unlock", map[string]string{"password": string(pw)}, nil); err != nil {
		t.Fatal(err)
	}
	var servers []struct {
		Name string `json:"name"`
	}
	if err := c.Call(ctx, "servers", nil, &servers); err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(servers))
	for i, s := range servers {
		names[i] = s.Name
	}
	return h, c, names
}

// TestLiveRedact runs a fixed list of read-only commands on every POSIX
// server in a copy of your real vault (see openLiveVault) and masks the
// output exactly as run does before it reaches the AI
// (config.RedactCapStreams with the host's redactorFor). It prints bytes read
// and mask counts, never output, and fails if masked output still trips a
// tripwire. It runs only when SSHGATE_LIVE_REDACT is set: "1" for every
// server, or a comma-separated list of names.
//
//	SSHGATE_LIVE_REDACT=1 go test ./internal/hub -run TestLiveRedact -v -count=1
func TestLiveRedact(t *testing.T) {
	only := os.Getenv("SSHGATE_LIVE_REDACT")
	if only == "" {
		t.Skip("set SSHGATE_LIVE_REDACT=1 (or a list of server names) to check redaction on the servers in your vault")
	}
	h, _, names := openLiveVault(t)
	n, checked := 0, 0
	for _, name := range names {
		if only != "1" && !slices.Contains(strings.Split(only, ","), name) {
			continue
		}
		n++
		t.Run(name, func(t *testing.T) {
			if liveRedact(t, h, name) {
				checked++
			}
		})
	}
	if n == 0 {
		t.Fatal("no matching server in the vault")
	}
	if checked == 0 {
		t.Fatal("no host was checked: every matching server was skipped")
	}
}

// liveRedactCmds are read-only. /etc/*.env runs on its own so its counts
// show alone (the exit gate: /etc/mwtn.env on fviainboxes-db yields at
// least 2 password masks).
var liveRedactCmds = []string{
	"env",
	"cat /etc/*.env 2>/dev/null",
	"cat ~/.env ~/*/.env ~/.aws/credentials ~/.npmrc ~/.my.cnf ~/.pgpass 2>/dev/null",
	"git config --global --list 2>/dev/null",
	"command -v docker >/dev/null 2>&1 && docker inspect $(docker ps -q) 2>/dev/null",
}

// liveRedact checks one server. Everything it logs is a fixed command
// string, a byte count, or counts by kind.
func liveRedact(t *testing.T, h *Hub, name string) (checked bool) {
	dc, err := h.Resolve(name)
	if err != nil {
		t.Skip("cannot resolve this server; open it in the app first")
	}
	if dc.HostKey == "" {
		t.Skip("no pinned host key; open a terminal on it in the app first")
	}
	red := redactorFor(dc)
	mgr := h.Registry().Get(name, dc)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := mgr.Exec(ctx, "uname -s")
	if err != nil {
		t.Errorf("uname -s failed: %s", execErrClass(err))
		return false
	}
	if res.ExitCode != 0 || strings.TrimSpace(res.Stdout) == "" {
		t.Skip("not a POSIX host (uname -s failed)")
	}
	total := map[string]int{}
	read := 0
	for i, cmd := range liveRedactCmds {
		res, err := mgr.Exec(ctx, cmd)
		if err != nil {
			t.Errorf("command %d (%q) failed: %s", i, cmd, execErrClass(err))
			continue
		}
		out, errOut, counts := config.RedactCapStreams(red, res.Stdout, res.Stderr, config.DefaultOutputCap)
		n := len(res.Stdout) + len(res.Stderr)
		read += n
		for k, c := range counts {
			total[k] += c
		}
		t.Logf("%q: %d bytes, masked %s", cmd, n, countsText(counts))
		for _, trip := range tripwires(out+errOut, dc) {
			t.Errorf("command %d (%q): tripwire %q hit in masked output", i, cmd, trip)
		}
	}
	line := fmt.Sprintf("host %s: bytes=%d", name, read)
	for _, k := range config.RedactKinds {
		line += fmt.Sprintf(" %s=%d", k, total[k])
	}
	t.Log(line)
	return true
}

// execErrClass names why an exec failed in fixed text. It never uses
// err.Error(): sshx errors can carry remote su-shell output.
func execErrClass(err error) string {
	var unknown *sshx.HostKeyUnknownError
	var mismatch *sshx.HostKeyMismatchError
	switch {
	case errors.Is(err, sshx.ErrTimeout):
		return "timed out"
	case errors.Is(err, sshx.ErrCancelled):
		return "cancelled"
	case errors.As(err, &unknown), errors.As(err, &mismatch):
		return "host key failed"
	}
	return "exec failed"
}

var (
	pemTrip    = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY`)
	bearerTrip = regexp.MustCompile(`(?i)authorization:[ \t]*bearer[ \t]+(\S*)`)
	tokenTrip  = regexp.MustCompile(`AKIA[A-Z0-9]{16}|ghp_[A-Za-z0-9]{20,}|glpat-[A-Za-z0-9_-]{20,}|xox[abpr]-[A-Za-z0-9-]{10,}`)
)

// tripwires names what masked output still shows that masking should have
// removed. It never returns the matched text.
func tripwires(masked string, dc sshx.DialConfig) []string {
	var hit []string
	if pemTrip.MatchString(masked) {
		hit = append(hit, "unmasked private key")
	}
	for _, m := range bearerTrip.FindAllStringSubmatch(masked, -1) {
		if !strings.HasPrefix(m[1], "[REDACTED:") {
			hit = append(hit, "bearer token")
			break
		}
	}
	if tokenTrip.MatchString(masked) {
		hit = append(hit, "known token prefix")
	}
	for _, s := range []string{dc.Password, dc.SuPassword, dc.SudoPassword, dc.Passphrase} {
		if s != "" && strings.Contains(masked, s) {
			hit = append(hit, "vault secret")
			break
		}
	}
	return hit
}

// countsText renders counts in config.RedactKinds order, or "nothing".
func countsText(counts map[string]int) string {
	var parts []string
	for _, k := range config.RedactKinds {
		if n := counts[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", k, n))
		}
	}
	if parts == nil {
		return "nothing"
	}
	return strings.Join(parts, " ")
}

func TestExecErrClass(t *testing.T) {
	const secret = "s3cr3t-shell-tail"
	for _, c := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("%w: %s", sshx.ErrTimeout, secret), "timed out"},
		{fmt.Errorf("%w: %s", sshx.ErrCancelled, secret), "cancelled"},
		{fmt.Errorf("shell output tail: %s", secret), "exec failed"},
	} {
		got := execErrClass(c.err)
		if got != c.want || strings.Contains(got, secret) {
			t.Errorf("execErrClass(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestLiveRedactTripwires(t *testing.T) {
	dc := sshx.DialConfig{Password: "vault-pw-1"}
	cases := []struct {
		masked string
		want   []string
	}{
		{"DB_PASSWORD=[REDACTED:password]\nAuthorization: Bearer [REDACTED:auth_header]\n[REDACTED:private_key]\n", nil},
		{"-----BEGIN OPENSSH PRIVATE KEY-----\nb3Bl", []string{"unmasked private key"}},
		{"> authorization: Bearer abc123", []string{"bearer token"}},
		{"id AKIAIOSFODNN7EXAMPLE", []string{"known token prefix"}},
		{"ghp_abcdefghijklmnopqrstuvwxyz", []string{"known token prefix"}},
		{"echo vault-pw-1", []string{"vault secret"}},
	}
	for _, c := range cases {
		if got := tripwires(c.masked, dc); !slices.Equal(got, c.want) {
			t.Errorf("tripwires(%q) = %v, want %v", c.masked, got, c.want)
		}
	}
	// Real-shaped output, masked the way run masks it, trips nothing.
	out, errOut, counts := config.RedactCapStreams(redactorFor(dc),
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----\nPASS=vault-pw-1\nGITHUB_TOKEN=ghp_R8mK2vQx7LpT4nWz9YbC3dFh6JsA1uEo5GiN\n",
		"> Authorization: Bearer abc123\n", config.DefaultOutputCap)
	if hit := tripwires(out+errOut, dc); hit != nil {
		t.Fatalf("masked output tripped %v", hit)
	}
	if got := countsText(counts); got != "private_key=1 secret=2 auth_header=1" {
		t.Fatalf("countsText = %q", got)
	}
	if got := countsText(nil); got != "nothing" {
		t.Fatalf("countsText(nil) = %q", got)
	}
}

// liveOpen opens a terminal on server the way the app does, never trusting a
// host key, and closes it.
func liveOpen(t *testing.T, c *rpc.Client, server string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var open map[string]any
	if err := c.Call(ctx, "term.open", map[string]any{"id": server, "server": server, "rows": 24, "cols": 80}, &open); err != nil {
		t.Fatalf("term.open: %v", err)
	}
	if open["status"] != "open" {
		t.Fatalf("term.open: %v", open)
	}
	if err := c.Call(ctx, "term.close", map[string]string{"id": server}, nil); err != nil {
		t.Errorf("term.close: %v", err)
	}
	var l map[string]any
	if err := c.Call(ctx, "files.list", map[string]any{"server": server, "path": ""}, &l); err != nil {
		t.Errorf("files.list: %v", err)
	} else if l["status"] != "listed" {
		t.Errorf("files.list: %v", l)
	}
}
