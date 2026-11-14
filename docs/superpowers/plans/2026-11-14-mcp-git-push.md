# Windows-aware tool text and bridge-side `git-push` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Teach agents the remote shell dialect through the bridge's tool descriptions, and add a bridge-only `git-push` tool that bundles a local branch and lands it on a server with the existing `upload` and `exec` door calls.

**Architecture:** Everything lives in the bridge (`internal/mcpserver`). `git-push` validates its inputs in Go, runs local git (read-only plus `git bundle create` into a temporary folder it removes), then makes two ordinary MCP-door calls: `upload` of the bundle, then `exec` of exactly one `git` command. The hub, protocol, app, CLI approver and audit are untouched.

**Tech Stack:** Go, `github.com/modelcontextprotocol/go-sdk/mcp`, `os/exec` for local git, existing `internal/rpc` door client.

**Spec:** `docs/superpowers/specs/2026-11-13-mcp-git-push-design.md`

## Global Constraints

- No change in `internal/hub`, `internal/broker`, `internal/rpc`, `app/`, protocol version, CLI approver or audit format.
- The tool never chains server commands: each `exec` carries exactly one `git …` command, no `&&`, `;`, `&`, `|`.
- Update command, exactly: `git -C "<R>" pull -q --ff-only --no-recurse-submodules "<R>/.git/sshgate.bundle" <branch>`.
- Create command, exactly: `git clone -q -c core.autocrlf=false -b <branch> "<P>/.<base>.sshgate.bundle" "<R>"`.
- `<R>`/`<P>` are the git form of the SFTP path: drop the leading `/` when the path matches `^/[A-Za-z]:/`, else unchanged.
- Local git runs with every environment variable whose name starts with `GIT_` removed.
- `timeoutSec`: 0 means 600; outside 1–600 is refused by the bridge; the same value goes to both door calls.
- Door descriptions are the caller's `description` prefixed `git-push 1/2: ` (upload) and `git-push 2/2: ` (exec).
- `go vet ./...` and `go test -race ./...` pass at the end of every task.

## Review Focus

- A branch with no local ref (typo, `origin/main`, `refs/heads/main`, unborn `HEAD` in an empty repo): a clear "no local branch" error, no hub call, no temp folder left. Test in Task 3.
- `remote` with a trailing `/`, `..` or `//` (`/srv/app/`, `/srv/../app`): refused before any git or hub call, since `upload` would refuse it anyway after a bundle was built. Test in Task 2.
- `create: true` whose parent is `/` or a drive root (`/app`, `/C:/app`): refused, the bundle would land in a root folder. Test in Task 2.
- The hub is down or the human denies the upload: the result is an error, `exec` is never called, the temp folder is gone. Test in Task 4.
- A path with a space or non-ASCII letters (`/C:/Users/Tên Việt/my app`): accepted and quoted in the command exactly. Test in Task 2.

---

### Task 1: Windows-aware tool descriptions (spec part A)

**Files:**
- Modify: `internal/mcpserver/bridge.go` (descriptions of `exec`, `sudo-exec`, `upload`, `list-servers`; lines 254–300)
- Test: `internal/mcpserver/bridge_test.go`

**Interfaces:**
- Produces: nothing new; text only.

- [ ] **Step 1: Write the failing test** `TestBridgeDescriptionsWindows` in `bridge_test.go`. List the bridge's tools through an in-memory client (as `inputSchemas` does, but collect `Description`) and assert:
  - `exec` contains `/C:/`, `PowerShell`, `$LASTEXITCODE` and `upload`; still contains `cd /app && ./run.sh` and `cat > path`.
  - `sudo-exec` contains `Linux and macOS only`.
  - `upload` contains `/C:/Users/`.
  - `list-servers` contains `every sshgate tool` and no longer contains `of exec and sudo-exec`.

- [ ] **Step 2: Run it to verify it fails**
  Run: `go test ./internal/mcpserver -run TestBridgeDescriptionsWindows -v`
  Expected: FAIL on the `exec` assertion.

