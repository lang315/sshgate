package mcpserver

import (
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
