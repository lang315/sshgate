# sshgate: Windows-aware tool text and a bridge-side `git-push` tool — Design

Date: 2026-11-13, revised 2026-11-14
Status: Approved in chat 2026-11-14. Approach A+C chosen after a five-reviewer critique of the first draft (a hub request type, now rejected; see Approaches). The agent learns the server's OS only from the tool descriptions (A); adding the server's home path to `upload` answers was offered and declined (it needs a hub change).

## Goal

Let an AI agent driving the MCP bridge put the committed state of one branch of its project onto a saved server, Linux, macOS or Windows, without any git hosting or forge credentials on that server, through the approvals and audit sshgate already has. And stop teaching agents POSIX-only shell on servers that are Windows.

Why: users test on machines they will not give a GitHub login to (first case: a Windows test PC whose OpenSSH default shell is PowerShell 5.1). The manual recipe works today, `git bundle` locally, `upload`, then `exec` of `git fetch`/`merge`, but the agent must know the remote shell's dialect, and the `exec` description actively teaches `cd /app && ./run.sh` and `cat > path`, both wrong in PowerShell 5.1.

Exit gate (manual): from Claude Code, on buildpc (Windows, PowerShell default shell) and on a Linux server: `git-push` with `create: true` makes the repository; after a new local commit, `git-push` fast-forwards it; `git rev-parse HEAD` there equals the local hash; after an amend, `git-push` is refused as not a fast-forward and the server is unchanged; each push shows in `audit.jsonl` as one `kind: "file"` record and one exec record, `approval: "auto"` under a grant.

## Approaches (2026-11-14)

