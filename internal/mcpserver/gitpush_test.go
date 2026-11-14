package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/lang315/sshgate/internal/rpc"
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
	if err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700) {
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

// gitDoor serves upload and exec like the MCP door and records each call.
func gitDoor(t *testing.T, uploadResp any, uploadErr string, exitCode int) (func(context.Context) (net.Conn, error), func() [][2]any) {
	var mu sync.Mutex
	var calls [][2]any
	dial := func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		s := rpc.NewServer()
		record := func(m string, raw json.RawMessage) {
			var p map[string]any
			json.Unmarshal(raw, &p)
			mu.Lock()
			calls = append(calls, [2]any{m, p})
			mu.Unlock()
		}
		s.HandleRequest("upload", func(_ context.Context, raw json.RawMessage) (any, error) {
			record("upload", raw)
			if uploadErr != "" {
				return nil, &rpc.Error{Code: -32000, Message: uploadErr}
			}
			return uploadResp, nil
		})
		s.HandleRequest("exec", func(_ context.Context, raw json.RawMessage) (any, error) {
			record("exec", raw)
			return map[string]any{"exitCode": exitCode, "stdout": "", "stderr": ""}, nil
		})
		go s.Serve(context.Background(), server, server)
		return client, nil
	}
	return dial, func() [][2]any { mu.Lock(); defer mu.Unlock(); return append([][2]any(nil), calls...) }
}

var oneFile = map[string]any{"files": 1, "bytes": 99}

