//go:build unix

package view

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/internal/hubtest"
	"github.com/lang315/sshgate/app/model"
)

// TestLiveFiles ports desktop/e2e/live-files.spec.ts: the Files checklist
// against a real host, SSHGATE_LIVE_HOST, from a copy of your vault
// (SSHGATE_STORE, else ~/.config/sshgate/servers.json); the real vault is
// never written. It reads the master password from the terminal without
// echo and types it into the app. Remote writes stay inside
// <home>/sshgate-e2e-<ms>, which it creates and removes. Optional:
// SSHGATE_LIVE_BIG_MB (default 128), SSHGATE_LIVE_DENIED (a folder the login
// cannot read; default /root). The Finder drop is a hand check.
//
//	SSHGATE_LIVE_HOST=<server> go test ./view -run TestLiveFiles -v -count=1 -timeout 60m
func TestLiveFiles(t *testing.T) {
	host := os.Getenv("SSHGATE_LIVE_HOST")
	if host == "" {
		t.Skip("set SSHGATE_LIVE_HOST to a server in your vault")
	}
	if testing.Short() {
		t.Skip("integration")
	}
	bigMB := 128
	if s := os.Getenv("SSHGATE_LIVE_BIG_MB"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			t.Fatalf("SSHGATE_LIVE_BIG_MB=%q", s)
		}
		bigMB = n
	}
	src := os.Getenv("SSHGATE_STORE")
	if src == "" {
		home, _ := os.UserHomeDir()
		src = filepath.Join(home, ".config", "sshgate", "servers.json")
	}
	pw := ttyPassword(t, "Master password for "+src+": ")
	scratch := fmt.Sprintf("sshgate-e2e-%d", time.Now().UnixMilli())
	f, sh := liveFlow(t, src, pw)
	_, v := f.openFilesOn(host, time.Minute)
	home := v.shown
	if !regexp.MustCompile(`^/[^\s'"\\$` + "`" + `]+$`).MatchString(home) {
		t.Fatalf("home %q", home)
	}
	remote := home + "/" + scratch
	for _, x := range slices.Clone(v.rows) { // leftovers of runs that could not clean up
		if x.Kind == "dir" && regexp.MustCompile(`^sshgate-e2e-\d+$`).MatchString(x.Name) {
			f.deleteByName(v, x.Name)
		}
	}

	f.click("New folder")
	f.tt.Type(scratch)
	f.click("Create")
	f.untilFor(time.Minute, func() bool { return hasRow(v, scratch) }, "the scratch folder")
	portChanged := false
	t.Cleanup(func() {
		t.Logf(">>> if this cleanup fails, remove %s on %s by hand", remote, host)
		g, w := f, v
		if portChanged { // this vault copy no longer reaches the host: a fresh copy does
			g, _ = liveFlow(t, src, pw)
			_, w = g.openFilesOn(host, time.Minute)
		}
		for _, j := range w.jobs {
			if j.state == "planning" || j.state == "running" {
				g.click("Cancel " + j.title())
			}
		}
		g.untilFor(time.Minute, func() bool {
			return !slices.ContainsFunc(w.jobs, func(j *fileJob) bool { return j.state == "planning" || j.state == "running" })
		}, "the transfers to end")
		g.goTo(w, home)
		g.deleteByName(w, scratch)
		t.Logf("removed %s", remote)
	})
	f.goTo(v, remote)
	big := filepath.Join(t.TempDir(), "big")
	if fh, err := os.Create(big); err != nil {
		t.Fatal(err)
	} else if err := fh.Truncate(int64(bigMB) << 20); err != nil {
		t.Fatal(err)
	} else {
		fh.Close()
	}
	sh.pick(big)
	f.click("Upload files")
	if got := f.finish(v, 20*time.Minute); got != "Uploaded 1" {
		t.Fatal(got)
	}

	t.Log("exit gate: browse, upload, skip existing, download, rename, delete")
	local := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(filepath.Join(local, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "a.txt"), []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "sub", "b.txt"), []byte("beta"), 0o600); err != nil {
		t.Fatal(err)
	}
	sh.pick(local)
	f.click("Upload folder")
	if got := f.finish(v, 5*time.Minute); got != "Uploaded 2" {
		t.Fatal(got)
	}
	f.untilFor(time.Minute, func() bool { return hasRow(v, "proj") }, "row proj")
	sh.pick(local)
	f.click("Upload folder")
	f.untilFor(time.Minute, func() bool { return f.tt.HasText("2 files already exist") }, "the conflict dialog")
	f.pass()
	f.click("Skip existing")
	if got := f.finish(v, 5*time.Minute); got != "Uploaded 0 · skipped 2" {
		t.Fatal(got)
	}
	dl := filepath.Join(t.TempDir(), "dl")
	if err := os.Mkdir(dl, 0o700); err != nil {
		t.Fatal(err)
	}
	f.choose(v, "proj")
	sh.pick(dl)
	f.click("Download")
	if got := f.finish(v, 5*time.Minute); got != "Downloaded 2" {
		t.Fatal(got)
	}
	if f.read(filepath.Join(dl, "proj", "a.txt")) != "alpha" || f.read(filepath.Join(dl, "proj", "sub", "b.txt")) != "beta" {
		t.Fatal("downloaded files differ")
	}
	f.choose(v, "proj")
	f.click("Rename")
	f.tt.Key(ui.Cmd, ui.KeyA)
	f.tt.Type("proj2")
	f.tt.Key(0, ui.KeyEnter)
	f.untilFor(time.Minute, func() bool { return hasRow(v, "proj2") }, "row proj2")
	f.choose(v, "proj2")
	f.click("Delete")
	f.untilFor(time.Minute, func() bool { return f.tt.HasText("Type delete to confirm") }, "the typed-word delete dialog")
	f.tt.Type("delete")
	f.pass()
	f.click("Delete for good")
	f.untilFor(time.Minute, func() bool { return !hasRow(v, "proj2") }, "proj2 gone")

	t.Log("permission denied")
	denied := os.Getenv("SSHGATE_LIVE_DENIED")
	if denied == "" && !regexp.MustCompile(`^/[A-Za-z]:`).MatchString(home) {
		denied = "/root"
	}
	switch {
	case denied == "":
		t.Log("skipped: set SSHGATE_LIVE_DENIED to a folder you cannot read on this Windows host")
	default:
		f.goTo(v, denied)
		switch {
		case v.err == "":
			t.Logf("skipped: the remote user can read %s; set SSHGATE_LIVE_DENIED to a folder it cannot", denied)
		case !strings.Contains(strings.ToLower(v.err), "permission denied"):
			t.Fatalf("listing %s: %q", denied, v.err)
		}
		f.goTo(v, remote)
	}

	t.Log("lock during download")
	part := func(dir string) string {
		es, _ := os.ReadDir(dir)
		for _, e := range es {
			if strings.HasSuffix(e.Name(), ".sshgate-part") {
				return filepath.Join(dir, e.Name())
			}
		}
		return ""
	}
	partSize := func(dir string) int64 {
		if fi, err := os.Stat(part(dir)); err == nil {
			return fi.Size()
		}
		return -1
	}
	download := func(name string) (*fileJob, string) {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		f.choose(v, "big")
		sh.pick(dir)
		f.click("Download")
		j := v.jobs[len(v.jobs)-1]
		f.untilFor(time.Minute, func() bool { return j.state == "running" && partSize(dir) > 0 }, "the download to write")
		return j, dir
	}
	j, dir := download("big-dl")
	f.click("Lock")
	f.until(func() bool { return f.tt.HasText("Master password") }, "the lock screen")
	before := partSize(dir)
	f.untilFor(15*time.Second, func() bool { return partSize(dir) > before }, fmt.Sprintf("the download to grow while locked; raise SSHGATE_LIVE_BIG_MB (%d)", bigMB))
	f.unlockWith(pw)
	if j.state == "done" { // finished during the lock: cancel a fresh one instead
		j, dir = download("big-dl2")
	}
	f.click("Cancel " + j.title())
	f.untilFor(time.Minute, func() bool { return j.state == "done" }, "the download to end")
	if s := model.DoneSummary(j.done, j.err); !strings.HasPrefix(s, "Cancelled") {
		t.Fatal(s)
	}
	if part(dir) != "" {
		t.Fatal("a part file is left")
	}
	if _, err := os.Stat(filepath.Join(dir, "big")); !os.IsNotExist(err) {
		t.Fatalf("big was written: %v", err)
	}

	t.Log("server changed")
	j, _ = download("big-dl3")
	port := 0
	if s := f.a.server(host); s != nil {
		port = s.Port
	}
	f.click("Hosts")
	f.click("Edit " + model.DisplayText(host))
	f.click("Port")
	f.tt.Key(ui.Cmd, ui.KeyA)
	f.tt.Type(strconv.Itoa(port + 1))
	f.until(func() bool { return f.tt.HasText("cancel 1 transfer") }, "the transfer warning")
	f.click("Save")
	portChanged = true
	f.click(model.DisplayText(host) + " · files")
	f.untilFor(time.Minute, func() bool { return j.state == "done" }, "the download to end")
	if s := model.DoneSummary(j.done, j.err); !strings.HasPrefix(s, "Cancelled: server changed") {
		t.Fatal(s)
	}
}

