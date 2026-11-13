package mcpserver

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCheckGitRemote(t *testing.T) {
	for _, r := range []string{"/srv/app", "/home/me/my app", "/C:/Users/Tên Việt/app", "/c:/x/y"} {
		if err := checkGitRemote(r, false); err != nil {
			t.Errorf("%q refused: %v", r, err)
		}
	}
	for _, r := range []string{"", "srv/app", "/", "/srv/app/", "/srv/../app", "//srv/app", "/C:", "/C:/", "/C:x/app",
		"/a\"b", "/a$b", "/a`b", "/a\\b", "/a%b", "/a!b", "/a\u2018b", "/a\u201eb", "/a\x01b", "/a\u200bb"} {
		if checkGitRemote(r, false) == nil {
			t.Errorf("%q accepted", r)
		}
	}
	for _, r := range []string{"/app", "/C:/app"} {
		if checkGitRemote(r, true) == nil {
			t.Errorf("create at %q accepted", r)
		}
	}
	for _, r := range []string{"/srv/app", "/C:/Users/app"} {
		if err := checkGitRemote(r, true); err != nil {
			t.Errorf("create at %q refused: %v", r, err)
		}
	}
}

func TestCheckGitBranch(t *testing.T) {
	for _, b := range []string{"main", "feat/x", "v1.2_rc-3"} {
		if err := checkGitBranch(b); err != nil {
			t.Errorf("%q refused: %v", b, err)
		}
	}
	for _, b := range []string{"", "-x", "/x", "a b", "a~1", "a^", "a:b", "a@{u}", "ä"} {
		if checkGitBranch(b) == nil {
			t.Errorf("%q accepted", b)
		}
	}
}