func TestGitPushUpdate(t *testing.T) {
	repo := gitRepo(t)
	t.Chdir(repo)
	wd, _ := os.Getwd()
	dial, calls := gitDoor(t, oneFile, "", 0)
	res := callTool(t, BuildBridgeServer(dial), "git-push", map[string]any{"server": "vis", "remote": "/srv/app", "description": "deploy"})
	if res.IsError {
		t.Fatal(text(res))
	}
	c := calls()
	if len(c) != 2 || c[0][0] != "upload" || c[1][0] != "exec" {
		t.Fatalf("door calls %v", c)
	}
	up, ex := c[0][1].(map[string]any), c[1][1].(map[string]any)
	local := up["local"].([]any)
	if len(local) != 1 || !regexp.MustCompile(`^\.git/sshgate-[0-9a-f]+/sshgate\.bundle$`).MatchString(local[0].(string)) ||
		up["remote"].([]any)[0] != "/srv/app/.git" || up["overwrite"] != true || up["description"] != "git-push 1/2: deploy" ||
		up["timeoutSec"] != float64(600) || up["root"] != wd {
		t.Fatalf("upload params %v", up)
	}
	if ex["command"] != planGitPush("/srv/app", "main", false).Command || ex["description"] != "git-push 2/2: deploy" || ex["timeoutSec"] != float64(600) {
		t.Fatalf("exec params %v", ex)
	}
	h7 := git(t, repo, "rev-parse", "main")[:7]
	txt := text(res)
	if !strings.HasPrefix(txt, "uploaded bundle of main "+h7+" (") || !strings.Contains(txt, "the server's checked-out branch now contains main "+h7+" (fast-forwarded, or left as is if it was already ahead)") || !strings.Contains(txt, "exit code: 0") {
		t.Fatalf("answer %q", txt)
	}
	if left := tempLeft(t, repo); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func TestGitPushCreate(t *testing.T) {
	repo := gitRepo(t)
	t.Chdir(repo)
	dial, calls := gitDoor(t, oneFile, "", 0)
	res := callTool(t, BuildBridgeServer(dial), "git-push", map[string]any{"server": "vis", "remote": "/C:/Users/me/app", "create": true})
	if res.IsError {
		t.Fatal(text(res))
	}
	c := calls()
	up, ex := c[0][1].(map[string]any), c[1][1].(map[string]any)
	if up["remote"].([]any)[0] != "/C:/Users/me" || !strings.HasSuffix(up["local"].([]any)[0].(string), "/.app.sshgate.bundle") {
		t.Fatalf("upload params %v", up)
	}
	if ex["command"] != planGitPush("/C:/Users/me/app", "main", true).Command {
		t.Fatalf("exec params %v", ex)
	}
	h7 := git(t, repo, "rev-parse", "main")[:7]
	if txt := text(res); !strings.Contains(txt, "created /C:/Users/me/app at main "+h7) || !strings.Contains(txt, "/C:/Users/me/.app.sshgate.bundle can be deleted") {
		t.Fatalf("answer %q", txt)
	}
}

func TestGitPushNotFastForward(t *testing.T) {
	repo := gitRepo(t)
	t.Chdir(repo)
	dial, _ := gitDoor(t, oneFile, "", 1)
	res := callTool(t, BuildBridgeServer(dial), "git-push", map[string]any{"server": "vis", "remote": "/srv/app"})
	if res.IsError || !strings.Contains(text(res), "exit code: 1") ||
		!strings.Contains(text(res), "hint: if the server's branch has commits yours lacks, git-push will not replace them; to discard them, run git fetch and git reset --hard with exec (the human sees it)") {
		t.Fatalf("update: %v %q", res.IsError, text(res))
	}
	res = callTool(t, BuildBridgeServer(dial), "git-push", map[string]any{"server": "vis", "remote": "/srv/app", "create": true})
	if res.IsError || !strings.Contains(text(res), "hint: if the folder already exists, call git-push without create") {
		t.Fatalf("create: %v %q", res.IsError, text(res))
	}
}

func TestGitPushUploadDenied(t *testing.T) {
	repo := gitRepo(t)
	t.Chdir(repo)
	dial, calls := gitDoor(t, nil, "denied by user", 0)
	res := callTool(t, BuildBridgeServer(dial), "git-push", map[string]any{"server": "vis", "remote": "/srv/app"})
	if !res.IsError || text(res) != "denied by user" || len(calls()) != 1 {
		t.Fatalf("%v %q %v", res.IsError, text(res), calls())
	}
	if left := tempLeft(t, repo); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func TestGitPushUploadNotOneFile(t *testing.T) {
	t.Chdir(gitRepo(t))
	dial, calls := gitDoor(t, map[string]any{"files": 0, "errorCount": 1, "errors": []string{"x: permission denied"}}, "", 0)
	res := callTool(t, BuildBridgeServer(dial), "git-push", map[string]any{"server": "vis", "remote": "/srv/app"})
	if !res.IsError || !strings.Contains(text(res), "x: permission denied") || len(calls()) != 1 {
		t.Fatalf("%v %q %v", res.IsError, text(res), calls())
	}
}

func TestGitPushHubDown(t *testing.T) {
	repo := gitRepo(t)
	t.Chdir(repo)
	dial := func(context.Context) (net.Conn, error) { return nil, errors.New("no hub") }
	res := callTool(t, BuildBridgeServer(dial), "git-push", map[string]any{"server": "vis", "remote": "/srv/app"})
	if !res.IsError || text(res) != hubDownMsg {
		t.Fatalf("%v %q", res.IsError, text(res))
	}
	if left := tempLeft(t, repo); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func TestGitPushRefusesBeforeHub(t *testing.T) {
	repo := gitRepo(t)
	t.Chdir(repo)
	dial, calls := gitDoor(t, oneFile, "", 0)
	for _, args := range []map[string]any{
		{"server": "vis", "remote": "/srv/app/"},
		{"server": "vis", "remote": "/srv/app", "timeoutSec": 601},
	} {
		if res := callTool(t, BuildBridgeServer(dial), "git-push", args); !res.IsError {
			t.Errorf("%v accepted: %q", args, text(res))
		}
	}
	if len(calls()) != 0 || len(tempLeft(t, repo)) != 0 {
		t.Fatalf("calls %v left %v", calls(), tempLeft(t, repo))
	}
}

// pull --ff-only exits 0 when the server is already ahead, so neither the
// answer nor the tool text may promise that the server now sits at our hash.
func TestGitPushTextAllowsServerAhead(t *testing.T) {
	d := toolDescriptions(t, BuildBridgeServer(nil))["git-push"]
	if strings.Contains(d, "if the server has commits your branch lacks, the push is refused") || !strings.Contains(d, "a server already ahead of your branch is left as is") {
		t.Fatalf("description: %q", d)
	}
	if strings.Contains(layoutGitPush("s", gitPushPlan{Dest: "/r/.git"}, gitBundle{Branch: "main", Commit: "0123456789"}, "/r", false, execOut{}), "fast-forwarded the server's checked-out branch to") {
		t.Fatal("answer claims the server is at our commit")
	}
}

// A partial clone would make git bundle create fetch missing objects from
// the promisor remote; the bridge's local git must stay offline.
func TestMakeBundlePartialClone(t *testing.T) {
	repo := gitRepo(t)
	git(t, repo, "config", "uploadpack.allowFilter", "true")
	partial := filepath.Join(t.TempDir(), "p")
	git(t, repo, "clone", "-q", "--filter=blob:none", "--no-checkout", "file://"+repo, partial)
	marker := filepath.Join(t.TempDir(), "fetched")
	git(t, partial, "config", "remote.origin.uploadpack", "touch "+marker+"; git-upload-pack")
	_, _, err := makeBundle(context.Background(), partial, "main", "sshgate.bundle")
	if err == nil || !strings.Contains(err.Error(), "partial clone") {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("bundle step contacted the promisor remote")
	}
	if left := tempLeft(t, partial); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}
