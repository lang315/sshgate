# Task 4: Engine — list, mkdir, rename — TDD Report

## Summary
Implemented `internal/files/list.go` (`Entry`, `Listing`, `Home`, `List`, `Mkdir`, `Rename`) plus test helpers `remote`/`write`/`read` in `internal/files/helpers_test.go`, exactly as given in the brief. Tested against Task 1's in-process `sshtest` SFTP server. All code matches the brief verbatim — no deviations.

## Files Created
- `internal/files/list.go` — `Entry`, `Listing`, `kind`, `Home`, `List`, `Mkdir`, `Rename`
- `internal/files/list_test.go` — `TestListHomeKindsAndLinks`, `TestListRejectsRelativeAndCountsBadNames`, `TestListTruncates`, `TestListPermissionDenied`, `TestMkdirAndRename`
- `internal/files/helpers_test.go` — `remote`, `write`, `read` (shared with Tasks 5–6)

## TDD: RED Step

Command:
```
go test ./internal/files -run 'TestList|TestMkdir' -v
```

Output:
```
# github.com/lang315/sshgate/internal/files [github.com/lang315/sshgate/internal/files.test]
internal/files/list_test.go:18:12: undefined: List
internal/files/list_test.go:25:20: undefined: Entry
internal/files/list_test.go:39:15: undefined: List
internal/files/list_test.go:43:12: undefined: List
internal/files/list_test.go:59:12: undefined: List
internal/files/list_test.go:76:15: undefined: List
internal/files/list_test.go:83:12: undefined: Mkdir
internal/files/list_test.go:86:12: undefined: Mkdir
internal/files/list_test.go:94:12: undefined: Rename
internal/files/list_test.go:97:12: undefined: Rename
internal/files/list_test.go:97:12: too many errors
FAIL	github.com/lang315/sshgate/internal/files [build failed]
FAIL
```

Build failed on the new symbols as expected.

## TDD: GREEN Step

Implemented `list.go` per brief specification. Full package test run:

```
go test -race ./internal/files/ -v
=== RUN   TestListHomeKindsAndLinks
--- PASS: TestListHomeKindsAndLinks (0.02s)
=== RUN   TestListRejectsRelativeAndCountsBadNames
--- PASS: TestListRejectsRelativeAndCountsBadNames (0.01s)
=== RUN   TestListTruncates
--- PASS: TestListTruncates (1.23s)
=== RUN   TestListPermissionDenied
--- PASS: TestListPermissionDenied (0.01s)
=== RUN   TestMkdirAndRename
--- PASS: TestMkdirAndRename (0.01s)
=== RUN   TestRemoteNameRules
--- PASS: TestRemoteNameRules (0.00s)
=== RUN   TestLocalNameRules
--- PASS: TestLocalNameRules (0.00s)
=== RUN   TestPathRules
--- PASS: TestPathRules (0.00s)
=== RUN   TestModes
--- PASS: TestModes (0.00s)
=== RUN   TestErrorsKeepFirstTwenty
--- PASS: TestErrorsKeepFirstTwenty (0.00s)
=== RUN   TestFatal
--- PASS: TestFatal (0.00s)
=== RUN   TestPartName
--- PASS: TestPartName (0.00s)
PASS
ok  	github.com/lang315/sshgate/internal/files	1.291s
```

## Verification

Ran together with exit statuses, per the CLAUDE.md pre-commit gate:

```
$ go vet ./... ; echo "vet=$?"
vet=0
$ GOOS=windows go vet ./... ; echo "win_vet=$?"
win_vet=0
$ go test -race ./internal/files/ ; echo "race=$?"
ok  	github.com/lang315/sshgate/internal/files	(cached)
race=0
```

## Implementation Notes

