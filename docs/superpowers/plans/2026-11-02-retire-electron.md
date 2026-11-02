# Retire the Electron app (ROADMAP row 7)

Date: 2026-11-02. Plan only, no spec: ROADMAP row 7 allows that ("needs a short spec or a plan only").

## Entry gate

Row 6's exit gate is met. The mygo app is installed in `/Applications/sshgate.app` (since 2026-10-28) and the author reported on 2026-11-02 that it has been the daily app since, with no return to Electron. Slice 7's hand checks passed on 2026-10-28.

## Decisions (author, 2026-11-02)

- `desktop/` is deleted whole. Windows and Linux get no desktop app: `sshgate hub --cli` is the approver there. The mygo spec's "Electron stays the desktop app [on Windows and Linux] until a later spec" ends here; packaging the mygo app for them would need its own spec. Rationale: the audience is the author (standing decision), who uses macOS.
- The `app/` module keeps building and testing on all three OSes in CI, as today.
- Comments in `app/` that name the TypeScript file a rule was ported from (`desktop/src/...`) stay as written: git history keeps those files. CLAUDE.md says so in one line.

## Test coverage

Every one of the 21 Playwright specs in `desktop/e2e/` was mapped to the Go tests that assert the same behaviour (2026-11-02, read from the code). Every approval, lock, soft-lock, auto-allow, sudo/root opt-in, host-key and audit behaviour has a Go flow test against a real hub and, where the spec used it, the MCP bridge and `audit.jsonl` (`app/view/flow*_unix_test.go`, `app/internal/hubtest`). The terminal flow tests need `MYGO_GHOSTTY_VT`, which CI sets on macOS only; every flow test runs on macOS and Linux CI.

Accepted gaps, none on a security rule:

1. `throughput.spec.ts`: frame gap, memory growth and key delivery during a flood. `app/termconn` `TestFloodThroughHub` checks only that every byte arrives with no stall.
2. `background.spec.ts`: the hub still serving, unlocked, after the window hides, and activate showing it again. Only the close-hides rule is tested; the rest was a hand check on 2026-10-28.
3. `clipboard.spec.ts`: the Linux Ctrl+Shift+C/V binding and mouse-drag selection. The mygo app is not shipped on Linux.
4. `live-files.spec.ts` "finder drop": hand-only already.

Dropped with Electron, nothing to port: the menu roles, renderer-crash recovery (`recoverRenderer`, `CrashPolicy`), the screenshot spec.

## Steps

1. Move what the mygo app's bundle still takes from `desktop/`:
   - `desktop/native/touchid/main.swift` → `app/native/touchid/main.swift`
   - `desktop/scripts/build-touchid.sh` → `app/scripts/build-touchid.sh` (same output, `<repo root>/sshgate-touchid`)
   - `desktop/build/icon.icns`, `icon.png`, `icon-source.png` → `app/icon/` (`app/build/` is gitignored)
   - `desktop/scripts/make-icon.sh` → `app/scripts/make-icon.sh`, working in `app/icon/`
   - `app/scripts/package-mac.sh` uses the new paths.
   Check: `app/scripts/package-mac.sh` builds the bundle; no path in it names `desktop/`.
2. Delete `desktop/` and the `desktop` job in `.github/workflows/ci.yml`.
   Check: `git grep -n 'desktop/'` outside `docs/superpowers/{specs,plans,reports}` finds only the port comments in `app/`.
3. Docs: README (macOS app only; Windows and Linux use `hub --cli`), CLAUDE.md (drop the `desktop/` section and commands), PRODUCT.md, `.impeccable/config.json` (its one rule names the deleted stylesheet), ROADMAP (rows 1, 6, 7 and the standing decision on the desktop UI), `checklists/slice1-manual.md` (retired; the Windows pipe DACL item moves to the ROADMAP findings, since the hub still runs on Windows).
4. Run `go vet ./...`, `go test -race ./...`, `(cd app && go test -race ./...)` with `MYGO_GHOSTTY_VT`, and the package script. Put the output in the PR.

## Exit gate

`desktop/` is gone, CI is green, and `app/scripts/package-mac.sh` builds the bundle with no file from `desktop/`.
