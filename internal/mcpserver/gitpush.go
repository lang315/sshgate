package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/lang315/sshgate/internal/files"
)

// git-push (spec 2026-11-13-mcp-git-push-design.md) lives in the bridge: it
// bundles a local branch, then makes two ordinary door calls, upload and exec.
// Every server command is one git command shown in full on the exec card.

var (
	winDrive    = regexp.MustCompile(`^/[A-Za-z]:`)
	winPath     = regexp.MustCompile(`^/[A-Za-z]:/.+`)
	winRoot     = regexp.MustCompile(`^/[A-Za-z]:$`)
	branchChars = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
)

// checkGitRemote: an SFTP-form absolute path that is safe inside double quotes
// under sh, PowerShell and cmd. With create, its parent must not be a root.
func checkGitRemote(remote string, create bool) error {
	if files.CheckAbs(remote) != nil || remote == "/" {
		return errors.New("remote must be an absolute, clean folder path such as /home/me/app or /C:/Users/me/app")
	}
	if winDrive.MatchString(remote) && !winPath.MatchString(remote) {
		return errors.New("a Windows remote must look like /C:/Users/me/app")
	}
	for _, r := range remote {
		if strings.ContainsRune("\"$`\\%!", r) || (r >= '\u2018' && r <= '\u201e') || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return errors.New("remote contains a character that is not allowed in a git-push path")
		}
	}
	if create {
		if p := path.Dir(remote); p == "/" || winRoot.MatchString(p) {
			return errors.New("with create, the repository folder must not sit directly in a root folder")
		}
	}
	return nil
}

// checkGitBranch is the Go-side charset check; git check-ref-format runs too.
func checkGitBranch(branch string) error {
	if !branchChars.MatchString(branch) || strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, "/") {
		return errors.New("branch may hold only letters, digits and . _ / - and must not start with - or /")
	}
	return nil
}

// gitPath turns an SFTP path into the form git takes on the server: Windows
// drops the leading slash (/C:/x → C:/x).
func gitPath(sftpPath string) string {
	if winDrive.MatchString(sftpPath) {
		return sftpPath[1:]
	}
	return sftpPath
}

type gitPushPlan struct {
	Dest    string // SFTP folder the bundle is uploaded into
	Name    string // the bundle's file name
	Command string // the one git command for exec
}

func planGitPush(remote, branch string, create bool) gitPushPlan {
	r := gitPath(remote)
	if create {
		dir, name := path.Dir(remote), "."+path.Base(remote)+".sshgate.bundle"
		return gitPushPlan{Dest: dir, Name: name,
			Command: `git clone -q -c core.autocrlf=false -b ` + branch + ` "` + gitPath(dir) + "/" + name + `" "` + r + `"`}
	}
	return gitPushPlan{Dest: remote + "/.git", Name: "sshgate.bundle",
		Command: `git -C "` + r + `" pull -q --ff-only --no-recurse-submodules "` + r + `/.git/sshgate.bundle" ` + branch}
}

type gitBundle struct {
	Rel    string // slash-separated path of the bundle file, relative to root
	Branch string
	Commit string
	Size   int64
}

// localGit runs git in root with every inherited GIT_* variable removed, so a
// GIT_DIR or GIT_CONFIG_* from the MCP client cannot point it elsewhere.
func localGit(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// makeBundle writes a bundle of exactly refs/heads/<branch> (branch "" = the
// one HEAD names) to a fresh 0700 folder inside root. cleanup removes that
// folder; on error it has already run.
func makeBundle(ctx context.Context, root, branch, name string) (b gitBundle, cleanup func(), err error) {
	cleanup = func() {}
	if branch == "" {
		if branch, err = localGit(ctx, root, "symbolic-ref", "-q", "--short", "HEAD"); err != nil {
			return b, cleanup, errors.New("HEAD is detached; pass branch")
		}
	}
	if err := checkGitBranch(branch); err != nil {
		return b, cleanup, err
	}
	if _, err := localGit(ctx, root, "check-ref-format", "--branch", branch); err != nil {
		return b, cleanup, fmt.Errorf("%q is not a valid branch name", branch)
	}
	if out, err := localGit(ctx, root, "rev-parse", "--is-shallow-repository"); err != nil {
		return b, cleanup, errors.New("the project folder is not a git repository")
	} else if out == "true" {
		return b, cleanup, errors.New("shallow repositories cannot be pushed (their bundle cannot be cloned); run git fetch --unshallow first")
	}
	commit, err := localGit(ctx, root, "rev-parse", "-q", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return b, cleanup, fmt.Errorf("no local branch %s with a commit", branch)
	}
	parent, pattern := root, ".sshgate-*"
	if fi, err := os.Lstat(filepath.Join(root, ".git")); err == nil && fi.IsDir() {
		parent, pattern = filepath.Join(root, ".git"), "sshgate-*"
	}
	dir, err := os.MkdirTemp(parent, pattern)
	if err != nil {
		return b, cleanup, err
	}
	cleanup = func() { os.RemoveAll(dir) }
	file := filepath.Join(dir, name)
	if err = os.Chmod(dir, 0o700); err == nil {
		_, err = localGit(ctx, root, "bundle", "create", "-q", file, "refs/heads/"+branch)
	}
	var fi os.FileInfo
	if err == nil {
		fi, err = os.Stat(file)
	}
	if err != nil {
		cleanup()
		return b, func() {}, fmt.Errorf("git bundle create failed: %v", err)
	}
	rel, _ := filepath.Rel(root, file)
	return gitBundle{Rel: filepath.ToSlash(rel), Branch: branch, Commit: commit, Size: fi.Size()}, cleanup, nil
}
