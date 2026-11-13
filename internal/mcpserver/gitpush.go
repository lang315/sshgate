package mcpserver

import (
	"errors"
	"path"
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