func TestGitPath(t *testing.T) {
	for in, want := range map[string]string{"/C:/Users/me": "C:/Users/me", "/c:/x": "c:/x", "/srv/app": "/srv/app"} {
		if got := gitPath(in); got != want {
			t.Errorf("gitPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlanGitPush(t *testing.T) {
	cases := []struct {
		remote, branch string
		create         bool
		want           gitPushPlan
	}{
		{"/srv/my app", "main", false, gitPushPlan{Dest: "/srv/my app/.git", Name: "sshgate.bundle",
			Command: `git -C "/srv/my app" pull -q --ff-only --no-recurse-submodules "/srv/my app/.git/sshgate.bundle" main`}},
		{"/C:/Users/me/app", "feat/x", false, gitPushPlan{Dest: "/C:/Users/me/app/.git", Name: "sshgate.bundle",
			Command: `git -C "C:/Users/me/app" pull -q --ff-only --no-recurse-submodules "C:/Users/me/app/.git/sshgate.bundle" feat/x`}},
		{"/C:/Users/Tên Việt/app", "main", true, gitPushPlan{Dest: "/C:/Users/Tên Việt", Name: ".app.sshgate.bundle",
			Command: `git clone -q -c core.autocrlf=false -b main "C:/Users/Tên Việt/.app.sshgate.bundle" "C:/Users/Tên Việt/app"`}},
		{"/srv/app", "main", true, gitPushPlan{Dest: "/srv", Name: ".app.sshgate.bundle",
			Command: `git clone -q -c core.autocrlf=false -b main "/srv/.app.sshgate.bundle" "/srv/app"`}},
	}
	for _, c := range cases {
		got := planGitPush(c.remote, c.branch, c.create)
		if got != c.want {
			t.Errorf("planGitPush(%q, %q, %v)\n got %+v\nwant %+v", c.remote, c.branch, c.create, got, c.want)
		}
		for _, sep := range []string{"&&", ";", "|", "&"} {
			if strings.Contains(got.Command, sep) {
				t.Errorf("command chains with %q: %s", sep, got.Command)
			}
		}
	}
}

// git runs git in dir with a clean, test-only identity and config.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitRepo makes a repository with one commit on main; skips without git.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// makeBundle's own git calls must not read the developer's config either.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	git(t, dir, "add", "a.txt")
	git(t, dir, "commit", "-qm", "one")
	return dir
}

// tempLeft lists sshgate temp folders left in the repo.
func tempLeft(t *testing.T, root string) []string {
	t.Helper()
	a, _ := filepath.Glob(filepath.Join(root, ".git", "sshgate-*"))
	b, _ := filepath.Glob(filepath.Join(root, ".sshgate-*"))
	return append(a, b...)
}

// bundleHeader returns the first line and the ref lines of a bundle file.
func bundleHeader(t *testing.T, file string) (first string, refs []string) {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan()
	first = sc.Text()
	for sc.Scan() && sc.Text() != "" {
		refs = append(refs, sc.Text())
	}
	return first, refs
}

func TestMakeBundleDefaultBranch(t *testing.T) {
	repo := gitRepo(t)
	b, cleanup, err := makeBundle(context.Background(), repo, "", "sshgate.bundle")
	if err != nil {
		t.Fatal(err)
	}
	if b.Branch != "main" || b.Commit != git(t, repo, "rev-parse", "main") || b.Size <= 0 {
		t.Fatalf("bundle %+v", b)
	}
	if !regexp.MustCompile(`^\.git/sshgate-[0-9a-f]+/sshgate\.bundle$`).MatchString(b.Rel) {
		t.Fatalf("Rel %q", b.Rel)
	}
	first, refs := bundleHeader(t, filepath.Join(repo, b.Rel))
	if first != "# v2 git bundle" || len(refs) != 1 || refs[0] != b.Commit+" refs/heads/main" {
		t.Fatalf("header %q %q", first, refs)
	}
	fi, err := os.Stat(filepath.Dir(filepath.Join(repo, b.Rel)))
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("temp folder mode %v %v", fi.Mode(), err)
	}
	cleanup()
	if left := tempLeft(t, repo); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func TestMakeBundleNamedBranch(t *testing.T) {
	repo := gitRepo(t)
	git(t, repo, "checkout", "-q", "-b", "feat/x")
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("b\n"), 0o644)
	git(t, repo, "add", "b.txt")
	git(t, repo, "commit", "-qm", "two")
	git(t, repo, "checkout", "-q", "main")
	b, cleanup, err := makeBundle(context.Background(), repo, "feat/x", ".app.sshgate.bundle")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	_, refs := bundleHeader(t, filepath.Join(repo, b.Rel))
	if b.Branch != "feat/x" || len(refs) != 1 || refs[0] != git(t, repo, "rev-parse", "feat/x")+" refs/heads/feat/x" || !strings.HasSuffix(b.Rel, "/.app.sshgate.bundle") {
		t.Fatalf("bundle %+v refs %q", b, refs)
	}
}

func TestMakeBundleDetached(t *testing.T) {
	repo := gitRepo(t)
	git(t, repo, "checkout", "-q", "--detach")
	if _, _, err := makeBundle(context.Background(), repo, "", "sshgate.bundle"); err == nil || !strings.Contains(err.Error(), "HEAD is detached") {
		t.Fatalf("err %v", err)
	}
	if left := tempLeft(t, repo); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func TestMakeBundleNoLocalBranch(t *testing.T) {
	repo := gitRepo(t)
	for _, br := range []string{"origin/main", "refs/heads/main", "nope"} {
		if _, _, err := makeBundle(context.Background(), repo, br, "sshgate.bundle"); err == nil || !strings.Contains(err.Error(), "no local branch") {
			t.Errorf("%q: err %v", br, err)
		}
	}
	empty := t.TempDir()
	git(t, empty, "init", "-q", "-b", "main")
	if _, _, err := makeBundle(context.Background(), empty, "", "sshgate.bundle"); err == nil || !strings.Contains(err.Error(), "no local branch") {
		t.Errorf("empty repo: err %v", err)
	}
	if left := append(tempLeft(t, repo), tempLeft(t, empty)...); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func TestMakeBundleShallow(t *testing.T) {
	repo := gitRepo(t)
	shallow := filepath.Join(t.TempDir(), "s")
	git(t, repo, "clone", "-q", "--depth", "1", "file://"+repo, shallow)
	if _, _, err := makeBundle(context.Background(), shallow, "", "sshgate.bundle"); err == nil || !strings.Contains(err.Error(), "shallow") {
		t.Fatalf("err %v", err)
	}
}

func TestMakeBundleWorktree(t *testing.T) {
	repo := gitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, repo, "worktree", "add", "-q", "-b", "wt", wt)
	b, cleanup, err := makeBundle(context.Background(), wt, "", "sshgate.bundle")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\.sshgate-[0-9a-f]+/sshgate\.bundle$`).MatchString(b.Rel) || b.Branch != "wt" {
		t.Fatalf("bundle %+v", b)
	}
	cleanup()
	if left := tempLeft(t, wt); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func TestMakeBundleIgnoresGitEnv(t *testing.T) {
	repo := gitRepo(t)
	t.Setenv("GIT_DIR", t.TempDir())
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	b, cleanup, err := makeBundle(context.Background(), repo, "", "sshgate.bundle")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if b.Branch != "main" {
		t.Fatalf("bundle %+v", b)
	}
}
