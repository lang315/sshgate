# mygo desktop, slice 3a (hosts) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The native mygo app manages hosts the way the Electron app does:
- a host editor (New host and Edit) beside the content: every connection field, write-only secrets with Clear, the host/port warning and "will be cleared", the count of tabs a save closes, the pin as `<algo> SHA256:…` with Forget host key, and the AI access fold with Visible to AI;
- Delete from the host card, behind a confirm;
- `Open host editor` in the host-key mismatch dialog, focused on Forget host key;
- Import from SSH config;
- the Dark / Light / Auto theme control, kept across launches.

Each piece carries the Electron renderer's rules and texts with Go tests, and the e2e specs `hosts`, `import` and the editor part of `keyboard` are ported as flow tests against a real hub.

**Architecture:**
- **Layers.** Unchanged from slices 1 and 2: `hubapi` (typed door calls), `model` (pure rules with their vitest cases as table tests), `view` (the `App` controller and its screens), `internal/hubtest` (sshgate and sshtestd for tests).
- **Sheets.** The host editor and the import sheet are one column at a time, built between the content column and the AI requests column of the ready screen (mygo.md §2 shape C). Their state (`editorState`, `importState`) lives on `App`; a reply touches only the state it was sent for, and only while its hub runs.
- **Rules.** `model/hostform.go` ports `hostForm.ts` (draft, `toInput`, hints, warnings) and `ImportSheet.tsx`'s `nextChecked`. The theme preference is a file the view reads and writes; `main` applies it through `mygo.Theme.SetSource`.

**Tech Stack:** Go 1.27.1 (`app/` module), mygo `ui` (fork commit `25387f1`), the existing `hubapi`, `model`, `view`, `hubtest` packages, and `sshgate hub` (UI door protocol 12, MCP door).

**Spec:** `docs/superpowers/specs/2026-10-14-mygo-desktop-design.md` (Slices row 3, "Approval column" → "Same rules elsewhere", "Door client" → Activity rule, "Testing"). Research notes the plan was written from, with file:line into the Electron sources and mygo: `/private/tmp/claude-501/-Users-lang-GolandProjects-github-com-lang315-ssh-mcp/1e1c2b02-5440-4e17-a378-848816a02d95/scratchpad/s3/{renderer,protocol,mygo}.md` (and `s1/`, `s2/` for earlier rules). A task names the section it ports. Every code block in this plan was compiled and its tests run in a scratch clone of the repo at `f680115`, in commit order: every commit with `MYGO_GHOSTTY_VT` (`go vet`, `gofmt -l`, `go test -short ./...`), and the final tree with and without it under `-race`, with the three new flow tests run three times.

## Global Constraints

- **Branch and scope.**
  - Work on branch `feat/mygo-slice3` (slices 0 to 2 are in it).
  - Edit only `app/`, and the plan's own docs.
  - `internal/`, `cmd/sshgate`, `desktop/`, the protocol and the mygo fork are unchanged. Never touch `/Users/lang/GolandProjects/github.com/lang315/mygo`; the fork checkout `/Users/lang/GolandProjects/github.com/lang315/mygo-sshgate` is read-only. Never touch `~/.config/sshgate`, the real `~/.ssh`, or `/Applications/sshgate.app`: tests use temp stores, temp SSH configs and `SSHGATE_RUNTIME_DIR`.
- **Wire.** UI door protocol 12, newline JSON-RPC. Hub errors arrive as `*door.Error`, whose `Error()` is the hub's message verbatim. Show it unchanged, through `model.DisplayText`.
- **Activity rule.** `App` calls nothing on a timer. A hub notification may call only `status`.
  - `servers.save`, `servers.delete`, `servers.forgetHostKey`, `import.scan` and `import.apply` run only after a click or key.
  - `servers` runs after a successful save, forget, import apply, after any delete reply, and (from slice 2) after a trusted open and on entering the ready screen. Opening the editor, typing in it, opening a fold and the theme control call nothing.