- [ ] **Step 3: Rewrite the four descriptions** with the spec's facts (section "(A) Tool descriptions"); keep every existing sentence's fact. Required facts in `exec`: Linux and macOS use sh/bash (keep the `cd /app && ./run.sh` sentence, scoped to them); Windows OpenSSH runs `cmd.exe` or Windows PowerShell 5.1, chosen by the server's admin; a path like `/C:/...` from `upload`, `download` or `git-push`, or `C:\` in output, means Windows; in PowerShell 5.1 `&&`, `cat > file` and `/dev/null` do not work, join steps with `;` and check `$LASTEXITCODE`, and any failing program reports exit code 1; on a Windows server use `upload` to write a file. The `stdin` sentence says `cat > path` is for Linux and macOS.

- [ ] **Step 4: Run the package tests**
  Run: `go test -race ./internal/mcpserver`
  Expected: PASS.

- [ ] **Step 5: Commit**
  ```bash
  git add internal/mcpserver/bridge.go internal/mcpserver/bridge_test.go
  git commit -m "feat(bridge): describe Windows servers' shells in the tool text"
  ```

### Task 2: Input validation and command building

**Files:**
- Create: `internal/mcpserver/gitpush.go`
- Test: `internal/mcpserver/gitpush_test.go`

**Interfaces:**
- Produces (all in `gitpush.go`):
  - `func checkGitRemote(remote string, create bool) error`
  - `func checkGitBranch(branch string) error` (Go-side charset only; `git check-ref-format` runs in Task 3)
  - `func gitPath(sftpPath string) string`
  - `type gitPushPlan struct { Dest, Name, Command string }` (Dest: SFTP folder the bundle is uploaded into; Name: the bundle's file name; Command: the exec string)
  - `func planGitPush(remote, branch string, create bool) gitPushPlan`

- [ ] **Step 1: Write the failing tests** in `gitpush_test.go`:
  - `TestCheckGitRemote`: accepted `/srv/app`, `/home/me/my app`, `/C:/Users/Tên Việt/app`, `/c:/x/y`; refused (non-nil error) `""`, `srv/app`, `/`, `/srv/app/`, `/srv/../app`, `//srv/app`, `/C:`, `/C:/`, `/C:x/app`, each of `/a"b`, `/a$b`, ``/a`b``, `/a\b`, `/a%b`, `/a!b`, `/a‘b` (U+2018), `/a„b` (U+201E), `/a\x01b`, `/a\u200bb` (format rune). With `create == true`, also refused: `/app`, `/C:/app`; accepted `/srv/app`, `/C:/Users/app`.
  - `TestCheckGitBranch`: accepted `main`, `feat/x`, `v1.2_rc-3`; refused `""`, `-x`, `/x`, `a b`, `a~1`, `a^`, `a:b`, `a@{u}`, `ä`.
  - `TestGitPath`: `/C:/Users/me` → `C:/Users/me`; `/c:/x` → `c:/x`; `/srv/app` → `/srv/app`.
  - `TestPlanGitPush` (exact strings):
    - update `/srv/my app`, `main` → `{Dest: "/srv/my app/.git", Name: "sshgate.bundle", Command: "git -C \"/srv/my app\" pull -q --ff-only --no-recurse-submodules \"/srv/my app/.git/sshgate.bundle\" main"}`
    - update `/C:/Users/me/app`, `feat/x` → `Command: "git -C \"C:/Users/me/app\" pull -q --ff-only --no-recurse-submodules \"C:/Users/me/app/.git/sshgate.bundle\" feat/x"`, `Dest: "/C:/Users/me/app/.git"`
    - create `/C:/Users/Tên Việt/app`, `main` → `{Dest: "/C:/Users/Tên Việt", Name: ".app.sshgate.bundle", Command: "git clone -q -c core.autocrlf=false -b main \"C:/Users/Tên Việt/.app.sshgate.bundle\" \"C:/Users/Tên Việt/app\""}`
    - create `/srv/app`, `main` → `Dest: "/srv"`, `Command: "git clone -q -c core.autocrlf=false -b main \"/srv/.app.sshgate.bundle\" \"/srv/app\""`
    - Every `Command` contains none of `&&`, `;`, `|`, `&`.

- [ ] **Step 2: Run them to verify they fail**
  Run: `go test ./internal/mcpserver -run 'TestCheckGit|TestGitPath|TestPlanGitPush' -v`
  Expected: FAIL, undefined functions.

- [ ] **Step 3: Implement the five symbols in `gitpush.go`.** `checkGitRemote`: `files.CheckAbs` (import `internal/files`), not `/`; a path starting `/X:` (letter, colon) must match `^/[A-Za-z]:/.+`; refuse the listed characters, `unicode.IsControl`, `unicode.Is(unicode.Cf, r)`, and U+2018–U+201E; with `create`, refuse when `path.Dir(remote)` is `/` or matches `^/[A-Za-z]:$`. `checkGitBranch`: `^[A-Za-z0-9._/-]+$` and no leading `-` or `/`. `planGitPush` uses `path.Dir`/`path.Base` on the SFTP path and `gitPath` for both quoted paths.

- [ ] **Step 4: Run them to verify they pass**
  Run: `go test -race ./internal/mcpserver -run 'TestCheckGit|TestGitPath|TestPlanGitPush' -v`
  Expected: PASS.

- [ ] **Step 5: Commit**
  ```bash
  git add internal/mcpserver/gitpush.go internal/mcpserver/gitpush_test.go
  git commit -m "feat(bridge): validate git-push inputs and build its git commands"
  ```

### Task 3: Local bundle

**Files:**
- Modify: `internal/mcpserver/gitpush.go`
- Test: `internal/mcpserver/gitpush_test.go`

**Interfaces:**
- Consumes: `checkGitBranch` (Task 2).
- Produces:
  - `type gitBundle struct { Rel, Branch, Commit string; Size int64 }` (Rel: slash-separated path of the bundle file relative to root, for `upload`'s `local`)
  - `func makeBundle(ctx context.Context, root, branch, name string) (b gitBundle, cleanup func(), err error)` — `branch == ""` means the branch `HEAD` names; `cleanup` is non-nil whenever a temp folder was made and removes it; on error `cleanup` has already run.

- [ ] **Step 1: Write the failing tests.** Add a helper `gitRepo(t) string` that makes a repo in `t.TempDir()` with one commit on `main`, running git with `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_NOSYSTEM=1` and author/committer env set (via `t.Setenv`), and skips the test when `exec.LookPath("git")` fails. Tests:
  - `TestMakeBundleDefaultBranch`: `makeBundle(ctx, repo, "", "sshgate.bundle")` → `Branch == "main"`, `Commit` equals `git rev-parse main`, `Size > 0`, `Rel` matches `^\.git/sshgate-[0-9a-f]+/sshgate\.bundle$`, the file's first line is `# v2 git bundle`, and its only ref line is `<Commit> refs/heads/main`; the temp folder's mode is `0700`; after `cleanup()` the temp folder is gone.
  - `TestMakeBundleNamedBranch`: a second branch `feat/x` with one more commit; `makeBundle(..., "feat/x", ".app.sshgate.bundle")` → `Branch == "feat/x"`, ref line `refs/heads/feat/x`, `Rel` ends `/.app.sshgate.bundle`.
  - `TestMakeBundleDetached`: `git checkout --detach`; `branch == ""` → error containing `HEAD is detached`; no `sshgate-*` left in `.git`.
  - `TestMakeBundleNoLocalBranch`: `branch` `origin/main`, `refs/heads/main`, `nope`, and an empty repo (`git init`, no commit) with `branch == ""` → error containing `no local branch`; no temp folder left.
  - `TestMakeBundleShallow`: `git clone --depth 1 file://<repo>`; → error containing `shallow`.
  - `TestMakeBundleWorktree`: `git worktree add <dir>/wt -b wt`; root `<dir>/wt` → `Rel` matches `^\.sshgate-[0-9a-f]+/sshgate\.bundle$`.
  - `TestMakeBundleIgnoresGitEnv`: `t.Setenv("GIT_DIR", t.TempDir())` and `t.Setenv("GIT_WORK_TREE", t.TempDir())` → still succeeds with `Branch == "main"`.

- [ ] **Step 2: Run them to verify they fail**
  Run: `go test ./internal/mcpserver -run TestMakeBundle -v`
  Expected: FAIL, undefined `makeBundle`.

- [ ] **Step 3: Implement `makeBundle`** in `gitpush.go`, git run as `exec.CommandContext(ctx, "git", args...)` with `Dir = root` and `Env` = `os.Environ()` minus `GIT_*`. Order:
  1. If `branch == ""`: `git symbolic-ref -q --short HEAD`; failure → `HEAD is detached; pass branch`.
  2. `checkGitBranch`, then `git check-ref-format --branch <b>`.
  3. `git rev-parse --is-shallow-repository` printing `true` → `shallow repositories cannot be pushed (their bundle cannot be cloned); run git fetch --unshallow first`.
  4. `git rev-parse -q --verify refs/heads/<b>^{commit}`; failure → `no local branch <b> with a commit`.
  5. Temp folder: `os.MkdirTemp` with pattern `sshgate-*` in `<root>/.git` when that is a directory (`os.Lstat`), else pattern `.sshgate-*` in `root`; `os.Chmod(dir, 0o700)`.
  6. `git bundle create -q <abs path> refs/heads/<b>`; `os.Stat` for `Size`.
  Error texts carry git's trimmed stderr only for step 6.

- [ ] **Step 4: Run them to verify they pass**
  Run: `go test -race ./internal/mcpserver -run TestMakeBundle -v`
  Expected: PASS (or SKIP when git is missing).

- [ ] **Step 5: Commit**
  ```bash
  git add internal/mcpserver/gitpush.go internal/mcpserver/gitpush_test.go
  git commit -m "feat(bridge): build a one-branch git bundle in a temporary folder"
  ```

### Task 4: The `git-push` tool

**Files:**
- Modify: `internal/mcpserver/bridge.go` (package-level `execOut` type used by `execTool`; register `git-push`; `BuildBridgeServer` doc comment)
- Modify: `internal/mcpserver/gitpush.go` (`bridgeGitPushInput`, `layoutGitPush`)
- Test: `internal/mcpserver/gitpush_test.go`, `internal/mcpserver/bridge_test.go` (`TestTransferToolsOnlyOnBridge` gains `git-push`)

**Interfaces:**
- Consumes: `checkGitRemote`, `planGitPush`, `gitPushPlan` (Task 2); `makeBundle`, `gitBundle` (Task 3); existing `callHub`, `transferOut`, `layoutTransfer`, `layoutExec`, `withHub` (closure in `BuildBridgeServer`), `transferOld`.
- Produces:
  - `type bridgeGitPushInput struct { Server, Remote, Branch string; Create bool; Description string; TimeoutSec int }` with JSON names `server`, `remote`, `branch`, `create`, `description`, `timeoutSec` and `jsonschema` text stating the spec's facts (remote SFTP form with the `/C:/` example; branch default; create semantics; 1–600 default 600).
  - `type execOut struct { ExitCode int \`json:"exitCode"\`; Stdout, Stderr string; Redacted map[string]int }` replacing the anonymous struct in `execTool`.
  - `func layoutGitPush(server string, p gitPushPlan, b gitBundle, remote string, create bool, out execOut) string`

- [ ] **Step 1: Write the failing tests.** A fake door `gitDoor(t, uploadResp any, uploadErr string, exitCode int)` modelled on `doorRecorder` that serves `upload` (returns `uploadResp` or an `rpc.Error` with `uploadErr`) and `exec` (returns `{"exitCode": exitCode, "stdout": "", "stderr": ""}`), recording `[method, params]`. Each test `t.Chdir(gitRepo(t))`.
  - `TestGitPushUpdate`: `{"server": "vis", "remote": "/srv/app", "description": "deploy"}` with upload `{"files": 1, "bytes": 99}`, exit 0 → not an error; door calls in order `upload` then `exec`; upload params `local == [<Rel>]` (matches `^\.git/sshgate-[0-9a-f]+/sshgate\.bundle$`), `remote == ["/srv/app/.git"]`, `overwrite == true`, `description == "git-push 1/2: deploy"`, `timeoutSec == 600`, `root == wd`; exec params `command == planGitPush("/srv/app", "main", false).Command`, `description == "git-push 2/2: deploy"`, `timeoutSec == 600`; text starts `uploaded bundle of main <hash7> (` and contains `fast-forwarded the server's checked-out branch to main <hash7>` and `exit code: 0`; afterwards `.git` holds no `sshgate-*`.
  - `TestGitPushCreate`: `create: true`, remote `/C:/Users/me/app` → upload `remote == ["/C:/Users/me"]`, `local` ends `/.app.sshgate.bundle`; exec command equals `planGitPush(..., true).Command`; text contains `created /C:/Users/me/app at main <hash7>` and `/C:/Users/me/.app.sshgate.bundle can be deleted`.
  - `TestGitPushNotFastForward`: exit 1 → not a tool error; text contains `exit code: 1` and `hint: if the server's branch has commits yours lacks, git-push will not replace them; to discard them, run git fetch and git reset --hard with exec (the human sees it)`. With `create: true` and exit 1: `hint: if the folder already exists, call git-push without create`.
  - `TestGitPushUploadDenied`: upload error `denied by user` → tool error with text `denied by user`; exactly one door call (`upload`); temp folder gone.
  - `TestGitPushUploadNotOneFile`: upload `{"files": 0, "errorCount": 1, "errors": ["x: permission denied"]}` → tool error containing `x: permission denied`; no `exec` call.
  - `TestGitPushHubDown`: dial returns an error → tool error `Open the app to approve commands`; temp folder gone.
  - `TestGitPushRefusesBeforeHub`: remote `/srv/app/`, then `timeoutSec: 601` → tool errors, zero door calls, no temp folder.
  - `TestTransferToolsOnlyOnBridge`: also asserts `git-push` is on the bridge and not in `--host` mode.

- [ ] **Step 2: Run them to verify they fail**
  Run: `go test ./internal/mcpserver -run 'TestGitPush|TestTransferToolsOnlyOnBridge' -v`
  Expected: FAIL, unknown tool `git-push`.

- [ ] **Step 3: Implement.** In `BuildBridgeServer`, register `git-push` (description: the spec's "What the tool says" facts) with a handler that: checks `timeoutSec` (0 → 600; outside 1–600 → error), `checkGitRemote`, `planGitPush` with the branch resolved by `makeBundle` (call `makeBundle(ctx, wd, in.Branch, plan.Name)` where `plan.Name` is `sshgate.bundle` or `.<base>.sshgate.bundle`, then build the final plan with `b.Branch`), `defer cleanup()`; `withHub` → `callHub(..., "upload", params, &transferOut, transferOld)` with `requestId`, `client`, `server`, `root`, `local`, `remote`, `overwrite: true`, `description`, `timeoutSec`; refuse unless `Files == 1 && ErrorCount == 0` (error text `layoutTransfer("upload", out)`); a second `withHub` → `callHub(..., "exec", ...)` into `execOut`; return `textOK(layoutGitPush(...))`. `layoutGitPush` output, in order: `uploaded bundle of <branch> <hash7> (<Size> bytes) to <server>:<Dest>\n`; on exit 0 the update or create line; then `layoutExec(out...)`; on non-zero exit the matching `hint:` line. Update the `BuildBridgeServer` comment to the spec's new bridge invariant.

- [ ] **Step 4: Run the package tests**
  Run: `go test -race ./internal/mcpserver`
  Expected: PASS.

- [ ] **Step 5: Commit**
  ```bash
  git add internal/mcpserver/
  git commit -m "feat(bridge): add the git-push tool over upload and exec"
  ```

### Task 5: End to end, docs, and the manual exit gate

**Files:**
- Modify: `cmd/sshgate/e2e_test.go` (`TestEndToEndAutoAllow`, after the download round trip)
- Modify: `CLAUDE.md` (first paragraph's tool count, "Hub and bridge" bridge bullet, MCP tools mention)

**Interfaces:**
- Consumes: the `git-push` tool (Task 4); `planGitPush` is not visible from `cmd/sshgate`, so the test spells the expected strings.

- [ ] **Step 1: Write the failing e2e step.** In `TestEndToEndAutoAllow`, after the download checks: make `proj` a git repo with one commit on `main` (same env as `gitRepo`; skip this part when git is missing), then call `git-push` `{"server": "box", "remote": "/home/app", "create": true}`. Assert: not an error; the text contains `stdout:\ngit clone -q -c core.autocrlf=false -b main "/home/.app.sshgate.bundle" "/home/app"` (sshtest echoes the command); `<sftpRoot>/home/.app.sshgate.bundle` starts with `# v2 git bundle\n` and holds the line `<git rev-parse main> refs/heads/main`. Then `os.MkdirAll(<sftpRoot>/home/app/.git)`, call `git-push` `{"server": "box", "remote": "/home/app"}`, and assert the echoed `git -C "/home/app" pull -q --ff-only --no-recurse-submodules "/home/app/.git/sshgate.bundle" main` and that the bundle file exists there; the last two audit records are a `kind: "file"` record and an exec record with `approval: "auto"`; `proj/.git` holds no `sshgate-*`.

- [ ] **Step 2: Run it to verify it fails, then passes after Task 4 is in**
  Run: `go test -race ./cmd/sshgate -run TestEndToEndAutoAllow -v`
  Expected: PASS (it fails only if Task 4 is missing).

- [ ] **Step 3: Update CLAUDE.md**: bridge forwards six tools (`exec`, `sudo-exec`, `list-servers`, `upload`, `download`, `git-push`); the bridge bullet states the new invariant (read-only local git plus `git bundle create` into a temp folder it removes, no policy; every server action is an ordinary `upload` and `exec`); one line on `git-push` pointing at the spec.

- [ ] **Step 4: Full verification**
  Run: `go vet ./... && go test -race ./...`
  Expected: exit 0.

- [ ] **Step 5: Commit**
  ```bash
  git add cmd/sshgate/e2e_test.go CLAUDE.md
  git commit -m "test: git-push end to end; docs: bridge runs local git for git-push"
  ```

- [ ] **Step 6: Manual exit gate (needs the human).** Build `go build -o <scratchpad>/sshgate ./cmd/sshgate`; the human registers it as a second MCP server (`claude mcp add sshgate-dev -- <scratchpad>/sshgate`) and restarts Claude Code (the MCP door is unchanged, so the installed app's hub serves it). With a throwaway repo, on buildpc (`remote` under `/C:/Users/<user>/Source/`) and on one Linux server: `create: true`; a new commit then an update; `exec git -C "<R>" rev-parse HEAD` equals the local hash; an amend then an update exits non-zero with the hint and `rev-parse HEAD` unchanged; a push of a second branch fast-forwards the checked-out one; `audit.jsonl` shows a file and an exec record per push (`approval: "auto"` when a grant is on). Record each result, and record `cmd.exe` DefaultShell as unchecked unless such a server exists. Afterwards the human removes `sshgate-dev`.
