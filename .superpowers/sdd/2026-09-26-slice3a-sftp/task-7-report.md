# Task 7 report: Hub — files.list, files.mkdir, files.rename, strict params, FileRecord, protocol 4

## Implemented

- `internal/broker/audit.go`: added `FileRecord` and `(*Audit).WriteFile`, verbatim from the brief.
- `internal/hub/files.go` (new): `strictParams`, `fileErr`, `(*Hub).sftpFor`, `(*Hub).pinnedClient`,
  `first20`, `(*Hub).auditFile`, `listTimeout`, and `registerFileMethods` registering
  `files.list`, `files.mkdir`, `files.rename` on the UI door. Code matches the brief verbatim
  except for the deviations below.
- `internal/hub/uidoor.go`: wired `closeFiles := registerFileMethods(s, h)` next to
  `registerTermMethods`, with `defer closeFiles()`.
- `internal/hub/idle.go`: `ProtocolVersion = 4`.
- `desktop/src/shared/protocol.ts`: `PROTOCOL_VERSION = 4`.
- `desktop/test/fixtures/fakeHub.mjs`: `mode === 'badproto' ? 99 : 4`.
- `internal/hub/files_test.go` (new): the brief's test file verbatim (fixture `filesHub`,
  `waitNote`, `fileRecords`, and the five `TestFiles*` tests).
- `internal/hub/mcpdoor_test.go`: added `"files.list", "files.mkdir", "files.rename",
  "files.plan", "files.run", "files.cancelAll"` to `TestMCPDoorRejectsUIOnlyMethods`'s method list.

## TDD evidence

RED (before `internal/hub/files.go` existed):

```
$ go test ./internal/hub -run 'TestFiles|TestMCPDoorRejects' -v
=== RUN   TestFilesListMkdirRename
    files_test.go:109: method not found: files.list
--- FAIL: TestFilesListMkdirRename (0.08s)
=== RUN   TestFilesStrictParams
    ... method not found: files.list (x4)
--- FAIL: TestFilesStrictParams (0.01s)
=== RUN   TestFilesNeedUnlockAndPaths
    files_test.go:159: locked list: method not found: files.list
--- FAIL: TestFilesNeedUnlockAndPaths (0.01s)
=== RUN   TestFilesListTrustsAnUnpinnedHost
    files_test.go:168: method not found: files.list
--- FAIL: TestFilesListTrustsAnUnpinnedHost (0.01s)
=== RUN   TestFilesPermissionDenied
    files_test.go:196: err method not found: files.list
--- FAIL: TestFilesPermissionDenied (0.01s)
=== RUN   TestMCPDoorRejectsUIOnlyMethods
--- PASS: TestMCPDoorRejectsUIOnlyMethods (0.01s)
FAIL
```

GREEN (after implementation, plus the protocol-version fixup below):

```
$ go test ./internal/hub -race -run 'TestFiles|TestMCPDoor|TestUIDoor' -v
--- PASS: TestFilesListMkdirRename (0.45s)
--- PASS: TestFilesStrictParams (0.01s)
--- PASS: TestFilesNeedUnlockAndPaths (0.02s)
--- PASS: TestFilesListTrustsAnUnpinnedHost (0.03s)
--- PASS: TestFilesPermissionDenied (0.02s)
--- PASS: TestMCPDoorRejectsUIOnlyMethods (0.01s)
--- PASS: TestMCPDoorListAndExec (0.01s)
--- PASS: TestMCPDoorCancelWithdrawsPending (0.01s)
--- PASS: TestMCPDoorConnectionCloseWithdrawsPending (0.01s)
--- PASS: TestMCPDoorIgnoresNotificationExec (0.01s)
--- PASS: TestMCPDoorCancelBeforeExecRefusesExec (0.01s)
--- PASS: TestUIDoorStatusServersAndDecide (0.01s)
--- PASS: TestUIDoorDenyAll (0.01s)
--- PASS: TestUIDoorDecideRejectsInvalidOutcome (0.01s)
--- PASS: TestUIDoorHelloAndLockedNotification (1.08s)
--- PASS: TestUIDoorLockedNotificationIdleReason (1.00s)
--- PASS: TestUIDoorStatusDoesNotHoldOffIdleLock (1.33s)
PASS
ok  	github.com/lang315/sshgate/internal/hub	5.333s
```

Full verification command from the task, all green:

```
$ go vet ./... && GOOS=windows go vet ./... && \
  go test -race ./internal/hub/ ./internal/broker/ ./internal/files/ && \
  (cd desktop && npm test)
ok  	github.com/lang315/sshgate/internal/hub	46.184s
ok  	github.com/lang315/sshgate/internal/broker	(cached)
ok  	github.com/lang315/sshgate/internal/files	(cached)
...
 Test Files  15 passed (15)
      Tests  120 passed (120)
```

`go test -short ./...` also passes across the whole repo.

## Files touched

- `internal/broker/audit.go` (modified)
- `internal/hub/files.go` (new)
- `internal/hub/files_test.go` (new)
- `internal/hub/uidoor.go` (modified)
- `internal/hub/uidoor_test.go` (modified — see deviation 2)
- `internal/hub/servers_test.go` (modified — see deviation 1)
- `internal/hub/mcpdoor_test.go` (modified)
- `internal/hub/idle.go` (modified)
- `desktop/src/shared/protocol.ts` (modified)
- `desktop/test/fixtures/fakeHub.mjs` (modified)

## Deviations from the brief

1. **`waitNote` name collision.** `internal/hub/servers_test.go` already declared a helper
   `func waitNote(t *testing.T, notes chan note, method, id string)` (no return value), used by
   three existing tests (`servers_test.go:218,235`, `trust_test.go:152`), predating this task and
   unknown to the brief's author. The brief's `files_test.go` version has an identical signature
   plus a `map[string]any` return and a longer (10 s vs 5 s) deadline — a strict superset. I
   deleted the old `waitNote` from `servers_test.go` (kept `noNote` there untouched) and put the
   brief's `waitNote` in `files_test.go` verbatim under its original name. The three existing call
   sites use it as a statement, discarding the return value, and compile and pass unchanged. This
   keeps the exact name Task 8 is told to reuse.
2. **`internal/hub/uidoor_test.go:111`** hardcoded `hello.Protocol != 3`. Bumping
   `ProtocolVersion` to 4 broke `TestUIDoorHelloAndLockedNotification`; changed the literal to
   `!= ProtocolVersion`. Not in the brief's file list, but a direct, mechanical consequence of the
   protocol bump it does ask for.
3. **"add `files.*` to the comment above `ServeUIDoor`'s method list"**: that comment
   (`internal/hub/uidoor.go:67-69`) does not enumerate methods — nothing to add there. (The brief
   notes the real method-list comment lives in CLAUDE.md, updated by Task 12.)

## Concerns

- None outstanding. `registerFileMethods` returns `func() {}` as specified; Task 8 is expected to
  extend it to cancel/await jobs.
- `filesFixture.w` (raw notification writer) and `waitNote` are unused by this task's own tests —
  expected, since Task 8 reuses them for job progress notifications; `go vet` is clean regardless
  (unused top-level funcs/fields are not flagged).