- **Replies.** Every reply of a new call checks `gen == a.hubGen` (a stopped hub's reply is dropped) and touches only the editor or import sheet it was sent for (`a.editor == ed`, `a.imp == imp`).
- **Guards.** `model.AllowDelay = 500 ms` also gates the delete confirm's `Delete host`. No new window-level `c.Shortcut`: the sheets' Esc is a `Shortcut` on the sheet element.
- **Verbatim strings.** Character for character:
  - Editor: `Host editor`, `New host`, `Edit ` + name, `Close host editor`, `Close`, `Save`, `Address`, `IP or hostname`, `Label`, `Defaults to the address`, `· changed`, `SSH on`, `Port`, `port`, `Credentials`, `Saved secrets are never shown. Leave a field empty to keep it.`, `User`, `Username`, `Password`, `su password`, `sudo password`, `Key passphrase`, `Clear`, `+ Key or agent`, `Auth`, `password`, `key`, `agent`, `Key path`, `~/.ssh/id_ed25519`, `Privilege escalation (su / sudo)`, ` · saved`, `AI access · On`, `AI access · Off`, ` · auto`, `Visible to AI`, `AI clients can see this server and ask to run commands. Commands wait for your approval unless auto-allow is on.`, `Needs a pinned host key.`, `Host key`, `Forget host key`, `Takes effect immediately.`, `Not pinned. You will be asked to confirm it on the next connect.`, `Changing host or port forgets the host key and saved passwords unless you re-enter them.`, `Saving will close N open tab(s)` (`model.CloseWarning`), `An IPv6 address cannot be a name: add a label.`, the hints `saved` and `will be cleared`, `Saved, but auto-allow was not turned on: ` + reason.
  - Delete: `Delete <name>? Its saved passwords and host key are removed and its open tabs close.`, `Delete host`, `Cancel`; card buttons `Edit` and `Delete` labelled `Edit <name>` and `Delete <name>`.
  - Mismatch: `Open host editor` (with slice 2's texts unchanged).
  - Import: `Import from SSH config`, `Close import`, `Host keys come from your known_hosts. With StrictHostKeyChecking accept-new, OpenSSH accepted them without asking you; compare them with the server if unsure.`, `Reading your SSH config…`, `Ready`, `Already in vault`, `Skipped`, `Import <alias>`, `not in known_hosts`, `Key has a passphrase: add it in the editor after import.`, `Import 1 host`, `Import N hosts`, `Not imported: alias (reason); …`.
  - Theme: the group `Theme`; segments named and tipped `Dark`, `Light`, `Auto`, showing `☾` (`"\u263e"`), `☀` (`"\u2600"`) and `Auto`.
- **Untrusted text.** Server names, hosts, users, fingerprints, aliases, key paths, reasons and hub errors go through `model.DisplayText` before they are shown.
- **Source text.** Never put a raw invisible or bidi rune in Go source; write escapes (`"\u202e"`). The blocks below do; keep it so when you copy them.
- **Tests.**
  - `go vet ./...`, `gofmt -l .` (empty) and `go test -race ./... -count=1` pass inside `app/`, both without and with `MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib`. Tests that need the terminal library call `needLib(t)` and skip without it.
  - Flow tests against a real hub are `//go:build unix` and check the hub's `audit.jsonl`. The import flow skips when `ssh` or `ssh-keygen` is not on `PATH`.
  - No fixed sleep over 500 ms in a test; poll with a 10 s deadline (`until`). Fake-door state is changed with `d.set(func() { ... })`.
  - The Tester clicks where an element is, scrolled into view or not: editor and import tests use a window of 800×900 (`edReady`) or 1000×900 (flows), so every field is on screen.
  - A task's tests must fail without its code (the RED step says how), and every commit compiles on its own.
  - Known flake, not this slice's: `TestStaleStatusReplyIgnored` fails about 1 run in 10 on `f680115` already (4 of 40 there). Rerun the package once when only it fails; do not change it here.
- **Commits.** Commit by explicit path. Every message ends with exactly `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never put a session URL in a commit. Do not push; the controller pushes.

## Review Focus

1. **A save, forget, delete or import reply that lands after its sheet closed, after a new sheet opened, or after the hub stopped.** It must not close, fill or show an error in another sheet, and a dead hub's reply changes nothing. Covered by Task 3 (`TestLateSaveReplyLeavesANewEditorAlone`).
2. **Secrets: untouched, cleared, and typed after Clear.** The exact JSON sent omits an untouched secret, sends `""` for a cleared one, and sends the value typed after Clear. Covered by Task 3 (`TestSaveSendsUntouchedClearedAndTypedSecrets`, `TestTypingAfterClearSendsTheNewValue`).
3. **Port text that is not 1 to 65535** (`70000`, letters, empty). It is never sent, by Save or by Enter, and the field says why. Covered by Task 3 (`TestBadPortIsNeverSent`).
4. **A lock or hub stop while the editor holds a typed secret.** The editor closes, the draft and its secret go, and unlocking does not bring them back. Covered by Task 3 (`TestLockDropsTheEditorAndItsTypedSecret`) and the hub-stop half of `TestLateSaveReplyLeavesANewEditorAlone`.
5. **An import row that is not ready.** It has no checkbox, clicking it checks nothing, and `import.apply` gets only checked ready aliases. Covered by Task 5 (`TestImportSendsCheckedReadyAliasesOnly`).

## Decisions taken in this plan (controller rulings, on the spec)

1. **Slice 3 is split; this is 3a.** One plan for slice 3 would be about 4,500 lines (renderer.md §7). 3a is hosts: editor, Delete, Import, `Open host editor`, theme; it ports `hosts`, `import` and `keyboard` (editor). 3b, next, is auto-allow: the editor's Auto-allow select and root/sudo opt-ins, `saveConfirm` with `AutoAllowDialog`, ticking chips, the card's Stop auto-allow, the paused and ran-while-locked banners, Settings, and the MCP bridge test client; it ports `autoallow`, `autoallow-mcp`, `settings`, `manuallock`, `softlock`. 3b plugs in at:
   - `buildAIAccess` (`view/editor.go`): the select and the two checkboxes go below the switch. `model.HostDraft` already carries `AutoAllow`, `AutoAllowRoot` and `AutoAllowSudo`, `DraftFrom` fills them and `ToInput` sends the opt-ins, with their vitest rows.
   - `saveHost` (`view/editor.go`): `keptMode` is replaced by `saveConfirm`, which opens the dialog when needed; the `savedBut` alert stays as it is.
   - `buildReady` (`view/home.go`): the Settings gear goes between `a.buildThemeControl(c)` and Lock.
   - `hubapi`: 3b adds `AutoAllowCheck` and `SetSoftLockLimit`; 3a adds neither.
   - Flow tests: `flowWith`, `configRecords`, `fill`, `editBox` and `saveEditor` are there for 3b's editor-driven arming.
2. **The host editor is a column beside the content, not a modal** (mygo.md §2 shape C; Electron's `.sheet-layer` lets clicks through, `styles.css:217`). The AI requests column stays usable while a host is edited. Esc is a `Shortcut` on the sheet, so it closes the editor only while the focus is inside it, and not while a save runs (`HostEditor.tsx:57`). Close, `×` and Esc discard unsaved edits without asking, as Electron's `onClose` does. mygo restores no focus: after a sheet closes the active terminal gets it, else the Hosts search field; the button that opened the sheet is not refocused. Cost: the content column narrows by 420 DIPs while a sheet shows; with the AI column open too, an 800-DIP window leaves the host list almost no width.
3. **Secrets are write-only.** No stored secret is ever in the draft or shown. Untouched is omitted (kept), Clear sends `""`, a typed value is sent; typing after Clear un-clears (`TextInput.Changed`). A host or port change marks every saved, unretyped secret `will be cleared` (the hub drops it). The hints are Electron's own lowercase placeholders, `saved` and `will be cleared`, shown as `Field.Description` (a placeholder is not a text node, mygo.md §1) and repeated as the placeholder, so the vitest counts ("one `saved`, one `Clear`") port as they are. `Clear` keeps its text and is labelled `Clear <field>`. The port is a `TextInput` with `model.PortError` (`Port must be 1-65535.`) under it; Save and Enter do nothing while it shows. Electron has no client check (it sends `Number(port)` and shows the hub's `port: must be 1-65535`); `ui.NumberInput` would drop bad text silently (mygo.md §1 "Port").
4. **Select labels.** 3a has no `ui.Select` (the theme control is a segmented control; the Auth radios' labels are their values). Binding for 3b: the app keeps its own value↔label table and never passes a hub value to `ui.Select` raw, since the select's text is its value (mygo.md §1 "Select").
5. **Forget host key lives in the editor, with no confirm**, as in Electron (`HostEditor.tsx:200`, "Takes effect immediately."). It is disabled while a save or forget runs; the editor stays open and shows `Not pinned…` once the host list is back. The mismatch dialog gains `Open host editor` (verbatim): it closes the dialog and opens the editor on that server with Forget host key focused and Address not (`App.tsx:343`). One change from Electron: `Close` comes before `Open host editor`. In mygo, a focus request that lands in a modal from behind it (a tab activated) goes to the modal's first button; with `Open host editor` first, slice 2's `TestDialogsGiveFocusBackToTheTerminal` failed in the scratch run. The safe button is first; the delete confirm follows the same rule with Cancel.
6. **Import.** Rows come from `import.scan`; ready rows are checked on the first scan, a rescan keeps the person's checks on rows still ready (`nextChecked`); only ready rows have a checkbox; `import.apply` gets checked ready alias names only, in scan order. No select-all (Electron has none; mygo's `CheckboxGroup` would also check disabled rows, mygo.md §5). The flow test writes its own SSH config whose last stanza, `Host *`, points `IdentityFile` and `UserKnownHostsFile` into the test's folder, so `ssh -G` never reads the real `~/.ssh`; sshtestd writes that `known_hosts` (`-write-known-hosts`); the test skips where `ssh` or `ssh-keygen` is missing. `hubtest.Start` (new) exposes sshtestd's port and the empty-store and known_hosts options; `hubtest.SSHD` stays as a wrapper for its seven callers.
7. **Theme.** A segmented control `Theme` in the top row between the AI button and Lock, as Electron's (`App.tsx:271`): `Dark` `☾`, `Light` `☀`, `Auto`, each named and tipped with its word. A click saves `{"theme":"…"}` to `theme.json` in `mygo.App.Path(mygo.PathUserData)` (temp file, then rename) and calls `Shell.SetTheme`, which `main` implements with `mygo.Theme.SetSource` (Auto = `ThemeSystem`). `main` reads the file and applies it before the window opens; no `SSHGATE_*` knob is read for it; tests inject `App.ThemeFile`. Any read failure means Auto and a failed write is ignored, as Electron's `loadPref`/`savePref`. Not ported: `resolveTheme` (mygo's `ThemeSystem` follows the OS) and `xtermTheme` with its vitest row (mygo's terminal follows `c.Theme().Dark` with its own palette; Electron's exact colours are cosmetic and deferred). Costs: `PathUserData` is `~/Library/Application Support/<App.Name()>`, the executable's name unpackaged (`sshgate-app` for a `go build ./cmd/sshgate-app`; not run here) and the bundle's name once packaged, so a choice made unpackaged does not carry over (slice 7's packaging decides the name, e.g. `App.SetName`); on Linux a forced Light still reads dark (mygo.md §4), accepted since Linux is not shipped.
8. **Activity rule unchanged.** `servers` is called only after a user action (save, forget, delete, import apply, a trusted open) or on entering the ready screen, never on a timer. Edit opens from the host list the app holds, with no reload first (Electron reloads, `App.tsx:282`). Cost: an edit of the vault file made outside the app since the last reload is not in the draft; the save then answers from the hub's copy. Every new reply is guarded by the hub generation (slice-1 carry) and by its own sheet.
9. **Open decisions from the research, decided:**
   - a. **Lock and hub stop close the editor, the import sheet and the delete confirm** (`dropDialogs`), so a typed secret never outlives the ready screen. Electron unmounts the editor on lock but reopens it with a fresh draft after unlock (`App.tsx:310`). Cost: the person reopens it by hand.
   - b. **Delete confirm**: a modal, `Cancel` autofocused, so Enter and Esc keep the host; `Delete host` is an ordinary button, disabled for `AllowDelay` after the confirm shows, so a double click on the card's Delete cannot confirm it. It is not mouse-only: deleting grants nothing, and a keyboard user can Tab to it. Electron's `window.confirm` had OK focused (renderer.md §5).
   - c. **A save keeps a live forever grant and ends any other** (`keptMode`): the hub ends every grant on save, and Electron re-arms a live forever grant without asking when nothing else changed (`saveConfirm`, `autoallow.ts:108`). A paused or timed grant is sent as `off`. Cost until 3b: editing a host whose forever grant is paused ends it, where Electron would ask through the dialog. Since a forever save can still fail to arm (e.g. Visible to AI turned off), `saved, but auto-allow was not turned on: ` is shown as Electron's alert, in the alert row.
   - d. **Card actions** `Edit` and `Delete` are always-visible text buttons; Electron's hover-revealed icons are not ported (the Tester cannot hover, and slice 1's cards are text).
   - e. **Auth radios** are a keyed `ui.RadioGroup`: one Tab stop, arrows that choose and wrap (mygo's focus group). Electron's `arrowStep` is not ported; its vitest rows become `TestArrowsMoveTheAuthChoiceAndKeepTheFocus`. The group and its radios are keyed because the Password field above them comes and goes with the choice; unkeyed, the radio lost the focus the arrow gave it (found in the scratch run).
   - f. **Close while saving**: `Close` and `×` still close (as Electron), Esc does not; the late reply then touches only its own, closed, editor.
   - g. **A server that vanished while edited** keeps the title `Edit <name>` and the save names it as `original`, so the hub answers `server "<name>" not found`; Electron would save it as a new host.

---

## File structure

```
app/hubapi/types.go               Server + hasPassword/hasSuPassword/hasSudoPassword/hasKeyPassphrase/autoAllowRoot/autoAllowSudo;
                                  ServerInput, Candidate, ImportScan, ImportSkip, ImportResult
app/hubapi/client.go              + SaveServer, DeleteServer, ImportScan, ImportApply
app/model/hostform.go             Secret, SecretEdit, HostDraft, DraftFrom, ToInput, PortError, LabelHint, SecretHint,
                                  EndpointChanged, ClosesTabs, CloseWarning, EditorWarnings, NextChecked          [new]
app/view/editor.go                editorState, openEditor, closeSheet, dropDialogs, keptMode, saveHost, buildEditor,
                                  buildEditorBody, buildAIAccess, forgetKey, deleteState, buildDeleteConfirm, deleteHost [new]
app/view/importsheet.go           importState, openImport, scanImport, applyImport, buildImport, buildImportRow   [new]
app/view/theme.go                 themes, parseTheme, LoadTheme, saveTheme, setTheme, buildThemeControl          [new]
app/view/app.go                   editor, imp, del, sheetSeq, searchH, theme, ThemeFile; dropDialogs on leaving ready
app/view/home.go                  New host and Import buttons, card Edit and Delete, the sheet column, theme control, delete confirm
app/view/hostkey.go               Open host editor in the mismatch dialog
app/view/shell.go                 Shell.SetTheme
app/cmd/sshgate-app/{main,tray}.go theme file, themeSource, SetSource before the window
app/internal/hubtest/hubtest.go   Options, Server, Start (port, empty store, known_hosts); SSHD wraps Start
app/view/*_test.go                editor_test, hostwrites_test, importsheet_test, theme_test [new]; flowhosts_unix_test [new]
```

---

### Task 1: `hubapi`: host writes and import

Ports the door calls of `desktop/src/renderer/transport.ts:61-65` and the types of `shared/protocol.ts:17-37, 64-68` (protocol.md §1.1, §1.2, §1.6, §1.7).

**Files:**
- Modify: `app/hubapi/types.go` (the `Server` struct; append), `app/hubapi/client.go` (append)
- Test: `app/hubapi/client_test.go`, `app/hubapi/hub_test.go`

**Interfaces:**
- Consumes: `hubapi.Client`, `Caller`, `Servers` (slice 1); `hubtest.SSHD`, `hubtest.Binaries`, `hubtest.Env` (slice 0).
- Produces (package `hubapi`):
  - `Server` gains `HasPassword, HasSuPassword, HasSudoPassword, HasKeyPassphrase, AutoAllowRoot, AutoAllowSudo bool` (JSON `hasPassword`, … `autoAllowSudo`).
  - `type ServerInput struct { Name, Host string; Port int; User, Auth, KeyPath string; AIVisible, AutoAllowRoot, AutoAllowSudo bool; Password, SuPassword, SudoPassword, KeyPassphrase *string }` (secrets `omitempty`).
  - `type Candidate struct { Alias, Host string; Port int; User, Auth, KeyPath string; NeedsPassphrase bool; HostKey, HostKeyAlgo, Status, Reason string }`.
  - `type ImportScan struct { Candidates []Candidate; Note string }`, `type ImportSkip struct { Alias, Reason string }`, `type ImportResult struct { Imported []string; Skipped []ImportSkip }`.
  - `func (h *Client) SaveServer(ctx context.Context, original string, in ServerInput, autoAllow string) error` (`original` omitted when "").
  - `func (h *Client) DeleteServer(ctx context.Context, name string) error`.
  - `func (h *Client) ImportScan(ctx context.Context) (ImportScan, error)` (`Candidates` never nil).
  - `func (h *Client) ImportApply(ctx context.Context, aliases []string) (ImportResult, error)`.

- [ ] **Step 1: Write the failing tests**

In `app/hubapi/client_test.go`, in `TestCallShapes`, add after `ctx := context.Background()`:

```go
	pw1, empty := "pw1", ""
```

and after the `"forget key"` row:

```go
		{"save new", "servers.save", `{"server":{"name":"web","host":"10.0.0.5","port":22,"user":"u","auth":"password","keyPath":"","aiVisible":false,"autoAllowRoot":false,"autoAllowSudo":false,"password":"pw1"},"autoAllow":"off"}`,
			func(c *Client) error {
				return c.SaveServer(ctx, "", ServerInput{Name: "web", Host: "10.0.0.5", Port: 22, User: "u", Auth: "password", Password: &pw1}, "off")
			}},
		{"save edit", "servers.save", `{"original":"box","server":{"name":"box","host":"h","port":2222,"user":"u","auth":"key","keyPath":"~/.ssh/id","aiVisible":true,"autoAllowRoot":true,"autoAllowSudo":false,"keyPassphrase":""},"autoAllow":"forever"}`,
			func(c *Client) error {
				return c.SaveServer(ctx, "box", ServerInput{Name: "box", Host: "h", Port: 2222, User: "u", Auth: "key", KeyPath: "~/.ssh/id", AIVisible: true, AutoAllowRoot: true, KeyPassphrase: &empty}, "forever")
			}},
		{"delete", "servers.delete", `{"name":"box"}`, func(c *Client) error { return c.DeleteServer(ctx, "box") }},
		{"import scan", "import.scan", `null`, func(c *Client) error { _, err := c.ImportScan(ctx); return err }},
		{"import apply", "import.apply", `{"aliases":["work","db"]}`, func(c *Client) error { _, err := c.ImportApply(ctx, []string{"work", "db"}); return err }},
```

Append to `app/hubapi/client_test.go`:

```go

func TestServerDecodesSecretFlagsAndOptIns(t *testing.T) {
	r := &rec{result: `[{"name":"box","hasPassword":true,"hasSuPassword":false,"hasSudoPassword":true,"hasKeyPassphrase":true,"autoAllowRoot":true,"autoAllowSudo":true}]`}
	s, err := New(r).Servers(context.Background())
	if err != nil || len(s) != 1 {
		t.Fatal(s, err)
	}
	if b := s[0]; !b.HasPassword || b.HasSuPassword || !b.HasSudoPassword || !b.HasKeyPassphrase || !b.AutoAllowRoot || !b.AutoAllowSudo {
		t.Fatalf("%+v", b)
	}
}

func TestImportRepliesDecode(t *testing.T) {
	s, err := New(&rec{result: `{"candidates":null,"note":"No ~/.ssh/config"}`}).ImportScan(context.Background())
	if err != nil || s.Candidates == nil || len(s.Candidates) != 0 || s.Note != "No ~/.ssh/config" {
		t.Fatalf("%+v %v", s, err)
	}
	s, err = New(&rec{result: `{"candidates":[{"alias":"web","host":"10.0.0.5","port":2200,"user":"deploy","auth":"key","keyPath":"~/.ssh/id_web","needsPassphrase":true,"hostKey":"SHA256:abc","hostKeyAlgo":"ssh-ed25519","status":"ready"}]}`}).ImportScan(context.Background())
	if c := s.Candidates; err != nil || len(c) != 1 || c[0].Port != 2200 || !c[0].NeedsPassphrase || c[0].HostKeyAlgo != "ssh-ed25519" || c[0].Status != "ready" {
		t.Fatalf("%+v %v", s, err)
	}
	res, err := New(&rec{result: `{"imported":["web"],"skipped":[{"alias":"jump","reason":"needs ProxyJump"}]}`}).ImportApply(context.Background(), []string{"web", "jump"})
	if err != nil || len(res.Imported) != 1 || res.Skipped[0] != (ImportSkip{"jump", "needs ProxyJump"}) {
		t.Fatalf("%+v %v", res, err)
	}
}
```

In `app/hubapi/hub_test.go`, add to the imports:

```go
	"os"
	"os/exec"
	"path/filepath"
```

and append:

```go

// TestHostWritesAgainstRealHub: servers.save, servers.delete and import
// answer with the hub's texts; secrets come back as flags only.
func TestHostWritesAgainstRealHub(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("import needs the OpenSSH client")
	}
	store := hubtest.SSHD(t)
	bin, _ := hubtest.Binaries(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "ssh_config")
	// Host * keeps ssh -G away from the real ~/.ssh.
	conf := "Host jump\n  HostName 127.0.0.1\n  ProxyJump other\nHost *\n  IdentityFile " + filepath.Join(dir, "missing") + "\n  UserKnownHostsFile " + filepath.Join(dir, "known_hosts") + "\n"
	if err := os.WriteFile(cfg, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := hubproc.Start(context.Background(), hubproc.Launch{Command: bin, Args: []string{"hub", "--store=" + store, "--sshConfig=" + cfg}, Env: hubtest.Env(t)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	ctx := context.Background()
	h := New(p.Client())
	if _, err := h.Unlock(ctx, hubtest.Password); err != nil {
		t.Fatal(err)
	}
	pw := "secret"
	in := ServerInput{Name: "web", Host: "10.0.0.5", Port: 22, User: "u", Auth: "password", Password: &pw}
	if err := h.SaveServer(ctx, "", in, "off"); err != nil {
		t.Fatal(err)
	}
	servers, err := h.Servers(ctx)
	if err != nil || len(servers) != 2 || servers[1].Name != "web" || !servers[1].HasPassword || servers[1].HasSuPassword || servers[1].AIVisible {
		t.Fatalf("servers %+v %v", servers, err)
	}
	in.Name = "box"
	if err := h.SaveServer(ctx, "", in, "off"); err == nil || err.Error() != `a server named "box" already exists` {
		t.Fatalf("duplicate: %v", err)
	}
	in.Name, in.Port = "web2", 0
	if err := h.SaveServer(ctx, "", in, "off"); err == nil || err.Error() != "port: must be 1-65535" {
		t.Fatalf("port 0: %v", err)
	}
	if err := h.DeleteServer(ctx, "web"); err != nil {
		t.Fatal(err)
	}
	if err := h.DeleteServer(ctx, "web"); err == nil || err.Error() != `server "web" not found` {
		t.Fatalf("delete twice: %v", err)
	}
	scan, err := h.ImportScan(ctx)
	if err != nil || len(scan.Candidates) != 1 || scan.Candidates[0].Alias != "jump" || scan.Candidates[0].Status != "skipped" || scan.Candidates[0].Reason != "needs ProxyJump" {
		t.Fatalf("scan %+v %v", scan, err)
	}
	res, err := h.ImportApply(ctx, []string{"jump", "nope"})
	if err != nil || len(res.Imported) != 0 || len(res.Skipped) != 2 ||
		res.Skipped[0] != (ImportSkip{"nope", "not in the SSH config"}) || res.Skipped[1] != (ImportSkip{"jump", "needs ProxyJump"}) {
		t.Fatalf("apply %+v %v", res, err)
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `cd app && go test ./hubapi -count=1`
Expected: FAIL to compile: `c.SaveServer undefined (type *Client has no field or method SaveServer)`.

- [ ] **Step 3: Implement**

In `app/hubapi/types.go`, add to `Server` after `AutoAllowRefused`:

```go
	HasPassword      bool       `json:"hasPassword"`
	HasSuPassword    bool       `json:"hasSuPassword"`
	HasSudoPassword  bool       `json:"hasSudoPassword"`
	HasKeyPassphrase bool       `json:"hasKeyPassphrase"`
	AutoAllowRoot    bool       `json:"autoAllowRoot"`
	AutoAllowSudo    bool       `json:"autoAllowSudo"`
```

Append to `app/hubapi/types.go`:

```go

// ServerInput is servers.save's server. The four secrets are write-only:
// nil is omitted and keeps the stored value, "" clears it, anything else
// replaces it. The hub never sends one back.
type ServerInput struct {
	Name          string  `json:"name"`
	Host          string  `json:"host"`
	Port          int     `json:"port"`
	User          string  `json:"user"`
	Auth          string  `json:"auth"`
	KeyPath       string  `json:"keyPath"`
	AIVisible     bool    `json:"aiVisible"`
	AutoAllowRoot bool    `json:"autoAllowRoot"`
	AutoAllowSudo bool    `json:"autoAllowSudo"`
	Password      *string `json:"password,omitempty"`
	SuPassword    *string `json:"suPassword,omitempty"`
	SudoPassword  *string `json:"sudoPassword,omitempty"`
	KeyPassphrase *string `json:"keyPassphrase,omitempty"`
}

// Candidate is one alias of import.scan. Status is "ready", "exists" (a
// server has this name) or "skipped" (Reason says why).
type Candidate struct {
	Alias           string `json:"alias"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	User            string `json:"user"`
	Auth            string `json:"auth"`
	KeyPath         string `json:"keyPath,omitempty"`
	NeedsPassphrase bool   `json:"needsPassphrase,omitempty"`
	HostKey         string `json:"hostKey,omitempty"`
	HostKeyAlgo     string `json:"hostKeyAlgo,omitempty"`
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
}

type ImportScan struct {
	Candidates []Candidate `json:"candidates"`
	Note       string      `json:"note,omitempty"` // "No ~/.ssh/config"
}

type ImportSkip struct {
	Alias  string `json:"alias"`
	Reason string `json:"reason"`
}

type ImportResult struct {
	Imported []string     `json:"imported"`
	Skipped  []ImportSkip `json:"skipped"`
}
```

Append to `app/hubapi/client.go`:

```go

// SaveServer creates a server (original "") or updates the one named
// original. autoAllow is the grant to arm after the write ("off" arms none);
// the hub ends the server's old grant on every save.
func (h *Client) SaveServer(ctx context.Context, original string, in ServerInput, autoAllow string) error {
	p := struct {
		Original  string      `json:"original,omitempty"`
		Server    ServerInput `json:"server"`
		AutoAllow string      `json:"autoAllow"`
	}{original, in, autoAllow}
	return h.c.Call(ctx, "servers.save", p, nil)
}

func (h *Client) DeleteServer(ctx context.Context, name string) error {
	return h.c.Call(ctx, "servers.delete", map[string]string{"name": name}, nil)
}

// ImportScan lists the aliases of the hub's SSH config; it never dials.
func (h *Client) ImportScan(ctx context.Context) (ImportScan, error) {
	var s ImportScan
	if err := h.c.Call(ctx, "import.scan", nil, &s); err != nil {
		return ImportScan{}, err
	}
	if s.Candidates == nil {
		s.Candidates = []Candidate{}
	}
	return s, nil
}

// ImportApply sends alias names only: the hub recomputes every field.
func (h *Client) ImportApply(ctx context.Context, aliases []string) (ImportResult, error) {
	var r ImportResult
	if err := h.c.Call(ctx, "import.apply", map[string][]string{"aliases": aliases}, &r); err != nil {
		return ImportResult{}, err
	}
	return r, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `cd app && go test ./hubapi -count=1 && go vet ./hubapi && gofmt -l hubapi`
Expected: `ok  github.com/lang315/sshgate/app/hubapi`, no gofmt output. `TestHostWritesAgainstRealHub` runs `ssh -G` on its own config (the hub's `--sshConfig`); it skips without `ssh` and with `-short`.

- [ ] **Step 5: Commit**

```bash
git add app/hubapi/types.go app/hubapi/client.go app/hubapi/client_test.go app/hubapi/hub_test.go
git commit -m "app: hubapi servers.save, servers.delete and SSH config import

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: `model`: host form rules and the import checks

Ports `desktop/src/renderer/hostForm.ts` (except `arrowStep`, Decision 9e, and the slice-1 `filterHosts`/`passwordChecks`), `HostEditor.tsx`'s `EditorWarnings` and `ImportSheet.tsx`'s `nextChecked`, with their vitest cases (renderer.md §1.1, §1.4, §3). The view cases of `hostForm.test.ts` (rendered editor) are Task 3's; the opt-in checkbox case (`:88-97`) is 3b's.

**Files:**
- Create: `app/model/hostform.go`
- Test: `app/model/hostform_test.go` (create)

**Interfaces:**
- Consumes: `hubapi.Server`, `hubapi.ServerInput`, `hubapi.Candidate` (Task 1).
- Produces (package `model`):
  - `type Secret int` with `SecretPassword, SecretSu, SecretSudo, SecretKeyPassphrase`; `func (f Secret) Label() string`; `func (f Secret) Saved(s *hubapi.Server) bool` (nil = new host).
  - `type SecretEdit struct { Value string; Cleared bool }`.
  - `type HostDraft struct { Name, Host, Port, User, Auth, KeyPath string; AIVisible bool; AutoAllow string; AutoAllowRoot, AutoAllowSudo bool; Secrets [4]SecretEdit }`.
  - `func DraftFrom(s *hubapi.Server) HostDraft`, `func ToInput(d HostDraft) hubapi.ServerInput`, `func PortError(p string) string`, `func LabelHint(d HostDraft) string`, `func SecretHint(saved bool, e SecretEdit, endpointChanged bool) string`, `func EndpointChanged(s *hubapi.Server, d HostDraft) bool`, `func ClosesTabs(s *hubapi.Server, d HostDraft) bool`, `func CloseWarning(openTabs, transfers int) string`, `func EditorWarnings(s *hubapi.Server, d HostDraft, openTabs, transfers int) []string`, `func NextChecked(cs []hubapi.Candidate, prev map[string]bool) map[string]bool`.

- [ ] **Step 1: Write the failing tests**

`app/model/hostform_test.go`:

```go
package model

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lang315/sshgate/app/hubapi"
)

// box is hostForm.test.ts's fixture.
var box = hubapi.Server{Name: "box", Host: "h", Port: 22, User: "u", Auth: "password", HostKey: "SHA256:x", HostKeyAlgo: "ssh-ed25519", HasPassword: true}

func TestToInputSendsOnlyTouchedSecrets(t *testing.T) {
	d := DraftFrom(&box)
	d.Secrets[SecretPassword] = SecretEdit{Cleared: true}
	d.Secrets[SecretSu] = SecretEdit{Value: "su!"}
	b, _ := json.Marshal(ToInput(d))
	want := `{"name":"box","host":"h","port":22,"user":"u","auth":"password","keyPath":"","aiVisible":false,"autoAllowRoot":false,"autoAllowSudo":false,"password":"","suPassword":"su!"}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

func TestSecretHint(t *testing.T) {
	for _, tc := range []struct {
		saved, moved bool
		e            SecretEdit
		want         string
	}{
		{true, false, SecretEdit{}, "saved"},
		{false, false, SecretEdit{}, ""},
		{true, false, SecretEdit{Cleared: true}, "will be cleared"},
		{true, true, SecretEdit{}, "will be cleared"},
		{false, true, SecretEdit{}, ""},
		{true, true, SecretEdit{Value: "new"}, "saved"},
	} {
		if got := SecretHint(tc.saved, tc.e, tc.moved); got != tc.want {
			t.Errorf("SecretHint(%v, %+v, %v) = %q, want %q", tc.saved, tc.e, tc.moved, got, tc.want)
		}
	}
}

func TestSecretsSavedAndLabels(t *testing.T) {
	s := hubapi.Server{HasSudoPassword: true, HasKeyPassphrase: true}
	var got []string
	for f := SecretPassword; f <= SecretKeyPassphrase; f++ {
		if f.Saved(&s) {
			got = append(got, f.Label())
		}
		if f.Saved(nil) {
			t.Fatal("a new host has no saved secret")
		}
	}
	if !reflect.DeepEqual(got, []string{"sudo password", "Key passphrase"}) {
		t.Fatal(got)
	}
}

func TestDraftFromAutoAllowAndOptIns(t *testing.T) {
	with := func(a *hubapi.AutoAllow) *hubapi.Server { s := box; s.AutoAllow = a; return &s }
	for _, tc := range []struct {
		s    *hubapi.Server
		want string
	}{
		{with(&hubapi.AutoAllow{Forever: true}), "forever"},
		{with(&hubapi.AutoAllow{Forever: true, Paused: true}), "forever"},
		{with(&hubapi.AutoAllow{Until: "2026-01-01T00:00:00Z"}), "off"},
		{&box, "off"},
		{nil, "off"},
	} {
		if got := DraftFrom(tc.s).AutoAllow; got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.s, got, tc.want)
		}
	}
	s := box
	s.AutoAllowRoot, s.AutoAllowSudo = true, true
	if d := DraftFrom(&s); !d.AutoAllowRoot || !d.AutoAllowSudo {
		t.Fatalf("%+v", d)
	}
	if d := DraftFrom(nil); d.AutoAllowRoot || d.AutoAllowSudo || d.Port != "22" || d.Auth != "password" || d.AIVisible {
		t.Fatalf("new host %+v", d)
	}
	d := DraftFrom(&box)
	d.AutoAllowRoot, d.AutoAllowSudo = true, true
	if in := ToInput(d); !in.AutoAllowRoot || !in.AutoAllowSudo {
		t.Fatalf("%+v", in)
	}
}

func TestLabelDefaultsToTheAddress(t *testing.T) {
	d := DraftFrom(nil)
	d.Host, d.User = " 10.0.4.21 ", "u"
	if n := ToInput(d).Name; n != "10.0.4.21" {
		t.Fatal(n)
	}
	d.Name = " web "
	if n := ToInput(d).Name; n != "web" {
		t.Fatal(n)
	}
	for _, tc := range []struct{ name, host, want string }{
		{"", "db.example.com", ""},
		{"", "2001:db8::1", "An IPv6 address cannot be a name: add a label."},
		{"v6box", "2001:db8::1", ""},
	} {
		d := DraftFrom(nil)
		d.Name, d.Host = tc.name, tc.host
		if got := LabelHint(d); got != tc.want {
			t.Errorf("%q %q: %q", tc.name, tc.host, got)
		}
	}
}

func TestKeyPathOnlyForKeyAuthAndPortText(t *testing.T) {
	d := DraftFrom(&box)
	d.KeyPath = " ~/.ssh/id "
	if in := ToInput(d); in.KeyPath != "" {
		t.Fatal("key path sent for password auth:", in.KeyPath)
	}
	d.Auth = "key"
	if in := ToInput(d); in.KeyPath != "~/.ssh/id" {
		t.Fatal(in.KeyPath)
	}
	for p, want := range map[string]string{"22": "", " 2222 ": "", "65535": "", "0": "Port must be 1-65535.", "65536": "Port must be 1-65535.", "": "Port must be 1-65535.", "22a": "Port must be 1-65535."} {
		if got := PortError(p); got != want {
			t.Errorf("PortError(%q) = %q", p, got)
		}
	}
}

func TestEndpointChangedAndWarnings(t *testing.T) {
	d := DraftFrom(&box)
	user, port, host := d, d, d
	user.User, port.Port, host.Host = "root", "2222", "h2"
	if EndpointChanged(&box, d) || EndpointChanged(&box, user) || !EndpointChanged(&box, port) || !EndpointChanged(&box, host) || EndpointChanged(nil, d) {
		t.Fatal("endpointChanged rows")
	}
	if got := EditorWarnings(&box, port, 2, 0); !reflect.DeepEqual(got, []string{"Changing host or port forgets the host key and saved passwords unless you re-enter them.", "Saving will close 2 open tabs."}) {
		t.Fatalf("%q", got)
	}
	if got := EditorWarnings(&box, port, 1, 0); len(got) != 2 || got[1] != "Saving will close 1 open tab." {
		t.Fatalf("%q", got)
	}
}

func TestClosesTabsOnlyForConnectionChanges(t *testing.T) {
	d := DraftFrom(&box)
	vis, user, sudo := d, d, d
	vis.AIVisible, user.User = true, "root"
	sudo.Secrets[SecretSudo] = SecretEdit{Value: "x"}
	if ClosesTabs(&box, vis) || !ClosesTabs(&box, user) || !ClosesTabs(&box, sudo) || ClosesTabs(nil, user) {
		t.Fatal("closesTabs rows")
	}
	if got := EditorWarnings(&box, vis, 2, 0); len(got) != 0 {
		t.Fatalf("Visible to AI alone warns: %q", got)
	}
}

func TestCloseWarning(t *testing.T) {
	for _, tc := range []struct {
		tabs, jobs int
		want       string
	}{
		{2, 0, "Saving will close 2 open tabs."},
		{1, 1, "Saving will close 1 open tab and cancel 1 transfer."},
		{0, 3, "Saving will cancel 3 transfers."},
	} {
		if got := CloseWarning(tc.tabs, tc.jobs); got != tc.want {
			t.Errorf("%d %d: %q", tc.tabs, tc.jobs, got)
		}
	}
}

func TestNextChecked(t *testing.T) {
	cs := []hubapi.Candidate{{Alias: "web", Status: "ready"}, {Alias: "db", Status: "ready"}, {Alias: "jump", Status: "skipped"}}
	if got := NextChecked(cs, nil); !reflect.DeepEqual(got, map[string]bool{"web": true, "db": true}) {
		t.Fatal(got)
	}
	if got := NextChecked(cs, map[string]bool{"db": true, "jump": true, "gone": true}); !reflect.DeepEqual(got, map[string]bool{"db": true}) {
		t.Fatal(got)
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `cd app && go test ./model -count=1`
Expected: FAIL to compile: `undefined: DraftFrom` (and the other new names).

- [ ] **Step 3: Implement**

`app/model/hostform.go`:

```go
package model

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lang315/sshgate/app/hubapi"
)

// Secret is one of a server's four write-only secrets, in the editor's
// order (desktop/src/renderer/hostForm.ts SECRET_FIELDS).
type Secret int

const (
	SecretPassword Secret = iota
	SecretSu
	SecretSudo
	SecretKeyPassphrase
)

func (f Secret) Label() string {
	return [...]string{"Password", "su password", "sudo password", "Key passphrase"}[f]
}

// Saved reports whether s stores this secret; s is nil for a new host.
func (f Secret) Saved(s *hubapi.Server) bool {
	return s != nil && [...]bool{s.HasPassword, s.HasSuPassword, s.HasSudoPassword, s.HasKeyPassphrase}[f]
}

// SecretEdit is what the person did to one secret: typed Value, or Cleared.
type SecretEdit struct {
	Value   string
	Cleared bool
}

// HostDraft is the host editor's form. Port stays text until it is saved;
// Secrets is indexed by Secret and never holds a stored value.
type HostDraft struct {
	Name, Host, Port, User, Auth, KeyPath string
	AIVisible                             bool
	AutoAllow                             string // "forever" for a forever host, paused too; else "off"
	AutoAllowRoot, AutoAllowSudo          bool
	Secrets                               [4]SecretEdit
}

// DraftFrom is the form for s, or for a new host when s is nil.
func DraftFrom(s *hubapi.Server) HostDraft {
	if s == nil {
		return HostDraft{Port: "22", Auth: "password", AutoAllow: "off"}
	}
	mode := "off"
	if s.AutoAllow != nil && s.AutoAllow.Forever {
		mode = "forever"
	}
	return HostDraft{Name: s.Name, Host: s.Host, Port: strconv.Itoa(s.Port), User: s.User, Auth: s.Auth, KeyPath: s.KeyPath,
		AIVisible: s.AIVisible, AutoAllow: mode, AutoAllowRoot: s.AutoAllowRoot, AutoAllowSudo: s.AutoAllowSudo}
}

// draftPort is Electron's Number(port): 0 for text that is not a number,
// which the hub refuses.
func draftPort(p string) int {
	n, err := strconv.Atoi(strings.TrimSpace(p))
	if err != nil {
		return 0
	}
	return n
}

// PortError is the port field's message, "" when the port can be saved.
func PortError(p string) string {
	if n := draftPort(p); n < 1 || n > 65535 {
		return "Port must be 1-65535."
	}
	return ""
}

// ToInput is servers.save's server: the label defaults to the address, the
// key path is sent only for key auth, and a secret is omitted (kept) when
// untouched, "" when cleared, and its value when typed.
func ToInput(d HostDraft) hubapi.ServerInput {
	in := hubapi.ServerInput{Name: strings.TrimSpace(d.Name), Host: strings.TrimSpace(d.Host), Port: draftPort(d.Port), User: strings.TrimSpace(d.User),
		Auth: d.Auth, AIVisible: d.AIVisible, AutoAllowRoot: d.AutoAllowRoot, AutoAllowSudo: d.AutoAllowSudo}
	if in.Name == "" {
		in.Name = in.Host
	}
	if d.Auth == "key" {
		in.KeyPath = strings.TrimSpace(d.KeyPath)
	}
	out := [...]**string{&in.Password, &in.SuPassword, &in.SudoPassword, &in.KeyPassphrase}
	for i, e := range d.Secrets {
		switch {
		case e.Cleared:
			v := ""
			*out[i] = &v
		case e.Value != "":
			v := e.Value
			*out[i] = &v
		}
	}
	return in
}

// LabelHint asks for a label when the address cannot be a name: the hub's
// name rule (A-Z a-z 0-9 . _ -) rejects an IPv6 address.
func LabelHint(d HostDraft) string {
	if strings.TrimSpace(d.Name) == "" && strings.Contains(d.Host, ":") {
		return "An IPv6 address cannot be a name: add a label."
	}
	return ""
}

// SecretHint says what a save does to a secret: "will be cleared" for a
// cleared one, or a saved one the hub drops because the endpoint changed;
// "saved" for a kept one; "" when nothing is stored.
func SecretHint(saved bool, e SecretEdit, endpointChanged bool) string {
	switch {
	case e.Cleared:
		return "will be cleared"
	case !saved:
		return ""
	case endpointChanged && e.Value == "":
		return "will be cleared"
	}
	return "saved"
}

// EndpointChanged: a new host or port drops the pin and every secret not
// typed again.
func EndpointChanged(s *hubapi.Server, d HostDraft) bool {
	return s != nil && (strings.TrimSpace(d.Host) != s.Host || draftPort(d.Port) != s.Port)
}

// ClosesTabs mirrors the hub: a save closes the server's connection when
// anything but Visible to AI and the auto-allow fields changed.
func ClosesTabs(s *hubapi.Server, d HostDraft) bool {
	if s == nil {
		return false
	}
	in := ToInput(d)
	return in.Name != s.Name || in.Host != s.Host || in.Port != s.Port || in.User != s.User || in.Auth != s.Auth || in.KeyPath != s.KeyPath ||
		in.Password != nil || in.SuPassword != nil || in.SudoPassword != nil || in.KeyPassphrase != nil
}

func CloseWarning(openTabs, transfers int) string {
	plural := func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	}
	tabs := fmt.Sprintf("close %d open %s", openTabs, plural(openTabs, "tab", "tabs"))
	jobs := fmt.Sprintf("cancel %d %s", transfers, plural(transfers, "transfer", "transfers"))
	switch {
	case openTabs > 0 && transfers > 0:
		return "Saving will " + tabs + " and " + jobs + "."
	case openTabs > 0:
		return "Saving will " + tabs + "."
	}
	return "Saving will " + jobs + "."
}

// EditorWarnings are the lines of the editor's one warning box, none when
// the save moves nothing (Electron's EditorWarnings).
func EditorWarnings(s *hubapi.Server, d HostDraft, openTabs, transfers int) []string {
	var w []string
	if EndpointChanged(s, d) {
		w = append(w, "Changing host or port forgets the host key and saved passwords unless you re-enter them.")
	}
	if (openTabs > 0 || transfers > 0) && ClosesTabs(s, d) {
		w = append(w, CloseWarning(openTabs, transfers))
	}
	return w
}

// NextChecked is the import sheet's checked aliases: every ready row on the
// first scan (prev nil); on a rescan, only rows still ready that were checked.
func NextChecked(cs []hubapi.Candidate, prev map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, c := range cs {
		if c.Status == "ready" && (prev == nil || prev[c.Alias]) {
			out[c.Alias] = true
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests**

Run: `cd app && go test ./model -count=1 && go vet ./model && gofmt -l model`
Expected: `ok  github.com/lang315/sshgate/app/model`, no gofmt output.

- [ ] **Step 5: Commit**

```bash
git add app/model/hostform.go app/model/hostform_test.go
git commit -m "app: model host form rules and import checks (hostForm.ts port)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: `view`: the host editor (New host, Edit, secrets, warnings, Save)

Ports `desktop/src/renderer/HostEditor.tsx` without the Auto-allow part (3b) and without Forget (Task 4), the editor wiring of `App.tsx:209-216, 281-284, 310-318`, and the rendered-editor cases of `hostForm.test.ts` (renderer.md §1.2-§1.4, §5; mygo.md §1, §2).

**Files:**
- Create: `app/view/editor.go`
- Modify: `app/view/app.go` (the `App` struct; `setScreen`), `app/view/home.go` (`buildReady`, `buildHosts`)
- Test: `app/view/editor_test.go` (create), `app/view/app_test.go` (`fakeDoor.gates`)

**Interfaces:**
- Consumes: `model.HostDraft`, `model.DraftFrom`, `model.ToInput`, `model.PortError`, `model.LabelHint`, `model.SecretHint`, `model.EndpointChanged`, `model.EditorWarnings`, `model.Secret*` (Task 2); `hubapi.Client.SaveServer` (Task 1); `App.reloadServers`, `App.focusActive`, `App.hubGen`, `App.alert`, `model.TabSet.OpenCount` (slices 1-2); the test helpers `readyApp`, `newApp`, `note`, `fakeDoor` (`d.set`, `d.callList`, `d.resetCalls`, `d.params`, `d.errs`, `d.hang`), `tester.flush`.
- Produces (package `view`):
  - `type editorState struct { seq int; name string; draft model.HostDraft; showAuth, escOpen, aiOpen, focusForget, busy bool; err string }`; `App.editor *editorState`, `App.sheetSeq int`, `App.searchH ui.Handle`.
  - `func (a *App) server(name string) *hubapi.Server`, `func (a *App) openEditor(name string, focusForget bool)`, `func (a *App) dropDialogs()`, `func (a *App) closeSheet()`, `func keptMode(s *hubapi.Server, d model.HostDraft) string`, `func (a *App) saveHost()`, `func (a *App) buildEditor(c *ui.Context)`, `func (a *App) buildEditorBody(c *ui.Context, s *hubapi.Server, portErr string) bool`, `func (a *App) buildAIAccess(c *ui.Context, s *hubapi.Server)`; `const savedBut`.
  - Test helpers: `fakeDoor.gates map[string]chan struct{}` (a method answers once its channel closes, so a test can hold a reply and release it); in `editor_test.go`: `boxServer`, `clickT(t, tt, label)`, `fill(t, tt, label, text)`, `count(texts, s) int`, `hasCall(d, method) bool`, `savedParams(d) string`, `edReady(t, servers)`, `editBox(t, s)`.

- [ ] **Step 1: Write the failing tests**

In `app/view/app_test.go`, replace `fakeDoor`'s `pendGate` line with

```go
	pendGate chan struct{}            // when set, pending waits on it
	gates    map[string]chan struct{} // these methods answer once their channel closes
```

in `Call`, after `d.params[method] = string(b)`:

```go
	wait := d.gates[method]
```

and right after that function's `d.mu.Unlock()`:

```go
	if wait != nil {
		<-wait
	}
```

`app/view/editor_test.go`:

```go
package view

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
	"github.com/lang315/sshgate/app/model"
)

// boxServer is hostForm.test.ts's fixture: password auth, a saved password,
// pinned.
var boxServer = hubapi.Server{Name: "box", Host: "h", Port: 22, User: "u", Auth: "password", HostKey: "SHA256:x", HostKeyAlgo: "ssh-ed25519", HasPassword: true}

func clickT(t *testing.T, tt *tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

// fill replaces a text field's value, as the e2e's fill does.
func fill(t *testing.T, tt *tester, label, text string) {
	t.Helper()
	clickT(t, tt, label)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(text)
	tt.Frame()
}

func count(texts []string, s string) int {
	n := 0
	for _, x := range texts {
		if x == s {
			n++
		}
	}
	return n
}

func hasCall(d *fakeDoor, method string) bool {
	for _, m := range d.callList() {
		if m == method {
			return true
		}
	}
	return false
}

func savedParams(d *fakeDoor) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.params["servers.save"]
}

// edReady is readyApp in a window tall enough for the whole editor: the
// Tester clicks where an element is, scrolled out of view or not.
func edReady(t *testing.T, servers []hubapi.Server) (*App, *tester, *fakeDoor) {
	a, tt, d := readyApp(t, servers)
	tt.SetSize(800, 900)
	tt.Frame()
	return a, tt, d
}

func editBox(t *testing.T, s hubapi.Server) (*App, *tester, *fakeDoor) {
	a, tt, d := edReady(t, []hubapi.Server{s})
	clickT(t, tt, "Edit box")
	return a, tt, d
}

func TestNewHostOpensAtAddressWithExtrasFolded(t *testing.T) {
	a, tt, d := edReady(t, []hubapi.Server{boxServer})
	d.resetCalls()
	clickT(t, tt, "New host")
	if !tt.HasText("New host") || !tt.HasText("Close host editor") || !tt.Focused("Address") {
		t.Fatalf("editor: %q", tt.Texts())
	}
	addr, _ := tt.Find("Address")
	label, _ := tt.Find("Label")
	if addr.Y >= label.Y {
		t.Fatalf("Address %+v not above Label %+v", addr, label)
	}
	for _, s := range []string{"+ Key or agent", "AI access · Off", "Privilege escalation (su / sudo)"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	for _, s := range []string{"key", "Saved secrets are never shown. Leave a field empty to keep it.", "su password", "Host key"} {
		if count(tt.Texts(), s) > 0 {
			t.Fatalf("%q shown for a new host: %q", s, tt.Texts())
		}
	}
	if a.editor.draft.AIVisible {
		t.Fatal("Visible to AI is on for a new host")
	}
	clickT(t, tt, "AI access · Off")
	for _, s := range []string{"Visible to AI", "Needs a pinned host key."} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	tt.Type("10.0.0.9") // the typing and the folds call nothing: only a save does
	tt.flush()
	if got := d.callList(); len(got) != 0 {
		t.Fatalf("calls while editing: %v", got)
	}
}

func TestEditShowsThePinAndOnlySavedHints(t *testing.T) {
	_, tt, _ := editBox(t, boxServer)
	texts := tt.Texts()
	for _, s := range []string{"Edit box", "ssh-ed25519 SHA256:x", "Saved secrets are never shown. Leave a field empty to keep it."} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, texts)
		}
	}
	if count(texts, "saved") != 1 || count(texts, "Clear") != 1 || count(texts, "Close") != 1 {
		t.Fatalf("want one saved, one Clear, one Close in %q", texts)
	}
}

func TestKeyAuthShowsOneRadioStopOnKey(t *testing.T) {
	s := boxServer
	s.Auth, s.KeyPath = "key", "~/.ssh/id"
	a, tt, _ := editBox(t, s)
	if tt.HasText("+ Key or agent") || !tt.HasText("Key path") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Key path")
	tt.Key(ui.Shift, ui.KeyTab)
	tt.Frame()
	if !tt.Focused("key") || a.editor.draft.Auth != "key" {
		t.Fatal("Shift+Tab from Key path should land on the checked radio, key")
	}
	tt.Key(ui.Shift, ui.KeyTab)
	tt.Frame()
	if tt.Focused("password") || tt.Focused("agent") {
		t.Fatal("the Auth radios are more than one Tab stop")
	}
}

// The Auth radios: the arrows choose and wrap (Electron's arrowStep rows),
// and the focus follows although the Password field goes with the choice.
func TestArrowsMoveTheAuthChoiceAndKeepTheFocus(t *testing.T) {
	a, tt, _ := edReady(t, []hubapi.Server{})
	clickT(t, tt, "New host")
	clickT(t, tt, "+ Key or agent")
	clickT(t, tt, "password")
	for _, step := range []struct {
		key  ui.Key
		want string
	}{{ui.KeyRight, "key"}, {ui.KeyLeft, "password"}, {ui.KeyLeft, "agent"}, {ui.KeyDown, "password"}, {ui.KeyUp, "agent"}} {
		tt.Key(0, step.key)
		tt.Frame()
		if !tt.Focused(step.want) || a.editor.draft.Auth != step.want {
			t.Fatalf("after %v: auth %q, want %q focused and chosen", step.key, a.editor.draft.Auth, step.want)
		}
	}
}

func TestSaveSendsUntouchedClearedAndTypedSecrets(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	clickT(t, tt, "Clear Password")
	if !tt.HasText("will be cleared") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Privilege escalation (su / sudo)")
	clickT(t, tt, "su password")
	tt.Type("su!")
	d.resetCalls()
	clickT(t, tt, "Save")
	tt.flush()
	want := `{"original":"box","server":{"name":"box","host":"h","port":22,"user":"u","auth":"password","keyPath":"","aiVisible":false,"autoAllowRoot":false,"autoAllowSudo":false,"password":"","suPassword":"su!"},"autoAllow":"off"}`
	if got := savedParams(d); got != want {
		t.Fatalf("sent %s\nwant %s", got, want)
	}
	if a.editor != nil || !tt.Focused("Search hosts") {
		t.Fatal("a saved editor stays open, or the focus went nowhere")
	}
	if got := d.callList(); len(got) != 2 || got[0] != "servers.save" || got[1] != "servers" {
		t.Fatalf("calls %v: want the save, then the host list", got)
	}
}

func TestTypingAfterClearSendsTheNewValue(t *testing.T) {
	_, tt, d := editBox(t, boxServer)
	clickT(t, tt, "Clear Password")
	clickT(t, tt, "Password")
	tt.Type("new")
	clickT(t, tt, "Save")
	tt.flush()
	if got := savedParams(d); !strings.Contains(got, `"password":"new"`) {
		t.Fatal(got)
	}
}

func TestEnterInAFieldSavesANewHost(t *testing.T) {
	a, tt, d := edReady(t, []hubapi.Server{})
	clickT(t, tt, "New host")
	tt.Type(" 10.0.4.21 ")
	clickT(t, tt, "User")
	tt.Type("u")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	want := `{"server":{"name":"10.0.4.21","host":"10.0.4.21","port":22,"user":"u","auth":"password","keyPath":"","aiVisible":false,"autoAllowRoot":false,"autoAllowSudo":false},"autoAllow":"off"}`
	if got := savedParams(d); got != want || a.editor != nil {
		t.Fatalf("sent %s\nwant %s", got, want)
	}
}

func TestBadPortIsNeverSent(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	fill(t, tt, "Port", "70000")
	if !tt.HasText("Port must be 1-65535.") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Save")
	clickT(t, tt, "Port")
	tt.Key(0, ui.KeyEnter)
	tt.flush()
	if hasCall(d, "servers.save") || a.editor == nil {
		t.Fatalf("calls %v", d.callList())
	}
}

func TestPortChangeWarnsAndCountsTheTabsASaveCloses(t *testing.T) {
	a, tt, _ := edReady(t, []hubapi.Server{boxServer})
	a.Update(func() { a.tabs.Open("box"); a.tabs.Open("box"); a.tabs.ShowHome() })
	tt.flush()
	clickT(t, tt, "Edit box")
	fill(t, tt, "Port", "2222")
	for _, s := range []string{"Changing host or port forgets the host key and saved passwords unless you re-enter them.", "Saving will close 2 open tabs.", "will be cleared", "· changed"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	fill(t, tt, "Port", "22")
	clickT(t, tt, "AI access · Off")
	clickT(t, tt, "Visible to AI")
	if tt.HasText("Saving will close 2 open tabs.") || !tt.HasText("AI access · On") {
		t.Fatalf("Visible to AI alone warns: %q", tt.Texts())
	}
}

func TestSaveErrorStaysInTheEditor(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	d.set(func() { d.errs = map[string]error{"servers.save": errors.New(`a server named "db" already exists`)} })
	clickT(t, tt, "Save")
	tt.flush()
	if a.editor == nil || !tt.HasText(`a server named "db" already exists`) {
		t.Fatal(tt.Texts())
	}
}

func TestSavedButArmingFailedAlertsAndCloses(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	d.set(func() {
		d.errs = map[string]error{"servers.save": errors.New("saved, but auto-allow was not turned on: auto-allow refused: root login")}
	})
	clickT(t, tt, "Save")
	tt.flush()
	if a.editor != nil || !tt.HasText("Saved, but auto-allow was not turned on: auto-allow refused: root login") || !hasCall(d, "servers") {
		t.Fatal(tt.Texts())
	}
}

func TestSaveKeepsOnlyALiveForeverGrant(t *testing.T) {
	for _, tc := range []struct {
		auto *hubapi.AutoAllow
		want string
	}{
		{&hubapi.AutoAllow{Forever: true}, `"autoAllow":"forever"`},
		{&hubapi.AutoAllow{Forever: true, Paused: true}, `"autoAllow":"off"`},
		{&hubapi.AutoAllow{Until: "2026-10-15T13:00:00Z"}, `"autoAllow":"off"`},
	} {
		s := boxServer
		s.AutoAllow = tc.auto
		_, tt, d := editBox(t, s)
		clickT(t, tt, "Save")
		tt.flush()
		if got := savedParams(d); !strings.HasSuffix(got, tc.want+"}") {
			t.Errorf("%+v: sent %s", tc.auto, got)
		}
	}
}

func TestEscClosesOnlyFromInsideTheEditor(t *testing.T) {
	a, tt, _ := editBox(t, boxServer)
	clickT(t, tt, "Search hosts")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.editor == nil {
		t.Fatal("Esc outside the editor closed it")
	}
	clickT(t, tt, "Label")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.editor != nil || !tt.Focused("Search hosts") {
		t.Fatal("Esc inside did not close the editor, or left the focus nowhere")
	}
}

// A save in flight blocks Esc and a second save; its late reply never
// touches an editor opened since, nor anything once its hub stopped.
func TestLateSaveReplyLeavesANewEditorAlone(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	gate := make(chan struct{})
	d.set(func() { d.gates = map[string]chan struct{}{"servers.save": gate} })
	clickT(t, tt, "Save")
	clickT(t, tt, "Save")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.editor == nil || count(d.callList(), "servers.save") != 1 {
		t.Fatalf("busy editor: open %v, calls %v", a.editor != nil, d.callList())
	}
	clickT(t, tt, "Close")
	clickT(t, tt, "New host")
	close(gate) // the save succeeds now
	tt.flush()
	if a.editor == nil || a.editor.name != "" || a.editor.err != "" || !hasCall(d, "servers") {
		t.Fatalf("the late reply closed or touched the new editor: %+v", a.editor)
	}

	clickT(t, tt, "Close host editor")
	clickT(t, tt, "Edit box")
	gate = make(chan struct{})
	d.set(func() {
		d.gates = map[string]chan struct{}{"servers.save": gate}
		d.errs = map[string]error{"servers.save": errors.New("saved, but auto-allow was not turned on: server changed; try again")}
	})
	clickT(t, tt, "Save")
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", In: time.Second}) })
	tt.flush()
	close(gate)
	tt.flush()
	if a.editor != nil || a.alert != "" {
		t.Fatalf("a hub stop leaves the editor open, or the dead hub's reply showed: %q", a.alert)
	}
}

func TestLockDropsTheEditorAndItsTypedSecret(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	clickT(t, tt, "Password")
	tt.Type("typed-secret")
	d.set(func() { d.status.Locked = true })
	note(t, a, tt, "locked", map[string]string{"reason": "manual"})
	if a.Screen() != model.ScreenLocked || a.editor != nil {
		t.Fatal("the editor outlived the lock")
	}
	d.set(func() { d.status.Locked = false })
	a.Update(a.refresh)
	tt.flush()
	if a.Screen() != model.ScreenReady || a.editor != nil || tt.HasText("Host editor") {
		t.Fatal("an unlock brought the editor back")
	}
}

func TestSaveIsReachableWithEveryFoldOpen(t *testing.T) {
	s := boxServer
	s.Auth, s.HasSuPassword = "key", true
	a, tt, _ := editBox(t, s)
	clickT(t, tt, "AI access · Off") // the lower fold first: opening one moves what is below it
	clickT(t, tt, "Privilege escalation (su / sudo) · saved")
	if !a.editor.escOpen || !a.editor.aiOpen {
		t.Fatal("a fold did not open")
	}
	tt.SetSize(800, 500)
	tt.Frame()
	r, ok := tt.Find("Save")
	if !ok || r.Y < 0 || r.Y+r.H > 500 {
		t.Fatalf("Save at %+v in a 500-high window", r)
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `cd app && go test -short ./view -count=1`
Expected: FAIL to compile: `a.editor undefined (type *App has no field or method editor)`.

- [ ] **Step 3: Implement**

`app/view/editor.go`:

```go
package view

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// savedBut starts the hub's error for a save that stood while its grant
// was not armed (Electron's SAVED_BUT).
const savedBut = "saved, but auto-allow was not turned on: "

// editorState is the open host editor (Electron's HostEditor.tsx): name is
// the server edited, "" for New host. A typed secret lives only in draft,
// which goes when the editor closes.
type editorState struct {
	seq             int // keys the sheet: each editor is a new mount, so AutoFocus runs again
	name            string
	draft           model.HostDraft
	showAuth        bool // the Auth radios instead of "+ Key or agent"
	escOpen, aiOpen bool // the two folds
	focusForget     bool // opened from the mismatch dialog
	busy            bool
	err             string
}

// server is a copy of the stored server called name, or nil.
func (a *App) server(name string) *hubapi.Server {
	for i := range a.servers {
		if name != "" && a.servers[i].Name == name {
			s := a.servers[i]
			return &s
		}
	}
	return nil
}

// openEditor shows the editor for name ("" for New host) from the list the
// app holds, in place of the import sheet.
func (a *App) openEditor(name string, focusForget bool) {
	s := a.server(name)
	a.sheetSeq++
	a.editor = &editorState{seq: a.sheetSeq, name: name, draft: model.DraftFrom(s), showAuth: s != nil && s.Auth != "password", focusForget: focusForget}
}

// dropDialogs closes the host dialogs when the ready screen goes (lock, hub
// stop): a typed secret must not outlive it, and an unlock opens none again.
func (a *App) dropDialogs() { a.editor = nil }

// closeSheet drops the open sheet, and any typed secret with it. mygo
// restores no focus: the active terminal gets it, else the host search.
func (a *App) closeSheet() {
	a.dropDialogs()
	if a.tabs.Active != "" {
		a.focusActive()
		return
	}
	a.searchH.Focus()
}

// keptMode is the autoAllow a save sends while the editor has no Auto-allow
// select: a live forever grant is armed again in the same write (Electron
// saves that without asking); a paused or timed one, or none, is off. 3b
// replaces it with saveConfirm and the Auto-allow dialog.
func keptMode(s *hubapi.Server, d model.HostDraft) string {
	if d.AutoAllow == "forever" && s != nil && s.AutoAllow != nil && s.AutoAllow.Forever && !s.AutoAllow.Paused {
		return "forever"
	}
	return "off"
}

// saveHost sends servers.save. The reply is dropped if its hub stopped; it
// touches this editor only, never one opened since.
func (a *App) saveHost() {
	ed := a.editor
	if ed == nil || ed.busy {
		return
	}
	in, mode := model.ToInput(ed.draft), keptMode(a.server(ed.name), ed.draft)
	ed.busy, ed.err = true, ""
	gen := a.hubGen
	go func() {
		err := a.hub().SaveServer(context.Background(), ed.name, in, mode)
		a.Update(func() {
			if gen != a.hubGen {
				return
			}
			ed.busy = false
			if err != nil && !strings.HasPrefix(err.Error(), savedBut) {
				ed.err = err.Error()
				return
			}
			if err != nil {
				a.alert = "Saved, but auto-allow was not turned on: " + strings.TrimPrefix(err.Error(), savedBut)
			}
			a.reloadServers()
			if a.editor == ed {
				a.closeSheet()
			}
		})
	}()
}

// buildEditor is the host editor: a column beside the content, not a modal,
// so the AI requests column stays usable. Esc closes it only while the
// focus is inside it, and not while a save runs.
func (a *App) buildEditor(c *ui.Context) {
	t := c.Theme()
	ed := a.editor
	s := a.server(ed.name)
	portErr := model.PortError(ed.draft.Port)
	title := "New host"
	if ed.name != "" {
		title = "Edit " + model.DisplayText(ed.name)
	}
	submit, closed := false, false
	sheet := ui.Column(c.Key("editor-"+strconv.Itoa(ed.seq))).Fill().Width(420).Background(t.Surface).BorderWidth(0, 0, 0, 1).BorderColor(t.Border).
		Role(ui.RoleDialog).Label("Host editor")
	sheet.Children(func() {
		ui.Row(c).Gap(8).Padding(12, 16).Children(func() {
			ui.Text(c, title).Bold().FontSize(16).Grow(1)
			if ui.Button(c, "×").Label("Close host editor").Tooltip("Close").Clicked() {
				closed = true
			}
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(14).Padding(8, 16).Children(func() {
				submit = a.buildEditorBody(c, s, portErr)
			})
		})
		ui.Row(c).Gap(8).Padding(12, 16).Justify(ui.End).Children(func() {
			if ed.err != "" {
				ui.Text(c, model.DisplayText(ed.err)).TextColor(t.Danger).Grow(1)
			}
			if ui.Button(c, "Close").Clicked() {
				closed = true
			}
			if ui.PrimaryButton(c, "Save").Disabled(ed.busy || portErr != "").Clicked() {
				submit = true
			}
		})
	})
	if sheet.Shortcut(0, ui.KeyEscape) && !ed.busy {
		closed = true
	}
	switch {
	case closed:
		a.closeSheet()
	case submit && portErr == "":
		a.saveHost()
	}
}

// buildEditorBody draws the form, top to bottom as Electron's; it reports
// Enter in a text field, which saves.
func (a *App) buildEditorBody(c *ui.Context, s *hubapi.Server, portErr string) (submit bool) {
	t := c.Theme()
	ed := a.editor
	d := &ed.draft
	in := model.ToInput(*d)
	moved := model.EndpointChanged(s, *d)
	text := func(key, label string, v *string, opts func(ui.Element)) ui.Element {
		var input ui.Element
		f := ui.Field(c, label, func() {
			input = ui.TextInput(c.Key(key), v)
			opts(input)
		})
		if input.Submitted() {
			submit = true
		}
		return f
	}
	secret := func(f model.Secret) {
		e := &d.Secrets[f]
		saved := f.Saved(s)
		hint := model.SecretHint(saved, *e, moved)
		ui.Row(c.Key(fmt.Sprintf("secret-%d", f))).Gap(8).Children(func() {
			text(fmt.Sprintf("secret-in-%d", f), f.Label(), &e.Value, func(input ui.Element) {
				input.Password().Placeholder(hint)
				if input.Changed() {
					e.Cleared = false // typing after Clear sends the new value
				}
			}).Description(hint).Grow(1)
			if saved && !e.Cleared && ui.Button(c, "Clear").Label("Clear "+f.Label()).Clicked() {
				e.Value, e.Cleared = "", true
			}
		})
	}

	addr := text("host", "Address", &d.Host, func(input ui.Element) {
		input.Placeholder("IP or hostname").Font("monospace")
		if !ed.focusForget {
			input.AutoFocus()
		}
	})
	if s != nil && in.Host != s.Host {
		addr.Description("· changed")
	}
	ph := in.Host
	if ph == "" {
		ph = "Defaults to the address"
	}
	label := text("name", "Label", &d.Name, func(input ui.Element) { input.Placeholder(ph) })
	if h := model.LabelHint(*d); h != "" {
		label.Description(h)
	}
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, "SSH on")
		if ui.TextInput(c.Key("port"), &d.Port).Label("Port").Font("monospace").Width(90).Submitted() {
			submit = true
		}
		ui.Text(c, "port")
		if s != nil && in.Port != s.Port {
			ui.Text(c, "· changed").TextColor(t.Warning)
		}
	})
	if portErr != "" {
		ui.Text(c, portErr).TextColor(t.Danger)
	}

	ui.Text(c, "Credentials").Bold()
	for f := model.SecretPassword; f <= model.SecretKeyPassphrase; f++ {
		if f.Saved(s) {
			ui.Text(c, "Saved secrets are never shown. Leave a field empty to keep it.").TextColor(t.TextMuted)
			break
		}
	}
	text("user", "User", &d.User, func(input ui.Element) { input.Placeholder("Username").Font("monospace") })
	if d.Auth == "password" || model.SecretPassword.Saved(s) {
		secret(model.SecretPassword)
	}
	if !ed.showAuth {
		if ui.Button(c, "+ Key or agent").Clicked() {
			ed.showAuth = true
		}
	} else {
		ui.Text(c, "Auth")
		// Keyed: the Password field above comes and goes with the choice, and
		// an unkeyed radio would lose the focus the arrows just gave it.
		ui.RadioGroup(c.Key("auth"), func() { // one Tab stop; the arrows choose, and wrap
			for _, v := range []string{"password", "key", "agent"} {
				ui.Radio(c.Key("auth-"+v), &d.Auth, v, v)
			}
		}).Row().Label("Auth")
		if d.Auth == "key" {
			text("keypath", "Key path", &d.KeyPath, func(input ui.Element) { input.Placeholder("~/.ssh/id_ed25519").Font("monospace") })
			secret(model.SecretKeyPassphrase)
		}
	}

	esc := "Privilege escalation (su / sudo)"
	if model.SecretSu.Saved(s) || model.SecretSudo.Saved(s) {
		esc += " · saved"
	}
	ui.Collapsible(c.Key("fold-esc"), esc, &ed.escOpen, func() {
		secret(model.SecretSu)
		secret(model.SecretSudo)
	})
	ai := "AI access · Off"
	if d.AIVisible {
		ai = "AI access · On"
	}
	if d.AutoAllow != "off" {
		ai += " · auto"
	}
	ui.Collapsible(c.Key("fold-ai"), ai, &ed.aiOpen, func() { a.buildAIAccess(c, s) })

	if s != nil {
		ui.Text(c, "Host key").Bold()
		if s.HostKey != "" {
			ui.Text(c, model.DisplayText(strings.TrimSpace(s.HostKeyAlgo+" "+s.HostKey))).Font("monospace").Selectable()
		} else {
			ui.Text(c, "Not pinned. You will be asked to confirm it on the next connect.").TextColor(t.TextMuted)
		}
	}
	if w := model.EditorWarnings(s, *d, a.tabs.OpenCount(ed.name), 0); len(w) > 0 {
		ui.Column(c).Gap(4).Padding(8, 12).Radius(6).Background(t.Warning.Alpha(0.15)).Role(ui.RoleStatus).Children(func() {
			for _, line := range w {
				ui.Text(c, line)
			}
		})
	}
	return submit
}

// buildAIAccess is the AI access fold. 3b adds the Auto-allow select and the
// two opt-in checkboxes here, below the switch.
func (a *App) buildAIAccess(c *ui.Context, s *hubapi.Server) {
	t := c.Theme()
	ui.Field(c, "Visible to AI", func() { ui.Switch(c, &a.editor.draft.AIVisible) })
	ui.Text(c, "AI clients can see this server and ask to run commands. Commands wait for your approval unless auto-allow is on.").TextColor(t.TextMuted)
	if s == nil || s.HostKey == "" {
		ui.Text(c, "Needs a pinned host key.").TextColor(t.TextMuted)
	}
}
```

In `app/view/app.go`, add to the `App` struct after `sendSeq`:

```go
	editor                 *editorState
	sheetSeq               int       // numbers the sheets opened
	searchH                ui.Handle // the host search: the focus after a sheet closes on Hosts
```

and in `setScreen`'s `default:` branch, after `a.servers = nil`:

```go
		a.dropDialogs()
```

In `app/view/home.go` `buildReady`, between the content column's closing `})` and `if a.aiOpen {`, insert:

```go
			if a.editor != nil {
				a.buildEditor(c)
			}
```

In `buildHosts`, replace

```go
		ui.TextInput(c.Key("search"), &a.query).Placeholder("Search hosts").Label("Search hosts")
```

with

```go
		ui.Row(c).Gap(8).Children(func() {
			ui.TextInput(c.Key("search"), &a.query).Placeholder("Search hosts").Label("Search hosts").Grow(1).Bind(&a.searchH)
			if ui.Button(c, "New host").Clicked() {
				a.openEditor("", false)
			}
		})
```

and replace the card's chips row

```go
					ui.Row(c).Gap(6).Wrap().Children(func() {
						if s.AIVisible {
							ui.Text(c, "AI").TextColor(t.Accent)
						}
						if s.HostKey == "" {
							ui.Text(c, "New key").TextColor(t.Warning)
						}
						if chip := model.ChipLabel(s.AutoAllow, a.Now()); chip != "" {
							ui.Text(c, chip).TextColor(t.Warning)
						}
					})
```

with a row of the chips and the `Edit` button:

```go
					ui.Row(c).Gap(6).Children(func() {
						ui.Row(c).Gap(6).Wrap().Grow(1).Children(func() {
							if s.AIVisible {
								ui.Text(c, "AI").TextColor(t.Accent)
							}
							if s.HostKey == "" {
								ui.Text(c, "New key").TextColor(t.Warning)
							}
							if chip := model.ChipLabel(s.AutoAllow, a.Now()); chip != "" {
								ui.Text(c, chip).TextColor(t.Warning)
							}
						})
						if ui.Button(c, "Edit").Label("Edit " + shownName).Tooltip("Edit").Clicked() {
							a.openEditor(name, false)
						}
					})
```

- [ ] **Step 4: Run the tests**

Run: `cd app && go test -short ./view -count=1 && go vet ./view && gofmt -l view`
Expected: `ok  github.com/lang315/sshgate/app/view`, no gofmt output. The slice 1-2 tests keep passing (the search field now sits in a row with `New host`).

Check that they bite, one at a time, restoring after each:
- in `buildEditor`, replace `if sheet.Shortcut(0, ui.KeyEscape) && !ed.busy {` with `if false && sheet.Shortcut(0, ui.KeyEscape) && !ed.busy {`: `TestEscClosesOnlyFromInsideTheEditor` must FAIL;
- in `saveHost`'s reply, replace `if a.editor == ed {` with `if a.editor != nil {`: `TestLateSaveReplyLeavesANewEditorAlone` must FAIL at "the late reply closed or touched the new editor";
- in `saveHost`'s reply, replace `if gen != a.hubGen {` with `if false && gen != a.hubGen {`: the same test must FAIL with the dead hub's `Saved, but …` alert;
- remove `c.Key("auth")` and `c.Key("auth-"+v)` from the Auth radios (use `c`): `TestArrowsMoveTheAuthChoiceAndKeepTheFocus` must FAIL after `ArrowRight`.

- [ ] **Step 5: Commit**

```bash
git add app/view/editor.go app/view/editor_test.go app/view/app_test.go app/view/app.go app/view/home.go
git commit -m "app: host editor sheet (New host, Edit, write-only secrets, warnings, Save)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: `view`: Forget host key, Delete with confirm, and the mismatch dialog's Open host editor

Ports the editor's Host key section (`HostEditor.tsx:194-208`), `deleteHost` (`App.tsx:217-221`) and `HostKeyMismatchDialog`'s `onEdit` (`HostKeyDialog.tsx:74`, `App.tsx:343`) (renderer.md §1.2, §1.3, §5; protocol.md §1.2, §1.3).

**Files:**
- Modify: `app/view/editor.go`, `app/view/app.go`, `app/view/home.go`, `app/view/hostkey.go`
- Test: `app/view/hostwrites_test.go` (create), `app/view/hostkey_test.go` (one test)

**Interfaces:**
- Consumes: Task 3's `editorState`, `openEditor`, `dropDialogs`, `editBox`, `edReady`, `clickT`, `hasCall`, `boxServer`; `hubapi.Client.ForgetHostKey` (slice 2), `DeleteServer` (Task 1); `model.TrustEnabled`, `model.AllowDelay` (slice 2); `hkApp`, `keyDial`, `clickOpenBox` (slice 2 tests).
- Produces (package `view`): `func (a *App) forgetKey()`, `type deleteState struct { name string; at time.Time }`, `App.del *deleteState`, `func (a *App) askDelete(name string)`, `func (a *App) buildDeleteConfirm(c *ui.Context)`, `func (a *App) deleteHost(name string)`; test helper `params(d *fakeDoor, method string) string`.

- [ ] **Step 1: Write the failing tests**

`app/view/hostwrites_test.go`:

```go
package view

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/termconn"
)

func params(d *fakeDoor, method string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.params[method]
}

func TestForgetUnpinsAndKeepsTheEditorOpen(t *testing.T) {
	a, tt, d := editBox(t, boxServer)
	if !tt.HasText("Takes effect immediately.") {
		t.Fatal(tt.Texts())
	}
	unpinned := boxServer
	unpinned.HostKey, unpinned.HostKeyAlgo = "", ""
	d.set(func() { d.servers = []hubapi.Server{unpinned} })
	d.resetCalls()
	clickT(t, tt, "Forget host key")
	tt.flush()
	if got := d.callList(); len(got) != 2 || got[0] != "servers.forgetHostKey" || got[1] != "servers" || params(d, "servers.forgetHostKey") != `{"name":"box"}` {
		t.Fatalf("calls %v %s", got, params(d, "servers.forgetHostKey"))
	}
	if a.editor == nil || !tt.HasText("Not pinned. You will be asked to confirm it on the next connect.") || tt.HasText("Forget host key") {
		t.Fatal(tt.Texts())
	}
}

func TestForgetErrorShowsInTheEditor(t *testing.T) {
	_, tt, d := editBox(t, boxServer)
	d.set(func() {
		d.errs = map[string]error{"servers.forgetHostKey": errors.New("Vault is locked; unlock it in the app")}
	})
	clickT(t, tt, "Forget host key")
	tt.flush()
	if !tt.HasText("Vault is locked; unlock it in the app") || !tt.HasText("Forget host key") {
		t.Fatal(tt.Texts())
	}
}

func TestDeleteWaitsHalfASecondThenDeletes(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{boxServer}}
	a, tt, c, _ := newApp(t, d)
	tt.flush()
	clickT(t, tt, "Delete box")
	if !tt.HasText("Delete box? Its saved passwords and host key are removed and its open tabs close.") || !tt.Focused("Cancel") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Delete host")
	tt.flush()
	if hasCall(d, "servers.delete") || a.del == nil {
		t.Fatal("Delete worked before 500 ms")
	}
	c.add(600 * time.Millisecond)
	tt.Frame()
	d.resetCalls()
	clickT(t, tt, "Delete host")
	tt.flush()
	if got := d.callList(); len(got) != 2 || got[0] != "servers.delete" || got[1] != "servers" || params(d, "servers.delete") != `{"name":"box"}` {
		t.Fatalf("calls %v", got)
	}
	if a.del != nil || !tt.Focused("Search hosts") {
		t.Fatal("the confirm stays, or the focus went nowhere")
	}
}

func TestDeleteConfirmCancelsWithEnterAndEsc(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{boxServer}}
	a, tt, c, _ := newApp(t, d)
	tt.flush()
	for _, key := range []ui.Key{ui.KeyEnter, ui.KeyEscape} {
		clickT(t, tt, "Delete box")
		c.add(600 * time.Millisecond)
		tt.Key(0, key)
		tt.flush()
		if a.del != nil || hasCall(d, "servers.delete") {
			t.Fatalf("key %v deleted, or left the confirm open", key)
		}
	}
}

func TestDeleteRefusalAlerts(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}, servers: []hubapi.Server{boxServer}, errs: map[string]error{"servers.delete": errors.New(`server "box" not found`)}}
	_, tt, c, _ := newApp(t, d)
	tt.flush()
	clickT(t, tt, "Delete box")
	c.add(600 * time.Millisecond)
	tt.Frame()
	clickT(t, tt, "Delete host")
	tt.flush()
	if !tt.HasText(`server "box" not found`) || !hasCall(d, "servers") {
		t.Fatal(tt.Texts())
	}
}

func TestMismatchOpensTheEditorOnForget(t *testing.T) {
	mis := hubapi.HostKey{Status: "hostKeyMismatch", Server: "box", Host: "127.0.0.1", Port: 2222, User: "test", Pinned: "SHA256:old", Presented: "SHA256:new"}
	k := &keyDial{answer: func(int) (*termconn.Conn, error) { return nil, &termconn.OpenError{Status: mis.Status, Key: mis} }}
	a, tt, _, d := hkApp(t, k)
	d.set(func() { d.servers[0].HostKey, d.servers[0].HostKeyAlgo = "SHA256:old", "ssh-ed25519" })
	a.Update(a.reloadServers)
	tt.flush()
	clickOpenBox(t, tt)
	if !tt.HasText("Open host editor") || !tt.Focused("Close") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Open host editor")
	tt.Frame()
	if a.mismatch != nil || a.editor == nil || a.editor.name != "box" || !tt.HasText("Edit box") {
		t.Fatal(tt.Texts())
	}
	if !tt.Focused("Forget host key") || tt.Focused("Address") {
		t.Fatal("Forget host key is not focused")
	}
}
```

In `app/view/hostkey_test.go`, rename `TestMismatchShowsBothKeysAndOnlyCloses` to `TestMismatchShowsBothKeysAndCloses` and replace

```go
	if tt.HasText("Trust and connect") || !tt.Focused("Close") {
		t.Fatal("the mismatch dialog offers only Close:", tt.Texts())
```

with

```go
	if tt.HasText("Trust and connect") || !tt.HasText("Open host editor") || !tt.Focused("Close") {
		t.Fatal("the mismatch dialog offers Open host editor and Close, never Trust:", tt.Texts())
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `cd app && go test -short ./view -count=1`
Expected: FAIL to compile: `a.del undefined (type *App has no field or method del)`.

- [ ] **Step 3: Implement**

In `app/view/editor.go`, add `"time"` to the imports, and replace `dropDialogs` with

```go
func (a *App) dropDialogs() { a.editor, a.del = nil, nil }
```

In `buildEditorBody`, after `ui.Text(c, model.DisplayText(strings.TrimSpace(s.HostKeyAlgo+" "+s.HostKey))).Font("monospace").Selectable()`, insert:

```go
			ui.Row(c).Gap(8).Children(func() {
				forget := ui.Button(c, "Forget host key").Disabled(ed.busy)
				if ed.focusForget {
					forget.AutoFocus()
				}
				if forget.Clicked() {
					a.forgetKey()
				}
				ui.Text(c, "Takes effect immediately.").TextColor(t.TextMuted)
			})
```

Append to `app/view/editor.go`:

```go

// forgetKey unpins the edited server's host key at once, as Electron does,
// with no confirm; the editor stays open and shows it unpinned once the
// host list is back. The hub closes the server's connection.
func (a *App) forgetKey() {
	ed := a.editor
	if ed == nil || ed.busy {
		return
	}
	ed.busy, ed.err = true, ""
	gen := a.hubGen
	go func() {
		err := a.hub().ForgetHostKey(context.Background(), ed.name)
		a.Update(func() {
			if gen != a.hubGen {
				return
			}
			ed.busy = false
			if err != nil {
				ed.err = err.Error()
				return
			}
			a.reloadServers()
		})
	}()
}

// deleteState is the delete confirm for name, shown at at: its Delete
// waits AllowDelay, so a double click on the card cannot confirm it.
type deleteState struct {
	name string
	at   time.Time
}

func (a *App) askDelete(name string) { a.del = &deleteState{name: name, at: a.Now()} }

// buildDeleteConfirm is Electron's window.confirm before servers.delete:
// Cancel has the focus, so Enter and Esc keep the host.
func (a *App) buildDeleteConfirm(c *ui.Context) {
	del := a.del
	if del == nil {
		return
	}
	now := a.Now()
	ok := model.TrustEnabled(del.at, now)
	if !ok {
		c.After(del.at.Add(model.AllowDelay).Sub(now))
	}
	answer := 0 // 1 delete, 2 cancel
	open := true
	m := ui.Modal(c.Key("del-"+del.name), &open, func() {
		ui.Column(c).Width(420).Gap(12).Padding(20).Children(func() {
			ui.Text(c, "Delete "+model.DisplayText(del.name)+"? Its saved passwords and host key are removed and its open tabs close.")
			// Cancel first, as the mismatch dialog's Close: the safe button
			// takes a focus that lands in the modal from outside.
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").AutoFocus().Clicked() {
					answer = 2
				}
				if ui.Button(c, "Delete host").Disabled(!ok).Clicked() && model.TrustEnabled(del.at, a.Now()) {
					answer = 1
				}
			})
		})
	}).Label("Confirm delete")
	if m.Dismissed() {
		answer = 2
	}
	if answer == 0 {
		return
	}
	a.del = nil
	if answer == 1 {
		a.deleteHost(del.name)
	}
	a.searchH.Focus()
}

// deleteHost sends servers.delete; a refusal shows in the alert row, and
// the host list is read again either way.
func (a *App) deleteHost(name string) {
	gen := a.hubGen
	go func() {
		err := a.hub().DeleteServer(context.Background(), name)
		a.Update(func() {
			if gen != a.hubGen {
				return
			}
			if err != nil {
				a.alert = err.Error()
			}
			a.reloadServers()
		})
	}()
}
```

In `app/view/app.go`, add to the `App` struct after `editor`:

```go
	del                    *deleteState
```

In `app/view/home.go`, at the end of `buildReady`, after `a.buildHostKeyDialogs(c)`:

```go
	a.buildDeleteConfirm(c)
```

and in `buildHosts`, after the card's `Edit` button:

```go
						if ui.Button(c, "Delete").Label("Delete " + shownName).Tooltip("Delete").Clicked() {
							a.askDelete(name)
						}
```

In `app/view/hostkey.go`, replace the comment and first line of `buildMismatch`'s body

```go
// buildMismatch shows a pinned key the server no longer presents. There is
// no way to pin the new key here: the host editor's Forget (slice 3) is.
func (a *App) buildMismatch(c *ui.Context, k hubapi.HostKey) {
	t := c.Theme()
	closed := false
```

with

```go
// buildMismatch shows a pinned key the server no longer presents. There is
// no way to pin the new key here: the host editor's Forget is, which Open
// host editor opens with the focus on it.
func (a *App) buildMismatch(c *ui.Context, k hubapi.HostKey) {
	t := c.Theme()
	closed, edit := false, false
```

replace its button row

```go
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.Button(c, "Close").AutoFocus().Clicked() {
					closed = true
				}
			})
```

with

```go
			// Close first: when a focus request from outside lands in the
			// modal (a tab activated behind it), mygo gives the focus to the
			// first button, and that must be the safe one.
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Close").AutoFocus().Clicked() {
					closed = true
				}
				if ui.Button(c, "Open host editor").Clicked() {
					edit = true
				}
			})
		})
```

and after `}).Label("Host key mismatch")` insert:

```go
	if edit {
		a.mismatch = nil
		a.openEditor(k.Server, true)
		return
	}
```

- [ ] **Step 4: Run the tests**

Run: `cd app && go test -short ./view -count=1 && MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -short ./view -count=1 -run 'Mismatch|Dialogs|Forget|Delete' && go vet ./view && gofmt -l view`
Expected: `ok` twice, no gofmt output. With the library, slice 2's `TestDialogsGiveFocusBackToTheTerminal` runs and passes only with `Close` before `Open host editor` (Decision 5).

Check that they bite: in `buildDeleteConfirm`, replace both `model.TrustEnabled(del.at, now)` and `model.TrustEnabled(del.at, a.Now())` with `true`: `TestDeleteWaitsHalfASecondThenDeletes` must FAIL at "Delete worked before 500 ms" (restore them).

- [ ] **Step 5: Commit**

```bash
git add app/view/editor.go app/view/app.go app/view/home.go app/view/hostkey.go app/view/hostwrites_test.go app/view/hostkey_test.go
git commit -m "app: Forget host key, Delete with confirm, Open host editor from a mismatch

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: `view`: Import from SSH config

Ports `desktop/src/renderer/ImportSheet.tsx` and its wiring (`App.tsx:281-284, 318-321`) with the `ImportSheet.test.ts` cases (renderer.md §3; protocol.md §1.7; mygo.md §5).

**Files:**
- Create: `app/view/importsheet.go`
- Modify: `app/view/app.go` (the `App` struct), `app/view/editor.go` (`openEditor`, `dropDialogs`), `app/view/home.go` (`buildReady`, `buildHosts`), `app/view/app_test.go` (`fakeDoor`)
- Test: `app/view/importsheet_test.go` (create)

**Interfaces:**
- Consumes: `hubapi.Client.ImportScan`, `ImportApply`, `hubapi.ImportScan`, `hubapi.Candidate`, `hubapi.ImportResult` (Task 1); `model.NextChecked` (Task 2); `closeSheet`, `dropDialogs`, `openEditor`, `sheetSeq`, `edReady`, `clickT`, `count`, `hasCall` (Task 3); `params` (Task 4).
- Produces (package `view`): `type importState struct { seq int; scan *hubapi.ImportScan; checked map[string]bool; busy bool; err string }`, `App.imp *importState`, `func (a *App) openImport()`, `func (a *App) scanImport(rescan bool)`, `func (imp *importState) importAliases() []string`, `func (a *App) applyImport()`, `func (a *App) buildImport(c *ui.Context)`, `func (a *App) buildImportRow(c *ui.Context, cand hubapi.Candidate)`; `fakeDoor.scan hubapi.ImportScan`, `fakeDoor.imported hubapi.ImportResult`.

- [ ] **Step 1: Write the failing tests**

In `app/view/app_test.go`, add to `fakeDoor` after `hang`:

```go
	scan     hubapi.ImportScan
	imported hubapi.ImportResult
```

in `Call`, after `srv, pend := d.servers, d.pending`:

```go
	scan, imported := d.scan, d.imported
```

and in its `switch method`, after the `"pending"` case:

```go
	case "import.scan":
		out = scan
	case "import.apply":
		out = imported
```

`app/view/importsheet_test.go`:

```go
package view

import (
	"errors"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
)

// ImportSheet.test.ts's rows: web ready and pinned, db ready without a pin
// and with a passphrase, jump skipped, box already a server.
var scanRows = hubapi.ImportScan{Candidates: []hubapi.Candidate{
	{Alias: "web", Host: "10.0.0.5", Port: 2200, User: "deploy", Auth: "key", KeyPath: "~/.ssh/id_web", HostKey: "SHA256:abc", HostKeyAlgo: "ssh-ed25519", Status: "ready"},
	{Alias: "db", Host: "10.0.0.6", Port: 22, User: "pg", Auth: "agent", NeedsPassphrase: true, Status: "ready"},
	{Alias: "jump", Host: "jump", Port: 22, User: "u", Auth: "agent", Status: "skipped", Reason: "needs ProxyJump"},
	{Alias: "box", Host: "h", Port: 22, User: "u", Auth: "agent", Status: "exists", Reason: "Already in vault"},
}}

func importApp(t *testing.T, scan hubapi.ImportScan) (*App, *tester, *fakeDoor) {
	a, tt, d := edReady(t, []hubapi.Server{boxServer})
	d.set(func() { d.scan = scan })
	d.resetCalls()
	clickT(t, tt, "Import from SSH config")
	tt.flush()
	return a, tt, d
}

func TestImportRowsShowTheScan(t *testing.T) {
	a, tt, _ := importApp(t, scanRows)
	for _, s := range []string{"deploy@10.0.0.5:2200 · ~/.ssh/id_web", "ssh-ed25519 SHA256:abc", "pg@10.0.0.6:22 · agent", "Key has a passphrase: add it in the editor after import.",
		"needs ProxyJump", "Skipped", "Already in vault", "Import web", "Import db", "Import 2 hosts"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	if count(tt.Texts(), "not in known_hosts") != 1 || tt.HasText("Import jump") || tt.HasText("Import box") {
		t.Fatalf("only db lacks a pin, and only ready rows have a checkbox: %q", tt.Texts())
	}
	if !reflect.DeepEqual(a.imp.checked, map[string]bool{"web": true, "db": true}) || !tt.Focused("Close") {
		t.Fatalf("checked %v", a.imp.checked)
	}
}

func TestImportSendsCheckedReadyAliasesOnly(t *testing.T) {
	a, tt, d := importApp(t, scanRows)
	clickT(t, tt, "Import db")
	clickT(t, tt, "jump") // a row without a checkbox: nothing to check
	if !tt.HasText("Import 1 host") {
		t.Fatal(tt.Texts())
	}
	d.resetCalls()
	clickT(t, tt, "Import 1 host")
	tt.flush()
	if got := d.callList(); len(got) != 2 || got[0] != "import.apply" || got[1] != "servers" || params(d, "import.apply") != `{"aliases":["web"]}` {
		t.Fatalf("calls %v %s", got, params(d, "import.apply"))
	}
	if a.imp != nil || !tt.Focused("Search hosts") {
		t.Fatal("the sheet stays after a full import, or the focus went nowhere")
	}
}

func TestImportBeforeTheScanAnswers(t *testing.T) {
	a, tt, d := edReady(t, []hubapi.Server{boxServer})
	d.set(func() { d.hang = map[string]bool{"import.scan": true} })
	clickT(t, tt, "Import from SSH config")
	for _, s := range []string{"Close import", "StrictHostKeyChecking accept-new", "Reading your SSH config…", "Import 0 hosts"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	clickT(t, tt, "Import 0 hosts")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if hasCall(d, "import.apply") || a.imp == nil {
		t.Fatal("Import ran with nothing scanned, or Esc closed a busy sheet")
	}
}

func TestImportSkippedRescansKeepingTheChoice(t *testing.T) {
	a, tt, d := importApp(t, scanRows)
	clickT(t, tt, "Import db")
	d.set(func() {
		d.imported = hubapi.ImportResult{Imported: []string{}, Skipped: []hubapi.ImportSkip{{Alias: "web", Reason: "Already in vault"}}}
	})
	clickT(t, tt, "Import 1 host")
	tt.flush()
	if !tt.HasText("Not imported: web (Already in vault)") || count(d.callList(), "import.scan") != 2 {
		t.Fatalf("%q %v", tt.Texts(), d.callList())
	}
	if !reflect.DeepEqual(a.imp.checked, map[string]bool{"web": true}) {
		t.Fatal("the rescan lost the choice:", a.imp.checked)
	}
}

func TestImportNoteAndScanError(t *testing.T) {
	_, tt, d := importApp(t, hubapi.ImportScan{Note: "No ~/.ssh/config"})
	if !tt.HasText("No ~/.ssh/config") || !tt.HasText("Import 0 hosts") {
		t.Fatal(tt.Texts())
	}
	clickT(t, tt, "Close")
	d.set(func() { d.errs = map[string]error{"import.scan": errors.New("OpenSSH client (ssh) not found")} })
	clickT(t, tt, "Import from SSH config")
	tt.flush()
	if !tt.HasText("OpenSSH client (ssh) not found") {
		t.Fatal(tt.Texts())
	}
}

func TestImportAndEditorTakeEachOthersPlace(t *testing.T) {
	a, tt, _ := importApp(t, scanRows)
	clickT(t, tt, "New host")
	if a.imp != nil || a.editor == nil {
		t.Fatal("New host left the import sheet open")
	}
	clickT(t, tt, "Import from SSH config")
	tt.flush()
	if a.editor != nil || a.imp == nil {
		t.Fatal("Import left the editor open")
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `cd app && go test -short ./view -count=1`
Expected: FAIL to compile: `a.imp undefined (type *App has no field or method imp)`.

- [ ] **Step 3: Implement**

`app/view/importsheet.go`:

```go
package view

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// importState is the open import sheet (Electron's ImportSheet.tsx). scan
// is nil until the first import.scan answers.
type importState struct {
	seq     int
	scan    *hubapi.ImportScan
	checked map[string]bool // aliases; only ready ones are ever set
	busy    bool
	err     string
}

// openImport shows the import sheet in place of the editor and scans.
func (a *App) openImport() {
	a.sheetSeq++
	a.editor = nil
	a.imp = &importState{seq: a.sheetSeq}
	a.scanImport(false)
}

// scanImport runs import.scan; a rescan keeps the person's checks on rows
// still ready. A reply for a closed sheet or a stopped hub is dropped.
func (a *App) scanImport(rescan bool) {
	imp, gen := a.imp, a.hubGen
	imp.busy = true
	go func() {
		s, err := a.hub().ImportScan(context.Background())
		a.Update(func() {
			if gen != a.hubGen || a.imp != imp {
				return
			}
			imp.busy = false
			if err != nil {
				imp.err = err.Error()
				return
			}
			var prev map[string]bool
			if rescan {
				prev = imp.checked
			}
			imp.scan, imp.checked = &s, model.NextChecked(s.Candidates, prev)
		})
	}()
}

// importAliases are the checked rows that are ready, in the scan's order:
// alias names are all import.apply gets.
func (imp *importState) importAliases() []string {
	var out []string
	if imp.scan == nil {
		return out
	}
	for _, c := range imp.scan.Candidates {
		if c.Status == "ready" && imp.checked[c.Alias] {
			out = append(out, c.Alias)
		}
	}
	return out
}

func (a *App) applyImport() {
	imp, gen := a.imp, a.hubGen
	names := imp.importAliases()
	if imp.busy || len(names) == 0 {
		return
	}
	imp.busy, imp.err = true, ""
	go func() {
		r, err := a.hub().ImportApply(context.Background(), names)
		a.Update(func() {
			if gen != a.hubGen {
				return
			}
			imp.busy = false
			if err != nil {
				imp.err = err.Error()
				return
			}
			a.reloadServers()
			if a.imp != imp {
				return
			}
			if len(r.Skipped) == 0 {
				a.closeSheet()
				return
			}
			parts := make([]string, len(r.Skipped))
			for i, s := range r.Skipped {
				parts[i] = fmt.Sprintf("%s (%s)", s.Alias, s.Reason)
			}
			imp.err = "Not imported: " + strings.Join(parts, "; ")
			a.scanImport(true)
		})
	}()
}

func (a *App) buildImport(c *ui.Context) {
	t := c.Theme()
	imp := a.imp
	n := len(imp.importAliases())
	closed, submit := false, false
	sheet := ui.Column(c.Key("import-"+strconv.Itoa(imp.seq))).Fill().Width(420).Background(t.Surface).BorderWidth(0, 0, 0, 1).BorderColor(t.Border).
		Role(ui.RoleDialog).Label("Import from SSH config")
	sheet.Children(func() {
		ui.Row(c).Gap(8).Padding(12, 16).Children(func() {
			ui.Text(c, "Import from SSH config").Bold().FontSize(16).Grow(1)
			if ui.Button(c, "×").Label("Close import").Tooltip("Close").Clicked() {
				closed = true
			}
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(12).Padding(8, 16).Children(func() {
				ui.RichText(c,
					ui.Span{Text: "Host keys come from your known_hosts. With "},
					ui.Span{Text: "StrictHostKeyChecking accept-new", Font: "monospace"},
					ui.Span{Text: ", OpenSSH accepted them without asking you; compare them with the server if unsure."}).TextColor(t.TextMuted)
				switch {
				case imp.scan == nil && imp.busy:
					ui.Text(c, "Reading your SSH config…").TextColor(t.TextMuted)
				case imp.scan != nil:
					if imp.scan.Note != "" {
						ui.Text(c, model.DisplayText(imp.scan.Note)).TextColor(t.TextMuted)
					}
					for _, cand := range imp.scan.Candidates {
						a.buildImportRow(c, cand)
					}
				}
			})
		})
		ui.Row(c).Gap(8).Padding(12, 16).Justify(ui.End).Children(func() {
			if imp.err != "" {
				ui.Text(c, model.DisplayText(imp.err)).TextColor(t.Danger).Grow(1)
			}
			if ui.Button(c, "Close").AutoFocus().Clicked() {
				closed = true
			}
			label := fmt.Sprintf("Import %d hosts", n)
			if n == 1 {
				label = "Import 1 host"
			}
			if ui.PrimaryButton(c, label).Disabled(imp.busy || n == 0).Clicked() {
				submit = true
			}
		})
	})
	if sheet.Shortcut(0, ui.KeyEscape) && !imp.busy {
		closed = true
	}
	switch {
	case closed:
		a.closeSheet()
	case submit:
		a.applyImport()
	}
}

// buildImportRow is one alias; only a ready one has a checkbox. Every text
// here comes from ~/.ssh/config and known_hosts, so it is display text.
func (a *App) buildImportRow(c *ui.Context, cand hubapi.Candidate) {
	t := c.Theme()
	imp := a.imp
	alias := model.DisplayText(cand.Alias)
	ui.Column(c.Key("cand-"+cand.Alias)).Gap(4).Padding(8, 0).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			if cand.Status == "ready" {
				on := imp.checked[cand.Alias]
				if ui.Checkbox(c, &on, alias).Label("Import " + alias).Disabled(imp.busy).Changed() {
					imp.checked[cand.Alias] = on
				}
			} else {
				ui.Text(c, alias).Bold()
			}
			status, col := map[string]string{"ready": "Ready", "exists": "Already in vault"}[cand.Status], t.Warning
			if status == "" {
				status = "Skipped"
			}
			if cand.Status == "ready" {
				col = t.Success
			}
			ui.Text(c, status).TextColor(col)
		})
		if cand.Status == "skipped" {
			ui.Text(c, model.DisplayText(cand.Reason)).TextColor(t.TextMuted)
		} else {
			key := "agent"
			if cand.Auth == "key" {
				key = cand.KeyPath
			}
			ui.Text(c, model.DisplayText(fmt.Sprintf("%s@%s:%d · %s", cand.User, cand.Host, cand.Port, key))).Font("monospace").TextColor(t.TextMuted)
		}
		if cand.Status != "ready" {
			return
		}
		pin := "not in known_hosts"
		if cand.HostKey != "" {
			pin = cand.HostKeyAlgo + " " + cand.HostKey
		}
		ui.Text(c, model.DisplayText(pin)).Font("monospace").Selectable()
		if cand.NeedsPassphrase {
			ui.Text(c, "Key has a passphrase: add it in the editor after import.").TextColor(t.TextMuted)
		}
	})
}
```

In `app/view/app.go`, add to the `App` struct after `editor`:

```go
	imp                    *importState
```

In `app/view/editor.go`, in `openEditor`, after `a.sheetSeq++`:

```go
	a.imp = nil
```

and replace `dropDialogs` with

```go
func (a *App) dropDialogs() { a.editor, a.imp, a.del = nil, nil, nil }
```

In `app/view/home.go` `buildReady`, replace

```go
			if a.editor != nil {
				a.buildEditor(c)
			}
```

with

```go
			switch {
			case a.editor != nil:
				a.buildEditor(c)
			case a.imp != nil:
				a.buildImport(c)
			}
```

and in `buildHosts`, after the `New host` button:

```go
			if ui.Button(c, "Import from SSH config").Clicked() {
				a.openImport()
			}
```

- [ ] **Step 4: Run the tests**

Run: `cd app && go test -short ./view -count=1 && go vet ./view && gofmt -l view`
Expected: `ok  github.com/lang315/sshgate/app/view`, no gofmt output.

Check that they bite: in `buildImportRow` replace `if cand.Status == "ready" {` with `if true {`, and in `importAliases` replace `if c.Status == "ready" && imp.checked[c.Alias] {` with `if imp.checked[c.Alias] {`: `TestImportSendsCheckedReadyAliasesOnly` must FAIL, the click on `jump` now checking it (`Import 2 hosts`); restore both.

- [ ] **Step 5: Commit**

```bash
git add app/view/importsheet.go app/view/importsheet_test.go app/view/app.go app/view/app_test.go app/view/editor.go app/view/home.go
git commit -m "app: Import from SSH config sheet (alias names only, ready rows checked)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: the theme control, kept across launches

Ports `desktop/src/renderer/ThemeControl.tsx` and `theme.ts`'s `parsePref`, `loadPref`, `savePref` with their vitest cases (renderer.md §4.3; mygo.md §4). `resolveTheme` and `xtermTheme` are not ported (Decision 7).

**Files:**
- Create: `app/view/theme.go`, `app/cmd/sshgate-app/theme_test.go`
- Modify: `app/view/shell.go`, `app/view/app.go` (the `App` struct, `noShell`, `init`), `app/view/home.go` (`buildReady`), `app/view/app_test.go` (`fakeShell`), `app/cmd/sshgate-app/tray.go`, `app/cmd/sshgate-app/main.go`
- Test: `app/view/theme_test.go` (create), `app/cmd/sshgate-app/theme_test.go`

**Interfaces:**
- Consumes: `App.shell()`, `Shell`, `noShell`, `trayShell` (slice 1); `clickT` (Task 3).
- Produces:
  - package `view`: `Shell.SetTheme(pref string)`; `App.ThemeFile string` (exported), `App.theme string`; `func LoadTheme(path string) string` (exported, `main` uses it); `func parseTheme(v string) string`, `func saveTheme(path, pref string) error`, `func (a *App) setTheme(pref string)`, `func (a *App) buildThemeControl(c *ui.Context)`; `fakeShell.themes []string`.
  - package `main`: `func (s *trayShell) SetTheme(pref string)`, `func themeSource(pref string) mygo.ThemeSource`.

- [ ] **Step 1: Write the failing tests**

In `app/view/app_test.go`, add a `themes` field to `fakeShell` and its method:

```go
type fakeShell struct {
	mu      sync.Mutex
	pending []int
	notes   []string
	themes  []string
}

func (s *fakeShell) SetTheme(pref string) {
	s.mu.Lock()
	s.themes = append(s.themes, pref)
	s.mu.Unlock()
}
```

(this replaces the `fakeShell` struct with the struct and its new `SetTheme` method; `SetPending` and `Notify` stay as they are, below it).

`app/view/theme_test.go`:

```go
package view

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lang315/sshgate/app/hubapi"
)

func TestParseThemeAndItsFile(t *testing.T) {
	for in, want := range map[string]string{"dark": "dark", "light": "light", "auto": "auto", "": "auto", "purple": "auto"} {
		if got := parseTheme(in); got != want {
			t.Errorf("parseTheme(%q) = %q", in, got)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "theme.json")
	if LoadTheme("") != "auto" || LoadTheme(path) != "auto" {
		t.Fatal("no file must read as auto")
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if LoadTheme(path) != "auto" {
		t.Fatal("a broken file must read as auto")
	}
	if err := saveTheme(filepath.Join(dir, "missing", "theme.json"), "dark"); err == nil {
		t.Fatal("a write into a missing folder should fail, quietly")
	}
	if err := saveTheme(path, "light"); err != nil || LoadTheme(path) != "light" {
		t.Fatal("round trip:", err)
	}
	if b, _ := os.ReadFile(path); string(b) != `{"theme":"light"}` {
		t.Fatalf("%s", b)
	}
}

func TestThemeControlAppliesKeepsAndCallsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	d := &fakeDoor{status: hubapi.Status{HasVault: true}}
	a := &App{Door: d, ThemeFile: path}
	c := &clock{}
	a.Now = c.now
	sh := &fakeShell{}
	a.Shell = sh
	tt := newTester(t, a)
	running(a, tt)
	for _, s := range []string{"Theme", "Dark", "☾", "Light", "☀", "Auto"} {
		if !tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, tt.Texts())
		}
	}
	if a.theme != "auto" {
		t.Fatal(a.theme)
	}
	d.resetCalls()
	clickT(t, tt, "Light")
	tt.flush()
	if a.theme != "light" || !reflect.DeepEqual(sh.themes, []string{"light"}) || LoadTheme(path) != "light" {
		t.Fatalf("theme %q, applied %v, kept %q", a.theme, sh.themes, LoadTheme(path))
	}
	if got := d.callList(); len(got) != 0 {
		t.Fatalf("the theme control called the hub: %v", got)
	}

	// The next launch starts in it; main has applied it, so the view does not again.
	sh2 := &fakeShell{}
	b := &App{Door: d, ThemeFile: path, Now: c.now, Shell: sh2}
	tt2 := newTester(t, b)
	running(b, tt2)
	if b.theme != "light" || len(sh2.themes) != 0 {
		t.Fatalf("theme %q, applied %v", b.theme, sh2.themes)
	}
}
```

`app/cmd/sshgate-app/theme_test.go`:

```go
package main

import (
	"testing"

	"github.com/egoist/mygo"
)

func TestThemeSource(t *testing.T) {
	for pref, want := range map[string]mygo.ThemeSource{"dark": mygo.ThemeDark, "light": mygo.ThemeLight, "auto": mygo.ThemeSystem, "": mygo.ThemeSystem} {
		if got := themeSource(pref); got != want {
			t.Errorf("themeSource(%q) = %q, want %q", pref, got, want)
		}
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `cd app && go test -short ./view ./cmd/sshgate-app -count=1`
Expected: FAIL to compile: `undefined: parseTheme` in `view`, `undefined: themeSource` in `main`.

- [ ] **Step 3: Implement**

`app/view/theme.go`:

```go
package view

import (
	"encoding/json"
	"os"

	"github.com/egoist/mygo/ui"
)

// themes is the theme control, in Electron's order (ThemeControl.tsx): each
// segment is named and tipped with its name and shows its glyph.
var themes = []struct{ pref, name, glyph string }{
	{"dark", "Dark", "☾"},
	{"light", "Light", "☀"},
	{"auto", "Auto", "Auto"},
}

// parseTheme is Electron's parsePref: anything but dark or light is auto.
func parseTheme(v string) string {
	if v == "dark" || v == "light" {
		return v
	}
	return "auto"
}

// LoadTheme reads the theme kept at path. A display preference, not
// security state: any failure, path "" included, means auto.
func LoadTheme(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "auto"
	}
	var p struct {
		Theme string `json:"theme"`
	}
	if json.Unmarshal(b, &p) != nil {
		return "auto"
	}
	return parseTheme(p.Theme)
}

// saveTheme keeps pref at path ("" keeps nothing), replacing the file whole.
func saveTheme(path, pref string) error {
	if path == "" {
		return nil
	}
	b, _ := json.Marshal(map[string]string{"theme": parseTheme(pref)})
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// setTheme applies a choice app-wide and keeps it for the next launch; a
// failed write only loses the choice, as in Electron.
func (a *App) setTheme(pref string) {
	a.theme = pref
	_ = saveTheme(a.ThemeFile, pref)
	a.shell().SetTheme(pref)
}

// buildThemeControl is the Dark / Light / Auto switch of the top row. It
// calls no hub method.
func (a *App) buildThemeControl(c *ui.Context) {
	t := c.Theme()
	sel := len(themes) - 1
	for i, th := range themes {
		if th.pref == a.theme {
			sel = i
		}
	}
	p := ui.SegmentedBase(c, &sel, len(themes))
	p.Track.Label("Theme").Children(func() {
		for i, th := range themes {
			seg := p.Segment(i).Label(th.name).Tooltip(th.name).Padding(4, 8).Radius(6)
			if i == sel {
				seg.Background(t.Accent.Alpha(0.2))
			}
			seg.Children(func() { ui.Text(c, th.glyph) })
		}
	})
	if p.Track.Changed() {
		a.setTheme(themes[sel].pref)
	}
}
```

`app/view/shell.go`, the whole file:

```go
package view

// Shell is what the app shows outside its window: the tray's pending
// count, a notification per AI request, and the app-wide theme ("dark",
// "light" or "auto"). main implements it with mygo; tests fake it.
type Shell interface {
	SetPending(n int)
	Notify(id, title, body string)
	SetTheme(pref string)
}
```

In `app/view/app.go`, add to the `App` struct after `Shell Shell`:

```go
	// ThemeFile keeps the theme choice across launches; "" keeps it in memory.
	ThemeFile string
```

after `searchH`:

```go
	theme                  string    // "dark", "light" or "auto"
```

to `noShell`:

```go
func (noShell) SetTheme(string)               {}
```

and in `init`, after `a.terms = map[string]*termTab{}`:

```go
		a.theme = LoadTheme(a.ThemeFile) // main applied it before the window opened
```

In `app/view/home.go` `buildReady`, before `lock := ui.Button(c, "Lock")`:

```go
			a.buildThemeControl(c)
```

Append to `app/cmd/sshgate-app/tray.go`:

```go

// SetTheme applies the theme control's choice app-wide, native chrome
// included; Auto follows the system.
func (s *trayShell) SetTheme(pref string) { mygo.Theme.SetSource(themeSource(pref)) }

func themeSource(pref string) mygo.ThemeSource {
	switch pref {
	case "dark":
		return mygo.ThemeDark
	case "light":
		return mygo.ThemeLight
	}
	return mygo.ThemeSystem
}
```

In `app/cmd/sshgate-app/main.go`, replace

```go
	mux := termconn.NewMux()
	var client atomic.Pointer[door.Client]
	a := &view.App{Now: time.Now}
```

with

```go
	// The theme choice lives beside mygo's own window-state.json; no
	// SSHGATE_* knob moves it. It applies before the window opens.
	themeFile := ""
	if dir, err := mygo.App.Path(mygo.PathUserData); err == nil {
		themeFile = filepath.Join(dir, "theme.json")
	}
	mygo.Theme.SetSource(themeSource(view.LoadTheme(themeFile)))

	mux := termconn.NewMux()
	var client atomic.Pointer[door.Client]
	a := &view.App{Now: time.Now, ThemeFile: themeFile}
```

- [ ] **Step 4: Run the tests**

Run, inside `app/`:

```bash
go test -short ./view ./cmd/sshgate-app -count=1 && go vet ./... && gofmt -l .
GOOS=linux go vet ./... && GOOS=windows go vet ./...
```

Expected: `ok` for both packages; no vet or gofmt output. `mygo.Theme.SetSource` needs a running app, so the native appearance is checked by hand (Manual check).

Check that they bite: in `buildThemeControl`, replace `a.setTheme(themes[sel].pref)` with `a.theme = themes[sel].pref`: `TestThemeControlAppliesKeepsAndCallsNothing` must FAIL (nothing applied or kept); restore it.

- [ ] **Step 5: Commit**

```bash
git add app/view/theme.go app/view/theme_test.go app/view/shell.go app/view/app.go app/view/app_test.go app/view/home.go app/cmd/sshgate-app/tray.go app/cmd/sshgate-app/main.go app/cmd/sshgate-app/theme_test.go
git commit -m "app: Dark / Light / Auto theme control, kept in theme.json

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Flow test against a real hub: hosts (and `hubtest.Start`)

Ports `desktop/e2e/hosts.spec.ts` step by step, then a delete, against a real hub on an empty store; every host write is checked in `audit.jsonl` (renderer.md §6.1; protocol.md §1.1-§1.3). `hubtest` gains what the empty-store and import flows need (protocol.md §2.4, §3).

**Files:**
- Modify: `app/internal/hubtest/hubtest.go` (imports; `SSHD`), `app/view/flow_unix_test.go` (`flowT`, `flow`, `auditedConfig`)
- Create: `app/internal/hubtest/hubtest_test.go`, `app/view/flowhosts_unix_test.go`

**Interfaces:**
- Consumes: Tasks 3-4 (`App.editor`, the editor's and the confirm's texts); slice 2's `needLib`, `tabState`, `flowT.until/click/pass/shows`, `hubtest.Binaries`, `hubtest.Runtime`.
- Produces:
  - package `hubtest`: `type Options struct { EmptyStore bool; KnownHosts string }`, `type Server struct { Store string; Port int }`, `func Start(t *testing.T, o Options) Server`; `SSHD(t)` is now `Start(t, Options{}).Store`.
  - package `view` (tests): `flowT.port int`, `func flowWith(t *testing.T, o hubtest.Options, extraArgs ...string) *flowT`, `func (f *flowT) configRecords(server, action string, n int)` (waits for exactly n), `func (f *flowT) fill(label, text string)`, `createVault()`, `trust()`, `editBox()`, `saveEditor()`.

- [ ] **Step 1: Write the failing tests**

`app/internal/hubtest/hubtest_test.go`:

```go
package hubtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lang315/sshgate/app/internal/hubtest"
)

func TestStartWritesWhatItIsAskedTo(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	kh := filepath.Join(t.TempDir(), "known_hosts")
	s := hubtest.Start(t, hubtest.Options{EmptyStore: true, KnownHosts: kh})
	if _, err := os.Stat(s.Store); !os.IsNotExist(err) {
		t.Fatalf("an empty store was written: %v", err)
	}
	b, err := os.ReadFile(kh)
	if err != nil || !strings.HasPrefix(string(b), fmt.Sprintf("[127.0.0.1]:%d ssh-ed25519 ", s.Port)) {
		t.Fatalf("known_hosts %q %v", b, err)
	}
	if v := hubtest.Start(t, hubtest.Options{}); v.Port == 0 {
		t.Fatal("no port")
	} else if _, err := os.Stat(v.Store); err != nil {
		t.Fatal("no vault:", err)
	}
}
```

In `app/view/flow_unix_test.go`, add to `flowT` after `writes`:

```go
	port   int // sshtestd's
```

replace the head of `flow`

```go
func flow(t *testing.T, extraArgs ...string) *flowT {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	store := hubtest.SSHD(t)
	bin, _ := hubtest.Binaries(t)
```

with

```go
func flow(t *testing.T, extraArgs ...string) *flowT {
	t.Helper()
	return flowWith(t, hubtest.Options{}, extraArgs...)
}

// flowWith is flow on an sshtestd started with o.
func flowWith(t *testing.T, o hubtest.Options, extraArgs ...string) *flowT {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	srv := hubtest.Start(t, o)
	store := srv.Store
	bin, _ := hubtest.Binaries(t)
```

replace the last line of `flowWith` (the old `flow`)

```go
	return &flowT{t: t, a: a, tt: tt, c: c, sock: hubtest.MCPSocket(dir), h: hubapi.New(p.Client()), audit: filepath.Join(filepath.Dir(store), "audit.jsonl"), writes: writes}
```

with

```go
	return &flowT{t: t, a: a, tt: tt, c: c, sock: hubtest.MCPSocket(dir), h: hubapi.New(p.Client()), audit: filepath.Join(filepath.Dir(store), "audit.jsonl"), writes: writes, port: srv.Port}
```

and replace `auditedConfig`

```go
// auditedConfig waits for box's config record with this action.
func (f *flowT) auditedConfig(action string) {
	f.t.Helper()
	f.until(func() bool {
		file, err := os.Open(f.audit)
		if err != nil {
			return false
		}
		defer file.Close()
		for sc := bufio.NewScanner(file); sc.Scan(); {
			var r auditRec
			if json.Unmarshal(sc.Bytes(), &r) == nil && r.Kind == "config" && r.Action == action && r.Server == "box" {
				return true
			}
		}
		return false
	}, "box's "+action+" config record")
}
```

with

```go
// auditedConfig waits for box's config record with this action.
func (f *flowT) auditedConfig(action string) {
	f.t.Helper()
	f.configRecords("box", action, 1)
}

// configRecords waits until server has n config records with this action.
func (f *flowT) configRecords(server, action string, n int) {
	f.t.Helper()
	f.until(func() bool {
		file, err := os.Open(f.audit)
		if err != nil {
			return false
		}
		defer file.Close()
		got := 0
		for sc := bufio.NewScanner(file); sc.Scan(); {
			var r auditRec
			if json.Unmarshal(sc.Bytes(), &r) == nil && r.Kind == "config" && r.Action == action && r.Server == server {
				got++
			}
		}
		return got == n
	}, fmt.Sprintf("%d %s config records of %s", n, action, server))
}
```

`app/view/flowhosts_unix_test.go`:

```go
//go:build unix

package view

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/internal/hubtest"
	"github.com/lang315/sshgate/app/model"
)

// Ports of desktop/e2e/hosts.spec.ts, import.spec.ts and the editor part of
// keyboard.spec.ts.

// fill replaces a field's text, as the e2e's fill does.
func (f *flowT) fill(label, text string) {
	f.t.Helper()
	f.click(label)
	f.tt.Key(ui.Cmd, ui.KeyA)
	f.tt.Type(text)
	f.tt.Frame()
}

func (f *flowT) createVault() {
	f.t.Helper()
	f.until(func() bool { return f.tt.HasText("New master password") }, "Create vault")
	f.click("New master password")
	f.tt.Type("password1")
	f.click("Confirm master password")
	f.tt.Type("password1")
	f.click("Create vault")
	f.until(func() bool { return f.a.Screen() == model.ScreenReady && f.tt.HasText("Vault file:") }, "the ready screen")
}

// trust answers the unknown-key prompt of box once Trust is enabled.
func (f *flowT) trust() {
	f.t.Helper()
	f.until(func() bool { return f.tt.HasText("Trust this host key?") }, "the host-key prompt")
	for _, s := range []string{"SHA256:", fmt.Sprintf("test@127.0.0.1:%d", f.port)} {
		if !f.tt.HasText(s) {
			f.t.Fatalf("missing %q in %q", s, f.tt.Texts())
		}
	}
	f.pass()
	f.click("Trust and connect")
	f.until(func() bool { return !f.tt.HasText("Trust this host key?") }, "the prompt to go")
}

func (f *flowT) editBox() {
	f.t.Helper()
	f.click("Hosts")
	f.click("Edit box")
	f.until(func() bool { return f.a.editor != nil }, "the host editor")
}

func (f *flowT) saveEditor() {
	f.t.Helper()
	f.click("Save")
	f.until(func() bool { return f.a.editor == nil }, "the editor to close after Save")
}

// TestFlowHostsCreateTrustEditForgetDelete: hosts.spec.ts in order, then a
// delete; every host write leaves its config record in the audit log.
func TestFlowHostsCreateTrustEditForgetDelete(t *testing.T) {
	needLib(t)
	f := flowWith(t, hubtest.Options{EmptyStore: true})
	f.tt.SetSize(1000, 900) // the whole editor in view: the Tester clicks where an element is
	f.createVault()

	// 2. Add a host pointing at sshtestd.
	f.click("New host")
	f.fill("Label", "box")
	f.fill("Address", "127.0.0.1")
	f.fill("Port", strconv.Itoa(f.port))
	f.fill("User", "test")
	f.fill("Password", "testpass")
	f.saveEditor()
	f.until(func() bool { s := f.a.Servers(); return len(s) == 1 && s[0].Name == "box" && s[0].HasPassword }, "box in the host list")
	f.configRecords("box", "save", 1)

	// 3. Connect: the prompt shows the fingerprint; Trust gives a shell.
	f.click("Open box")
	f.trust()
	first := f.a.tabs.Active
	f.until(func() bool { return tabState(f.a, first) == "open" && f.tt.Focused("Terminal box") }, "the box tab")
	f.tt.Type("echo hosts-ok")
	f.shows(first, "echo hosts-ok")

	// 4. Edit the port: the editor warns, and saving closes the tab.
	f.editBox()
	f.fill("Port", strconv.Itoa(f.port+1))
	for _, s := range []string{"Changing host or port forgets the host key and saved passwords unless you re-enter them.", "Saving will close 1 open tab."} {
		if !f.tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, f.tt.Texts())
		}
	}
	f.saveEditor()
	f.until(func() bool { return tabState(f.a, first) == "exited" }, "the box tab to exit")
	// Back to the real port: the pin went with the old endpoint, so connecting asks again.
	f.editBox()
	f.fill("Port", strconv.Itoa(f.port))
	f.saveEditor()
	f.until(func() bool { s := f.a.Servers(); return len(s) == 1 && s[0].Port == f.port && s[0].HostKey == "" }, "box back on its port, unpinned")
	f.click("Hosts")
	f.click("Open box")
	f.trust()
	f.configRecords("box", "save", 3)
	f.configRecords("box", "trust", 2)

	// 5. Forget the key; reconnecting shows the prompt again.
	f.editBox()
	if !f.tt.HasText("SHA256:") {
		t.Fatal(f.tt.Texts())
	}
	f.click("Forget host key")
	f.until(func() bool { return f.tt.HasText("Not pinned") }, "the editor to show the key forgotten")
	f.configRecords("box", "forgetHostKey", 1)
	f.click("Close")
	f.click("Hosts")
	f.click("Open box")
	last := f.a.tabs.Active
	f.until(func() bool { return f.tt.HasText("Trust this host key?") }, "the prompt again")
	f.click("Cancel")
	f.until(func() bool { return !f.tt.HasText("Trust this host key?") && tabState(f.a, last) == "exited" }, "the cancelled tab")

	// Delete waits half a second, then removes the host.
	f.click("Hosts")
	f.click("Delete box")
	f.pass()
	f.click("Delete host")
	f.until(func() bool { return len(f.a.Servers()) == 0 && f.tt.HasText("No hosts yet.") }, "box gone")
	f.configRecords("box", "delete", 1)
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `cd app && go test ./internal/hubtest ./view -count=1 -run 'TestStart|TestFlowHosts'`
Expected: FAIL to compile: `undefined: hubtest.Start`.

- [ ] **Step 3: Implement**

In `app/internal/hubtest/hubtest.go`, add `"strconv"` to the imports, and replace `SSHD`

```go
// SSHD starts sshtestd, which writes a ready vault (password Password,
// server "box", host key pinned), and returns the vault's path.
func SSHD(t *testing.T) string {
	t.Helper()
	_, bin := Binaries(t)
	store := filepath.Join(t.TempDir(), "servers.json")
	cmd := exec.Command(bin, "-write-store="+store, "-password="+Password)
	stdin, err := cmd.StdinPipe() // sshtestd exits when its stdin closes
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "PORT=") {
		t.Fatalf("sshtestd: %q, %v", line, err)
	}
	return store
}
```

with

```go
// Options picks what Start has sshtestd write besides serving.
type Options struct {
	EmptyStore bool   // no vault: a hub on Server.Store starts on Create vault
	KnownHosts string // when set, sshtestd writes its host key there as a known_hosts line
}

// Server is a running sshtestd: it accepts any password and any key.
type Server struct {
	Store string // the hub's --store; a ready vault (password Password, server "box", pinned) unless EmptyStore
	Port  int
}

// Start runs sshtestd until the test ends.
func Start(t *testing.T, o Options) Server {
	t.Helper()
	_, bin := Binaries(t)
	store := filepath.Join(t.TempDir(), "servers.json")
	args := []string{"-password=" + Password}
	if !o.EmptyStore {
		args = append(args, "-write-store="+store)
	}
	if o.KnownHosts != "" {
		args = append(args, "-write-known-hosts="+o.KnownHosts)
	}
	cmd := exec.Command(bin, args...)
	stdin, err := cmd.StdinPipe() // sshtestd exits when its stdin closes
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	port, perr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PORT=")))
	if err != nil || !strings.HasPrefix(line, "PORT=") || perr != nil {
		t.Fatalf("sshtestd: %q, %v", line, err)
	}
	return Server{Store: store, Port: port}
}

// SSHD starts sshtestd with a ready vault (Start with no options) and
// returns the vault's path.
func SSHD(t *testing.T) string { return Start(t, Options{}).Store }
```

- [ ] **Step 4: Run them**

Run: `cd app && go test -race ./internal/hubtest -count=1 && MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race ./view -run 'TestFlow' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: `ok` for `hubtest`; every `TestFlow…` PASS, `TestFlowHostsCreateTrustEditForgetDelete` among them (about 5 s), and slice 2's `TestFlowHostKeyPrompt` still passes with `configRecords` behind `auditedConfig`. Then `-count=3` without a flake.

Check that it bites, restoring after each:
- in `buildEditorBody` (`view/editor.go`), remove `.Label("Port")` from the port input: the flow must FAIL at `ui: no element shows "Port"`;
- in `forgetKey`, remove `a.reloadServers()`: the flow must FAIL waiting for "the editor to show the key forgotten".

- [ ] **Step 5: Commit**

```bash
git add app/internal/hubtest/hubtest.go app/internal/hubtest/hubtest_test.go app/view/flow_unix_test.go app/view/flowhosts_unix_test.go
git commit -m "app: flow test of hosts.spec against a real hub (create, trust, edit, forget, delete)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Flow tests against a real hub: import and the editor's keyboard

Ports `desktop/e2e/import.spec.ts` and the editor test of `keyboard.spec.ts:101-118` (renderer.md §6.2, §6.3; protocol.md §3).

**Files:**
- Modify: `app/view/flowhosts_unix_test.go`

**Interfaces:**
- Consumes: Task 5 (`App.imp`, the sheet's texts), Task 3 (`App.editor`), Task 7 (`flowWith`, `configRecords`, `hubtest.Options.KnownHosts`, `flowT.port`), slice 1-2 flow helpers.
- Produces: nothing new.

- [ ] **Step 1: Write the tests**

In `app/view/flowhosts_unix_test.go`, replace the import block with

```go
import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/internal/hubtest"
	"github.com/lang315/sshgate/app/model"
)
```

and append:

```go

// TestFlowImportPinsFromKnownHosts: import.spec.ts. The config's last
// stanza points IdentityFile and UserKnownHostsFile into the test's own
// folder, so ssh -G never reads the real ~/.ssh.
func TestFlowImportPinsFromKnownHosts(t *testing.T) {
	needLib(t)
	for _, bin := range []string{"ssh", "ssh-keygen"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("import needs the OpenSSH client: ", err)
		}
	}
	dir := t.TempDir()
	key, kh, cfg := filepath.Join(dir, "id_ed25519"), filepath.Join(dir, "known_hosts"), filepath.Join(dir, "ssh_config")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v %s", err, out)
	}
	f := flowWith(t, hubtest.Options{KnownHosts: kh}, "--sshConfig="+cfg)
	conf := strings.Join([]string{
		"Host work", "  HostName 127.0.0.1", fmt.Sprintf("  Port %d", f.port), "  User test", "  IdentityFile " + key,
		"Host viaproxy", "  HostName 127.0.0.1", "  ProxyCommand nc %h %p",
		"Host *", "  IdentityFile " + filepath.Join(dir, "missing"), "  UserKnownHostsFile " + kh, "",
	}, "\n")
	if err := os.WriteFile(cfg, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	f.unlock()
	f.tt.SetSize(1000, 900)
	f.click("Import from SSH config")
	f.until(func() bool { return f.tt.HasText("Import work") }, "the scanned rows")
	for _, s := range []string{"StrictHostKeyChecking accept-new", "SHA256:", "needs ProxyCommand"} {
		if !f.tt.HasText(s) {
			t.Fatalf("missing %q in %q", s, f.tt.Texts())
		}
	}
	if !f.a.imp.checked["work"] || f.a.imp.checked["viaproxy"] {
		t.Fatalf("checked %v", f.a.imp.checked)
	}
	f.click("Import 1 host")
	f.until(func() bool { return f.a.imp == nil && len(f.a.Servers()) == 2 }, "the sheet to close with work imported")
	f.configRecords("work", "import", 1)

	// Pinned at import: no "New key" chip, and connecting asks nothing.
	if f.tt.HasText("New key") || !f.tt.HasText("Open work") {
		t.Fatal(f.tt.Texts())
	}
	f.click("Open work")
	id := f.a.tabs.Active
	f.until(func() bool { return tabState(f.a, id) == "open" && f.tt.Focused("Terminal work") }, "the work tab")
	if f.tt.HasText("Trust this host key?") {
		t.Fatal("an imported host asked to trust its key")
	}
	f.tt.Type("echo import-ok")
	f.shows(id, "echo import-ok")
}

// TestFlowKeyboardMovesTheAuthChoice: keyboard.spec.ts's editor test. The
// arrows move and choose among the Auth radios, wrapping; Esc closes.
func TestFlowKeyboardMovesTheAuthChoice(t *testing.T) {
	f := flow(t)
	f.unlock()
	f.tt.SetSize(1000, 900)
	f.click("New host")
	f.click("+ Key or agent")
	f.tt.Frame() // the radios replace the button
	f.click("password")
	f.tt.Frame()
	if !f.tt.Focused("password") {
		t.Fatal("password is not focused")
	}
	f.tt.Key(0, ui.KeyRight)
	f.tt.Frame()
	if !f.tt.Focused("key") || f.a.editor.draft.Auth != "key" {
		t.Fatal("ArrowRight: want key focused and chosen")
	}
	f.tt.Key(0, ui.KeyLeft)
	f.tt.Key(0, ui.KeyLeft)
	f.tt.Frame()
	if !f.tt.Focused("agent") || f.a.editor.draft.Auth != "agent" {
		t.Fatal("ArrowLeft twice: want agent focused and chosen")
	}
	f.tt.Key(0, ui.KeyEscape)
	f.until(func() bool { return f.a.editor == nil && !f.tt.HasText("Host editor") }, "Esc to close the editor")
}
```

- [ ] **Step 2: Run them**

Run: `cd app && MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race ./view -run 'TestFlow(Import|KeyboardMoves)' -count=3 -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: both PASS three times. Without `ssh` or `ssh-keygen` on `PATH` the import test SKIPs; without the library it SKIPs (`needLib`); the keyboard test needs neither.

These port e2e assertions onto code from Tasks 3 and 5, so they pass at once. Check that they bite, restoring after each:
- in `scanImport` (`view/importsheet.go`), replace `model.NextChecked(s.Candidates, prev)` with `model.NextChecked(nil, prev)`: `TestFlowImportPinsFromKnownHosts` must FAIL at `checked map[]`;
- in `buildEditorBody`, replace `ui.Radio(c.Key("auth-"+v), &d.Auth, v, v)` with `ui.Radio(c, &d.Auth, v, v)` and `ui.RadioGroup(c.Key("auth"), ` with `ui.RadioGroup(c, `: `TestFlowKeyboardMovesTheAuthChoice` must FAIL at "ArrowRight: want key focused and chosen".

- [ ] **Step 3: Run the whole module**

Run, inside `app/`:

```bash
go vet ./... && gofmt -l . && go test -race ./... -count=1
MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race ./... -count=1
GOOS=linux go vet ./... && GOOS=windows go vet ./... && GOOS=windows go test -c -o /dev/null ./view
```

Expected: no vet or gofmt output; every package `ok` in both runs (rerun once if only `TestStaleStatusReplyIgnored` fails, Global Constraints); the cross-OS vet and test build are clean.

- [ ] **Step 4: Commit**

```bash
git add app/view/flowhosts_unix_test.go
git commit -m "app: flow tests of import.spec and the editor part of keyboard.spec

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Manual check (author, macOS, end of slice)

Not a test; the controller runs it with a build of the slice's last commit and a throwaway sshtestd vault (own `SSHGATE_STORE` and `SSHGATE_RUNTIME_DIR`), as for slices 1 and 2:
- New host, fill it in, Save; open it, trust it; Edit it: the pin line shows, Forget host key unpins it, a port change shows both warnings and the tab exits on Save;
- the editor sits beside the host list; the AI requests column stays usable while it is open (send an AI request through the bridge and Deny it with the editor open); Esc in the editor closes it, Esc in the search field does not;
- Delete: the confirm's Delete host is grey for half a second; Enter cancels;
- Import from SSH config with a test config (`SSHGATE_SSH_CONFIG`): rows, pins, a skipped ProxyCommand host; Import 1 host; it opens without a prompt;
- the theme control: Dark and Light change the window and the native title bar at once; Auto follows System Settings; the choice survives a restart (`theme.json` under `~/Library/Application Support/<the executable or bundle name>`);
- Open host editor is covered by fake-door tests only: sshtestd's host key is new in every process and its port random, so a real mismatch is not reachable (slice 2 Decision 10).