// liveFlow runs the app on a copy of the vault src, unlocked with pw. The
// hub keeps your HOME and ssh-agent, as the Electron live specs did (a host
// may log in with the agent); only its runtime directory is a temp one, so
// a running sshgate app is not disturbed.
func liveFlow(t *testing.T, src, pw string) (*flowT, *fakeShell) {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(t.TempDir(), "servers.json")
	if err := os.WriteFile(store, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := hubtest.RuntimeDir(t)
	f := flowOn(t, store, append(os.Environ(), "SSHGATE_RUNTIME_DIR="+dir), dir)
	sh := &fakeShell{}
	f.a.Shell = sh
	f.tt.SetSize(1000, 700)
	f.unlockWith(pw)
	return f, sh
}

// ttyPassword reads a line from the terminal with echo off, as
// internal/hub's TestLiveVaultConnect does (stty here: no x/term in app/).
func ttyPassword(t *testing.T, prompt string) string {
	t.Helper()
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no terminal to read the master password from")
	}
	defer tty.Close()
	stty := func(arg string) {
		c := exec.Command("stty", arg)
		c.Stdin = tty
		c.Run()
	}
	fmt.Fprint(tty, prompt)
	stty("-echo")
	line, err := bufio.NewReader(tty).ReadString('\n')
	stty("echo")
	fmt.Fprintln(tty)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(line, "\r\n")
}

