//go:build unix

package view

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
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
	if _, err := os.Stat(src); err != nil {
		t.Fatal(err)
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
		// End this run's transfers first, on the hub that holds them: a
		// Windows host cannot delete a file a live download keeps open.
		live := func(j *fileJob) bool { return j.state == "planning" || j.state == "running" }
		for _, j := range v.jobs {
			if live(j) {
				id := j.id
				f.a.Update(func() { f.a.filesCancel(id) })
			}
		}
		f.untilFor(time.Minute, func() bool { return !slices.ContainsFunc(v.jobs, live) }, "the transfers to end")
		g, w := f, v
		if portChanged { // this vault copy no longer reaches the host: a fresh copy does
			g, _ = liveFlow(t, src, pw)
			_, w = g.openFilesOn(host, time.Minute)
		}
		g.goTo(w, home, false)
		g.deleteByName(w, scratch)
		t.Logf("removed %s", remote)
	})
	f.goTo(v, remote, false)
	big := filepath.Join(t.TempDir(), "big")
	if fh, err := os.Create(big); err != nil {
		t.Fatal(err)
	} else if err := fh.Truncate(int64(bigMB) << 20); err != nil {
		t.Fatal(err)
	} else {
		fh.Close()
	}
	sh.pick(big)
	j := f.start(v, "Upload files")
	if got := f.finish(j, 20*time.Minute); got != "Uploaded 1" {
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
	j = f.start(v, "Upload folder")
	if got := f.finish(j, 5*time.Minute); got != "Uploaded 2" {
		t.Fatal(got)
	}
	f.untilFor(time.Minute, func() bool { return hasRow(v, "proj") }, "row proj")
	sh.pick(local)
	j = f.start(v, "Upload folder")
	f.untilFor(time.Minute, func() bool { return f.tt.HasText("2 files already exist") }, "the conflict dialog")
	f.pass()
	f.click("Skip existing")
	if got := f.finish(j, 5*time.Minute); got != "Uploaded 0 · skipped 2" {
		t.Fatal(got)
	}
	dl := filepath.Join(t.TempDir(), "dl")
	if err := os.Mkdir(dl, 0o700); err != nil {
		t.Fatal(err)
	}
	f.choose(v, "proj")
	sh.pick(dl)
	j = f.start(v, "Download")
	if got := f.finish(j, 5*time.Minute); got != "Downloaded 2" {
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
		f.goTo(v, denied, true)
		switch {
		case v.err == "":
			t.Logf("skipped: the remote user can read %s; set SSHGATE_LIVE_DENIED to a folder it cannot", denied)
		case !strings.Contains(strings.ToLower(v.err), "permission denied"):
			t.Fatalf("listing %s: %q", denied, v.err)
		}
		f.goTo(v, remote, false)
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
		j := f.start(v, "Download")
		f.untilFor(time.Minute, func() bool { return j.state == "running" && partSize(dir) > 0 }, "the download to write")
		return j, dir
	}
	j, dir := download("big-dl")
	f.click("Lock")
	f.until(func() bool { return f.tt.HasText("Master password") }, "the lock screen")
	before := partSize(dir)
	f.untilFor(15*time.Second, func() bool { return partSize(dir) > before || j.state == "done" }, fmt.Sprintf("the download to grow while locked; raise SSHGATE_LIVE_BIG_MB (%d)", bigMB))
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
	f.tt.SetSize(1000, 1200) // the whole editor in view: the Tester clicks where an element is
	f.click("Hosts")
	f.click("Edit " + model.DisplayText(host))
	f.until(func() bool { return f.a.editor != nil }, "the host editor")
	// A host on auto-allow (even paused) would ask the Auto-allow dialog on
	// Save; turn it off in this vault copy so Save only saves.
	if d := &f.a.editor.draft; d.AutoAllow != "" {
		openAI(f.t, f.tt)
		choose(f.t, f.tt, autoLabel(d.AutoAllow), "Off")
	}
	f.click("Port")
	f.tt.Key(ui.Cmd, ui.KeyA)
	f.tt.Type(strconv.Itoa(port + 1))
	f.until(func() bool { return f.tt.HasText("cancel 1 transfer") }, "the transfer warning")
	if j.state == "done" {
		t.Fatalf("the download ended before the save; raise SSHGATE_LIVE_BIG_MB (%d)", bigMB)
	}
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
	stty := func(arg string) error {
		c := exec.Command("stty", arg)
		c.Stdin = tty
		return c.Run()
	}
	fmt.Fprint(tty, prompt)
	if err := stty("-echo"); err != nil {
		t.Skip("cannot turn off the terminal echo: " + err.Error())
	}
	defer func() { stty("echo"); fmt.Fprintln(tty) }()
	// Ctrl-C ends the test binary without running defers: restore echo first.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	go func() {
		if _, ok := <-sig; ok {
			stty("echo")
			os.Exit(130)
		}
	}()
	line, err := bufio.NewReader(tty).ReadString('\n')
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

// goTo lists dir through the Path box and waits for the answer. A failed
// listing fails the test unless mayFail.
func (f *flowT) goTo(v *filesTab, dir string, mayFail bool) {
	f.t.Helper()
	f.click("Path")
	f.tt.Key(ui.Cmd, ui.KeyA)
	f.tt.Type(dir)
	f.tt.Key(0, ui.KeyEnter)
	f.untilFor(30*time.Second, func() bool { return v.pathIn == dir && !v.loading }, "the listing of "+dir)
	if v.err != "" && !mayFail {
		f.t.Fatalf("listing %s: %q", dir, v.err)
	}
}

// start clicks label and returns the job it adds. The job lands in v.jobs
// only on a flush, so a read right after the click would see the last one.
// It first waits for the actions: the relist after an upload or delete
// disables them while it loads, and a click then does nothing.
func (f *flowT) start(v *filesTab, label string) *fileJob {
	f.t.Helper()
	f.untilFor(time.Minute, func() bool { return model.ActionsReady(v.listing, v.loading) }, "the file actions")
	n := len(v.jobs)
	f.click(label)
	f.untilFor(time.Minute, func() bool { return len(v.jobs) > n }, "the "+label+" job")
	return v.jobs[n]
}

// finish waits for j to end and returns its row's text.
func (f *flowT) finish(j *fileJob, limit time.Duration) string {
	f.t.Helper()
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