1. A project skill (written recipe). Rejected: serves one user's agent only, and leaves the dialect to the agent.
2. A hub request type `gitPush` (the first draft): one approval, the hub drives SFTP and git on the server. Rejected after review: the hub would run git behind a card that shows a summary, not a command, so it must neutralise everything git can run or overwrite (hooks via `core.hooksPath` and `hook.<event>.command`, repo-local filter drivers, submodule fetches over the server's credentials, ignored files overwritten by a fast-forward, case-aliased branch names moving the checked-out branch on NTFS/APFS; several reproduced with git 2.56), plus atomic clone, cleanup on a cancelled context, a bundle swap between approval and upload, protocol 14, a new audit kind and a git-running test server. Estimated 4–5k lines.
3. **Chosen, A+C**: (A) rewrite the bridge's tool descriptions so agents write the right dialect; (C) a bridge-only `git-push` tool that builds a bundle locally and then calls the existing `upload` and `exec` door methods. The approver sees the literal `git` command on the exec card, so what git runs on the server is an informed human decision, exactly as an agent's own `exec` of `git pull` is today: the tool adds no capability that `upload` plus `exec` do not already grant. No hub, protocol, app, CLI-approver or audit change. Cost: two approvals per push instead of one (zero under a grant).

Also rejected: a git remote helper or any byte pipe to a remote `git-receive-pack` (bypasses output redaction; the fetch direction would bypass the download scan).

## Checked on buildpc (2026-11-14, Windows, DefaultShell `powershell.exe` 5.1.19041)

- Double quotes survive into git.exe: `git rev-parse --sq-quote "a b" c` gave `'a b' 'c'`; `"x;echo INJECTED"` arrived as one argument and nothing was echoed; an empty `-c` value `"core.hooksPath="` and a non-ASCII path arrived intact.
- A git command that writes stderr and succeeds reports exit code 0; one that exits 128 reports exit code 1.
- Not checked: `cmd.exe` as DefaultShell (the Windows OpenSSH default).

Checked locally (git 2.56, with the exact strings pinned in step 4): the create string clones and stores `core.autocrlf=false` in the new repository; the update string fast-forwards (exit 0, server `HEAD` equals the local commit); after a local amend it exits 128 with "Not possible to fast-forward, aborting" and the server's `HEAD` and work tree are unchanged; with the server on `main` and a push of `feat/x` (ahead of `main`), the server's `main` moves to the `feat/x` commit and no `feat/x` branch is created. `git pull` does not accept `--no-overwrite-ignore` (exit 129), and a fast-forward replaces a file that is ignored on the server (via `.git/info/exclude`) when the pushed commit tracks it.

## Scope

In: (A) text of the `exec`, `sudo-exec`, `upload` and `list-servers` descriptions; (C) the bridge tool `git-push`; CLAUDE.md's bridge paragraph.

Out (non-goals): any hub, MCP-door, UI-door, app, CLI-approver or audit change; pulling back to local; uncommitted work; force push or history replacement (the refusal tells the agent how to do it with `exec`, which the human then approves as such); incremental bundles; removing the uploaded bundle; LFS objects and submodules (not carried by a bundle); SHA-256 repositories (not tested); `--host` mode.

Anti-goals: the tool never runs a server command that is not shown in full on an exec card; the tool never chains server commands (`&&`, `;`, `&` differ between sh, PowerShell and cmd); nothing on the server ever points at a forge because of the tool.

## (A) Tool descriptions

Rewrite, keeping every existing fact:

- `exec`: the shell depends on the server's OS. Linux and macOS: sh/bash; write `cd /app && ./run.sh` as one command. Windows OpenSSH: `cmd.exe` or Windows PowerShell 5.1, set by the server's admin; a remote path of the form `/C:/...` from `upload`, `download` or `git-push`, or `C:\` in output, means Windows. In PowerShell 5.1 `&&`, `cat > file` and `/dev/null` do not work: join steps with `;` and check `$LASTEXITCODE`; any failing program reports exit code 1 there. On a Windows server use `upload` to write a file, not `stdin` with `cat`.
- `sudo-exec`: add "Linux and macOS only".
- `upload`: remote paths on Windows are `/C:/Users/...`; the `stdin` hint (`cat > path`) is for Linux and macOS.
- `list-servers`: "Use these names as the `server` argument of every sshgate tool".

Wording is the builder's; the facts above are fixed. A test pins the presence of the Windows sentence and the `/C:/` form.

## (C) `git-push`

`git-push {server, remote, branch?, create?, description, timeoutSec?}`

- `remote`: the repository folder on the server in SFTP form, absolute and clean (`/home/me/src/app`, `/C:/Users/me/src/app`). With `create: true` it must not exist and its parent must; otherwise it must be a git work tree with a `.git` folder.
- `branch`: the local branch whose commit is sent; default the branch `HEAD` names. Detached `HEAD` without `branch`: error asking for one. On an update, whatever branch is checked out on the server is fast-forwarded to that commit (git `pull` semantics, checked above); the server's branch names are not matched to `branch`, and no branch is created there. Call made 2026-11-14: a test server mirrors the agent's current work in its one checkout; the exec card shows the literal `git pull … <branch>`.
- `create` (default false): make the repository with `git clone`.
- `timeoutSec`: 1–600, default 600; used for each of the two calls.

Steps, all in the bridge:

1. **Validate**, no hub call: `remote` passes `files.CheckAbs`-equivalent checks, is not `/`, and holds none of `"`, `$`, `` ` ``, `\`, `%`, `!`, U+2018–U+201E, control or format runes (Windows form: must match `^/[A-Za-z]:/.+`); `branch` matches `^[A-Za-z0-9._/-]+$`, starts with neither `-` nor `/`, and passes `git check-ref-format --branch`.
2. **Bundle**, local git run with every inherited `GIT_*` variable removed from its environment, in the bridge's working directory (the project root `upload` already uses): resolve `branch`; refuse a shallow repository (`git rev-parse --is-shallow-repository` prints `true`: its bundle cannot be cloned); `git bundle create <tmp>/<name> refs/heads/<branch>`, where `<tmp>` is a fresh `0700` folder `sshgate-<random>` inside `<root>/.git` when that is a folder, else `.sshgate-<random>` at the top of the root (a linked worktree), removed when the call returns, whatever the outcome. `<name>` is `sshgate.bundle` for an update and `.<basename of remote>.sshgate.bundle` for a create.
3. **Upload**: the door's `upload` with `local: [<tmp relative to root>/<name>]`, `remote: [<remote>/.git]` (update) or `[<parent of remote>]` (create), `overwrite: true`, the caller's `description` prefixed `git-push 1/2: `. Any error, or anything but exactly one file uploaded, ends the call with the upload's text.
4. **Exec**: the door's `exec`, description prefixed `git-push 2/2: `, with exactly one git command. Paths are the git form of the SFTP path: on Windows the leading `/` is dropped (`C:/Users/me/src/app`), elsewhere unchanged; both in double quotes.
   - update: `git -C "<remote>" pull -q --ff-only --no-recurse-submodules "<remote>/.git/sshgate.bundle" <branch>`
   - create: `git clone -q -c core.autocrlf=false -b <branch> "<parent>/.<base>.sshgate.bundle" "<remote>"`
5. **Answer**: one line per step (`uploaded bundle of <branch> <hash7> (<size>) to <server>:<dest>`; on an exit 0 update, `fast-forwarded the server's checked-out branch to <branch> <hash7>`), then the exec result in the usual `layoutExec` form. On a non-zero exit, add a fixed hint: for an update, "if the server's branch has commits yours lacks, git-push will not replace them; to discard them, run `git fetch` and `git reset --hard` with exec (the human sees it)"; for a create, "if the folder exists, call git-push without create". After a create, a note that the bundle beside the repository can be deleted.

What the tool says (tool description, fixed facts): pushes committed work of one branch, never uncommitted changes; the server's checked-out branch, whatever its name, is fast-forwarded to it, never rewritten; two approvals (upload, then the git command), none under auto-allow; the server's hooks run as for any `git pull`; a file the server ignores is replaced if your commit tracks it; submodules and LFS objects are not sent; shallow repositories are refused.

Bridge invariant (CLAUDE.md and the `BuildBridgeServer` comment change with it): the bridge now runs read-only local git plus `git bundle create` into a temporary folder it removes, and holds no policy; every server action is an ordinary `upload` and `exec` the hub decides and audits.

## Testing

- Validation: each refused `remote` character, the Windows form, `/` and `/C:`; branch names (leading `-`, `..`, `@{`, `a.lock`), and acceptance of `feat/x`.
- Command building: update and create, POSIX and Windows paths, a path with a space and a non-ASCII name; the strings are pinned exactly.
- Bundle step against a real temporary repository: default branch, detached `HEAD` refused, shallow refused, `.git`-is-a-file uses the top-level temp folder, the temp folder is gone after success and after a hub error, an inherited `GIT_DIR` pointing elsewhere is ignored.
- `cmd/sshgate` e2e (bridge to hub to `sshtest`, which serves SFTP and echoes exec commands): an approved `git-push` uploads a bundle whose header names the branch and commit, and the echoed exec command equals the expected string; a denied upload stops before exec.
- Tool descriptions: the Windows sentence, `/C:/` form and the `list-servers` wording are present.
- Answer text: the update line names the server's checked-out branch semantics; the non-zero hints for update and create.
- Manual exit gate on buildpc and a Linux server (above), including a push of a branch other than the one checked out on the server (it fast-forwards the checked-out one), plus one run on a Windows server whose DefaultShell is `cmd.exe` if one is available, else recorded as unchecked.

## Left to the builder

Exact description wording within the facts above; size formatting; Go names; how the e2e test reads the uploaded bundle header.