func hasRow(v *filesTab, name string) bool {
	return slices.ContainsFunc(v.rows, func(x hubapi.FileEntry) bool { return x.Name == name })
}

// choose selects name alone. A real folder can be long and the table shows
// only the rows in view, so this goes through the selection, as the e2e
// scrolled the grid.
func (f *flowT) choose(v *filesTab, name string) {
	f.t.Helper()
	f.a.Update(func() { v.selection.Clear(); v.selection.Add(name) })
	f.tt.flush()
}

// goTo lists dir through the Path box and waits for the answer.
func (f *flowT) goTo(v *filesTab, dir string) {
	f.t.Helper()
	f.click("Path")
	f.tt.Key(ui.Cmd, ui.KeyA)
	f.tt.Type(dir)
	f.tt.Key(0, ui.KeyEnter)
	f.untilFor(30*time.Second, func() bool { return v.pathIn == dir && !v.loading }, "the listing of "+dir)
}

// finish waits for the newest job to end and returns its row's text.
func (f *flowT) finish(v *filesTab, limit time.Duration) string {
	f.t.Helper()
	f.untilFor(time.Minute, func() bool { return len(v.jobs) > 0 }, "a job")
	j := v.jobs[len(v.jobs)-1]
	f.untilFor(limit, func() bool { return j.state == "done" }, j.title()+" to end")
	return model.DoneSummary(j.done, j.err)
}

// deleteByName deletes one folder of this test's scratch namespace.
func (f *flowT) deleteByName(v *filesTab, name string) {
	f.t.Helper()
	if !regexp.MustCompile(`^sshgate-e2e-\d+$`).MatchString(name) {
		f.t.Fatalf("refusing to delete %q: not this test's scratch namespace", name)
	}
	f.choose(v, name)
	f.click("Delete")
	f.untilFor(time.Minute, func() bool { return f.tt.HasText("Delete for good?") }, "the delete dialog")
	if f.tt.HasText("Type delete to confirm") {
		f.tt.Type("delete")
	}
	f.pass()
	f.click("Delete for good")
	f.untilFor(5*time.Minute, func() bool { return !hasRow(v, name) }, name+" gone")
}