### list.go
- `Entry`: JSON-tagged listing row; `Mode` holds permission bits only (`fi.Mode().Perm()`), `Kind` is one of `dir`/`file`/`link`/`other`, `Target` is populated only for symlinks (best-effort `ReadLink`, error ignored — a link whose target can't be read is still listed with an empty target).
- `Listing`: `Path` is the resolved absolute folder; `Bad` counts names that failed `checkRemoteName` (from Task 3) and were skipped rather than surfaced as entries.
- `Home`: thin wrapper on `c.RealPath(".")`.
- `List`: `""` resolves to the login home; any other path must pass `CheckAbs` (relative paths rejected). Reads via `c.ReadDirContext` (bounded by the caller's context — the whole folder is buffered by `pkg/sftp` v1.13.11, hence the `ponytail:` comment on the upgrade path). Stops appending at `MaxList` and sets `Truncated`; bad names are counted but don't count toward truncation.
- `Mkdir`: `CheckAbs` then plain `c.Mkdir`.
- `Rename`: resolves home, applies `checkMutable` (from Task 3) to both `from` and `to` (refuses `/` and the home directory itself), then an existence check on `to` via `Lstat` to refuse silently replacing anything — including a directory — before calling the plain (non-POSIX) SFTP rename, which the server itself also refuses onto an existing name.

## Deviations from Brief

None. `list.go`, `list_test.go`, and `helpers_test.go` are exactly the code given in the brief. No adjustments were needed for `go vet`, `GOOS=windows go vet`, or `go test -race`.

## Concerns

- `Rename`'s pre-check (`Lstat` then `Rename`) is inherently check-then-act (TOCTOU); it isn't atomic against a concurrent create of `to` between the two calls. This is fine per the brief: the server's own plain-rename semantics (verified in Task 1, `TestSFTPAgainstOpenSSH`) also refuse to replace an existing name, so the `Lstat` is a fast, clearer-error pre-check, not the sole enforcement.
- `TestListTruncates` writes `MaxList+1` (10,001) local files, so it costs ~1.2s of the ~1.3s test run. Not a correctness concern, just the slowest test in the package.

## Fix Report (review round 1)

Task review found two Important issues. Both fixed.

### Issue 1: `List`'s symlink loop ignored ctx

**Problem:** the per-symlink `c.ReadLink` loop in `List` (`internal/files/list.go`) did not check `ctx` between entries, so a folder with many symlinks kept issuing `ReadLink` round trips after the caller's deadline/cancellation.

**Change:** added `if err := ctx.Err(); err != nil { return Listing{}, err }` at the top of the loop body, before the name check. Extended the existing `ponytail:` comment on `List` with one line: a single hung `ReadLink` on the shared client cannot itself be cancelled (`pkg/sftp` has no `ReadLinkContext`, and closing the shared client would kill other listings in flight); the connection's keepalive is the backstop for that case.

**Test added:** `TestListRespectsCancelledContext` (`internal/files/list_test.go`) — lists `/home` (containing a file and two symlinks) with an already-cancelled context and asserts `List` returns a non-nil error.

**Which path the test exercises:** I checked `pkg/sftp` v1.13.11's `clientConn.sendPacket` (`conn.go:155`): it dispatches the wire request unconditionally, then does `select { case <-ctx.Done(): return ctx.Err(); case s := <-ch: ... }`. With an already-cancelled context passed in before `List` is even called, `ctx.Done()` is closed at the moment `opendir`'s `sendPacket` evaluates that select, while the server's response channel is not yet ready (real round trip). So `c.ReadDirContext` itself returns `ctx.Err()` before the loop is ever reached — this test exercises `ReadDirContext`'s own ctx handling, not the new per-entry check in the symlink loop. The new loop check is defensive for a context that expires *during* a listing (e.g. a slow `ReadLink` mid-loop after the deadline fires) — forcing that specific interleaving deterministically in a test would need an artificially slow/hostile `ReadLink` in `sshtest`, which the brief's fixtures don't offer, so per the review's own allowance I kept to the simplest test and documented the gap here rather than adding test-only server hooks.

### Issue 2: `Rename`'s comment overstated the server backstop

**Problem:** the comment said "OpenSSH's sftp-server itself refuses to run onto an existing name (verified by `TestSFTPAgainstOpenSSH`)" without noting that test is Docker-only and skipped under `-short` — implying a guarantee that isn't verified in every test run.

**Change:** reworded the comment to state plainly that the `Lstat` check on `to` is the enforced guard, and that the remaining check-then-rename window is covered on OpenSSH servers specifically (plain `SSH_FXP_RENAME` there also refuses to replace), verified by `TestSFTPAgainstOpenSSH` in CI (not in every local run).

**Tests added:** `TestRenameRefusesRootAndHomeAsTarget` (`internal/files/list_test.go`) covers three cases the existing `TestMkdirAndRename` didn't: `from == "/"`, `to == "/"`, and `to == home` (existing test only covered `from == home`).

### Covering tests run

```
$ go test -race ./internal/files/ -run 'TestList|TestMkdir' -v
=== RUN   TestListHomeKindsAndLinks
--- PASS: TestListHomeKindsAndLinks (0.02s)
=== RUN   TestListRejectsRelativeAndCountsBadNames
--- PASS: TestListRejectsRelativeAndCountsBadNames (0.01s)
=== RUN   TestListTruncates
--- PASS: TestListTruncates (1.26s)
=== RUN   TestListPermissionDenied
--- PASS: TestListPermissionDenied (0.01s)
=== RUN   TestListRespectsCancelledContext
--- PASS: TestListRespectsCancelledContext (0.01s)
=== RUN   TestMkdirAndRename
--- PASS: TestMkdirAndRename (0.01s)
PASS
ok  	github.com/lang315/sshgate/internal/files	2.793s

$ go test -race ./internal/files/ -v   # full package, includes TestRenameRefusesRootAndHomeAsTarget
=== RUN   TestListHomeKindsAndLinks
--- PASS: TestListHomeKindsAndLinks (0.02s)
=== RUN   TestListRejectsRelativeAndCountsBadNames
--- PASS: TestListRejectsRelativeAndCountsBadNames (0.01s)
=== RUN   TestListTruncates
--- PASS: TestListTruncates (1.20s)
=== RUN   TestListPermissionDenied
--- PASS: TestListPermissionDenied (0.01s)
=== RUN   TestListRespectsCancelledContext
--- PASS: TestListRespectsCancelledContext (0.01s)
=== RUN   TestMkdirAndRename
--- PASS: TestMkdirAndRename (0.01s)
=== RUN   TestRenameRefusesRootAndHomeAsTarget
--- PASS: TestRenameRefusesRootAndHomeAsTarget (0.01s)
=== RUN   TestRemoteNameRules
--- PASS: TestRemoteNameRules (0.00s)
=== RUN   TestLocalNameRules
--- PASS: TestLocalNameRules (0.00s)
=== RUN   TestPathRules
--- PASS: TestPathRules (0.00s)
=== RUN   TestModes
--- PASS: TestModes (0.00s)
=== RUN   TestErrorsKeepFirstTwenty
--- PASS: TestErrorsKeepFirstTwenty (0.00s)
=== RUN   TestFatal
--- PASS: TestFatal (0.00s)
=== RUN   TestPartName
--- PASS: TestPartName (0.00s)
PASS
ok  	github.com/lang315/sshgate/internal/files	2.488s

$ go vet ./... ; echo "vet=$?"
vet=0
$ GOOS=windows go vet ./... ; echo "win_vet=$?"
win_vet=0
```

## Fix Report (review round 2)

One open finding from round 1: `TestListRespectsCancelledContext` still passed with the loop's `ctx.Err()` check removed, because `ReadDirContext` fails first on an already-cancelled context — nothing actually covered the per-entry check added in round 1.

**Root cause:** `pkg/sftp` v1.13.11's `clientConn.sendPacket` (`conn.go:163`) only watches `ctx.Done()`; it never calls `ctx.Err()` itself. An already-cancelled `context.WithCancel` has both `Done()` closed and `Err()` non-nil, so it always trips `ReadDirContext`'s own check first — no test built on a plain cancelled context can ever reach the loop.

**Fix:** added a test-only context type that decouples the two signals:

```go
// errCtx passes pkg/sftp's Done()-based checks (its embedded Done() never
// closes) but reports cancellation to code that polls Err(), so a test can
// reach List's per-entry ctx check after ReadDirContext has already
// succeeded.
type errCtx struct{ context.Context }

func (errCtx) Err() error { return context.Canceled }
```

`errCtx{context.Background()}` has a `Done()` that never closes (so `ReadDirContext`'s select never takes the cancellation branch and the real listing succeeds), but its `Err()` always returns `context.Canceled`. `List`'s per-entry loop check polls `ctx.Err()` directly, so it is the only thing that can catch this.

**Test added:** `TestListPerEntryCtxCheck` (`internal/files/list_test.go`) — lists `/home` (containing one symlink, so the loop runs at least once) with `errCtx{context.Background()}` and asserts `errors.Is(err, context.Canceled)`.

**Confirmed the test catches a missing check:** temporarily removed the `if err := ctx.Err(); err != nil { return Listing{}, err }` line from `List`'s loop and re-ran just that test:

```
$ go test ./internal/files -run 'TestListPerEntryCtxCheck' -v
=== RUN   TestListPerEntryCtxCheck
    list_test.go:114: err <nil>, want context.Canceled
--- FAIL: TestListPerEntryCtxCheck (0.01s)
FAIL
FAIL	github.com/lang315/sshgate/internal/files	0.407s
FAIL
```

Then restored the check (file diffed back to identical with the committed version — confirmed via `git diff --stat` showing no changes) and re-ran to confirm it passes again (see below).

**Renamed the existing test** from `TestListRespectsCancelledContext` to `TestListReadDirContextCancelled` and updated its comment to say plainly that it covers `ReadDirContext`'s own cancellation path (via a plain already-cancelled `context.WithCancel`), not the per-entry loop check — per the review's guidance to keep it but retitle it.

### Covering tests run

```
$ go test -race ./internal/files/ -run 'TestList' -v
=== RUN   TestListHomeKindsAndLinks
--- PASS: TestListHomeKindsAndLinks (0.02s)
=== RUN   TestListRejectsRelativeAndCountsBadNames
--- PASS: TestListRejectsRelativeAndCountsBadNames (0.01s)
=== RUN   TestListTruncates
--- PASS: TestListTruncates (1.23s)
=== RUN   TestListPermissionDenied
--- PASS: TestListPermissionDenied (0.01s)
=== RUN   TestListReadDirContextCancelled
--- PASS: TestListReadDirContextCancelled (0.01s)
=== RUN   TestListPerEntryCtxCheck
--- PASS: TestListPerEntryCtxCheck (0.01s)
PASS
ok  	github.com/lang315/sshgate/internal/files	2.659s

$ go vet ./... ; echo "vet=$?"
vet=0

$ go test -race ./internal/files/ ; echo "full_race=$?"   # full package, not just -run TestList
ok  	github.com/lang315/sshgate/internal/files	2.527s
full_race=0

$ GOOS=windows go vet ./... ; echo "win_vet=$?"
win_vet=0
```
