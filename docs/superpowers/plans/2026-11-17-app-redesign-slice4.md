# App Visual Redesign, Slice 4 Implementation Plan (sheets, tabs, remaining dialogs, hub screens, DESIGN.md)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put the editor and import sheets, the Files and Tunnels tabs, the audit detail, the host-key and confirm dialogs and the hub screens on the warm + teal tokens, then run the impeccable finish review and write DESIGN.md from the built app.

**Architecture:** All work is in `app/view`. Two shared pieces:
- `decisionButton` (`guard.go`) gives the mouse-only decision buttons their tone once they are enabled. A disabled one looks exactly as today.
- An `enter` pose helper (`modal.go`) gives the sheets the same entrance as dialogs.

The rest is per-screen styling from `tok(c)`. No hub, protocol, vault or copy change.

**Tech Stack:** Go, mygo `ui` (fork `github.com/lang315/mygo` at `3413a999e44f`).

**Spec:** `docs/superpowers/specs/2026-11-16-app-visual-redesign-design.md` (Slices item 4; Motion grammar row "Dialogs and editor sheet"; Design process (impeccable)).

**Branch:** `feat/app-redesign-screens`, stacked on `feat/app-redesign-dialogs` (PR #57). Its PR targets `feat/app-redesign-dialogs`. Commit `3cb1d82` already adds before-shots of every screen (`TestShots`).

## Global Constraints

- `app/` only, plus `DESIGN.md` and `.impeccable/` in Task 5. No hub, protocol or vault change. **No copy change**: every label, sentence and button text stays as it is.
- Colours come from named tokens; no `ui.Hex` outside `palette.go`. `c.Theme().Surface` is `tok(c).Raised`: name `tok(c).Surface` / `tok(c).Raised` / `tok(c).Bg` / `tok(c).TermBg` explicitly for every new background.
- Every text/background pair passes 4.5:1 and every UI pair 3:1, both themes; any new pair goes into `TestPaletteContrast`.
- The app never calls the hub because of an animation, a timer or a redraw. No animation changes the size of a mounted terminal.
- Safety rules unchanged:
  - Deny is the default and reachable by keyboard.
  - Allow, Send to tab, Trust, Enable, Resume, Overwrite all, Skip existing and Delete stay mouse-only with the 500 ms delay.
  - "Deny all" is always rendered.
  - The safe choice keeps the focus and the filled primary look: Cancel in the auto-allow, settings and confirm dialogs.
  - A decision button looks the same as today while its delay runs, and only takes its tone once enabled.
- Motion: dialogs and the editor and import sheets fade in and scale 0.98 → 1 over 160 ms from the centre. Under Reduce Motion every app-driven fade or pose is skipped. App-driven animation runs only while it moves.
- View tests use the Tester's clock. `view/term.go` keeps `NoClipboardWrite: true`.
- Test command: `cd app && MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race -timeout 20m ./view` (about 11 minutes); `go vet ./...` and `gofmt -l .` clean.
- Pixel helpers live in `view/pixels_test.go` (`region`, `sameRegion`, `countNear`, `grow`). Pixel tests call `ui.SetDefaultThemes(Themes())` and reset it in `t.Cleanup`.
- Commits end with `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>` (implementers); no session URL.

## Review Focus

1. **A decision button during its delay:** while disabled, Delete, Overwrite all, Skip existing, Trust and connect, Enable and Resume must look exactly as before; nothing invites a click early. Test in Task 1.
2. **The editor sheet opening beside the AI column:** the sheet's entrance pose must not move the AI column or its Allow. Test in Task 2.
3. **Long values in the redesigned rows:** a 200-character import alias line, a long tunnel forward, a long remote path in Files and a long stderr line. Each wraps or ellipsizes inside its row and never pushes buttons off-screen at 900×500. Tests in Tasks 2 and 3.
4. **Light theme tints:** the denied audit row, a running-tunnel pill and the import status chips must keep 4.5:1 text. Pairs go in `TestPaletteContrast` (Tasks 2 and 3).
5. **Reduce Motion with a sheet open:** the sheet is at rest in its first frame. Test in Task 2.

---

### Task 1: Decision buttons take their tone; dialog padding

**Files:**
- Modify: `app/view/guard.go`, `app/view/approvals.go` (`allowButton`), `editor.go:443`, `filejobs.go:397,427,430`, `hostkey.go:68`, `autoallow.go:83,168`; the dialog bodies at `autoallow.go:72`, `editor.go:437`, `files.go:343`, `filejobs.go:379,413`, `hostkey.go:49,117`, `settings.go:64`, `tunnels.go:287`
- Test: `app/view/guard_test.go`

**Interfaces:**
- Produces: `type tone int` with `toneNeutral`, `toneOk`, `toneAccent`, `toneDanger`, `toneDangerFill`, and `func decisionButton(c *ui.Context, text, name string, enabled bool, t tone) bool` in `guard.go`.
  - Disabled, or `toneNeutral`: exactly `mouseButtonNamed`'s look.
  - Enabled `toneOk`, `toneAccent` or `toneDanger`: a 1 px border and text in `Ok`, `Accent` or `Danger`.
  - Enabled `toneDangerFill`: `DangerFill` background with `DangerText`, plus `DangerFillHover` / `DangerFillPressed` when hovered or pressed, if the row reports it (as Deny does in `approvals.go`).
  - `MouseButton` and `mouseButtonNamed` stay as they are. `allowButton` becomes `decisionButton(c, "Allow", "Allow", enabled, toneOk)`.

- [ ] **Step 1: Write the failing tests** (`guard_test.go`)
  - `TestDecisionButtonDisabledLooksNeutral`: for each tone, a disabled `decisionButton`'s region is `sameRegion` with a disabled `MouseButton` of the same text.
  - `TestDecisionButtonTones`: enabled, each tone shows its colour, in light and dark:
    - `toneOk`, `toneAccent` and `toneDanger`: at least 10 border pixels in the tone colour (tol 20);
    - `toneDangerFill`: a centre patch within tol 3 of `DangerFill`.
- [ ] **Step 2: Run them to verify they fail** (`decisionButton` undefined).
- [ ] **Step 3: Implement, and apply the tones.**
  - Tones per button:
    - Delete host (`editor.go`) and Delete for good (`filejobs.go`): `toneDangerFill`;
    - Overwrite all: `toneDanger`;
    - Skip existing: `toneNeutral`;
    - Trust and connect: `toneAccent`;
    - Enable (auto-allow): `toneDanger`, because it widens what an AI may run without asking;
    - Resume: `toneAccent`;
    - Settings' Save stays `MouseButton`.
  - Keep every call's re-check after the click exactly as it is.
  - Dialog padding: `ui.Modal`'s panel already pads 20, so drop `Padding(20)` from the nine dialog body columns listed above. Keep their widths and gaps.
  - In `settings.go`, give the "Settings" title a bottom margin, so the gap to the first heading is 16 rather than 10.
- [ ] **Step 4: Run the whole `./view` suite with `-race`.** Expected `ok`, with no existing decision-button test weakened.
- [ ] **Step 5: Commit** — `feat(app): decision buttons take their tone once enabled; single dialog padding`

---

### Task 2: Editor and import sheets

**Files:**
- Modify: `app/view/modal.go`, `app/view/editor.go` (`buildEditor`), `app/view/importsheet.go` (`buildImport`), `app/view/settings.go` (move `section` out), `app/view/palette_test.go`
- Create: `app/view/section.go` (the `section` helper moved from `settings.go`, unchanged)
- Test: `app/view/editor_test.go`, `app/view/importsheet_test.go`

**Interfaces:**
- Consumes: `section(c, title, fn)` (the Settings section from slice 3), `modal`, `tok(c)`, `radiusCard`, `radiusControl`, `raisedShadow`.
- Produces: `func enter(c *ui.Context, e ui.Element)` in `modal.go`. It is the entrance pose `modal()` uses today: the two-build start pose `{Scale: 0.98, Fade: 1}`, then rest over 160 ms, `TransformOrigin(0.5, 0.5)`, nothing under Reduce Motion. `modal()` calls it, and so do the two sheets.

- [ ] **Step 1: Write the failing tests**
  - `TestSheetEntersWithoutMovingAIColumn` (`editor_test.go`): 1000×640, the AI column open with one request.
    - Note the Allow bounds, then click "New host".
    - At +40 ms the sheet's region differs from its region at rest (after `tt.Advance(time.Second)`).
    - Allow's bounds are equal to those noted before the sheet opened, at +40 ms and at rest.
  - `TestSheetReduceMotionAtRest` (`editor_test.go`): with Reduce Motion, the sheet's region in its first frame is `sameRegion` with its region at rest.
  - `TestEditorGroupsOnRaised` (`editor_test.go`): with the existing-host editor open, dark theme:
    - the sheet background patch is `Surface`;
    - a patch inside the "Credentials" group is `Raised`;
    - a patch inside the opened "AI access" fold is `Raised`.
  - `TestImportRowsAreCards` (`importsheet_test.go`): with the scan result of the `import` shot:
    - each alias row's background patch is `Raised`;
    - the "Ready" chip has `OkTint` pixels and "Skipped" has `WaitTint` pixels (tol 3);
    - a 200-character alias's row keeps "Import 2 hosts" inside the window at 900×500.
- [ ] **Step 2: Run them to verify they fail.**
- [ ] **Step 3: Implement**
  - Extract `enter` from `modal()` with no behaviour change. `TestModal*` must still pass.
  - Both sheets' outer column:
    - `Background(tok(c).Surface)`, `BorderStartWidth(1)` in `tok(c).Line` (the side facing the hosts), and `raisedShadow`;
    - `enter(c, sheet)`, called once per opening. Key the sheet so a new opening enters again.
  - Editor groups, each a `Raised` panel (`radiusCard`, `Padding(12)`, `Gap(8)`):
    - Address, Label and the SSH port, with no heading (adding one would be new copy);
    - "Credentials": its existing heading stays the group heading; the group holds its help, User, Password and its Clear, and "+ Key or agent";
    - each of the two folds' open body;
    - "Host key": its existing heading; the fingerprint sits in a `TermBg` block with `TermFg` mono text (`radiusControl`, `Padding(6, 10)`), then Forget host key.
  - Import rows: each alias is a `Raised` card (`radiusCard`, `Padding(10)`, `Gap(4)`). Its status word becomes a chip (`Padding(2, 8)`, `radiusControl`):
    - Ready: `OkTint` background, `Ok` text;
    - Skipped and Already in vault: `WaitTint` background, `Wait` text.
    
    The help paragraph at the top uses Text, not Muted. Mono lines wrap.
  - Add `ok/okTint` and `wait/waitTint` to `TestPaletteContrast` if they are not already there.
- [ ] **Step 4: Run the whole `./view` suite with `-race`.** Expected `ok`. The editor tests use `clickFold` and focus, and Esc closes only from inside the editor; all must stay green.
- [ ] **Step 5: Commit** — `feat(app): editor and import sheets on Surface with raised groups and an entrance`

---

### Task 3: Files, Tunnels and Audit detail

**Files:**
- Modify: `app/view/files.go`, `app/view/filejobs.go` (job strip), `app/view/tunnels.go`, `app/view/audit.go`, `app/view/palette_test.go`
- Test: `app/view/files_test.go`, `app/view/tunnels_test.go`, `app/view/audit_test.go`

**Interfaces:**
- Consumes: `tok(c)`, `radiusCard`, `radiusControl`, pixel helpers.

- [ ] **Step 1: Write the failing tests**
  - `TestFilesTableAndJobStrip` (`files_test.go`), light:
    - the column header row's background patch is `Raised`;
    - the job strip's background patch is `Raised`, and its top edge has a `Line` pixel row;
    - at 900×500, a 300-character path in the path field keeps "Refresh" inside the window.
  - `TestTunnelStatusPill` (`tunnels_test.go`), light and dark:
    - the "running" text sits on `OkTint` pixels with `Ok` text pixels (tol 20);
    - "stopped" has no `OkTint` pixels;
    - the rows are separated by `Divider` lines;
    - the footer sentence is in Text, not Muted;
    - a 200-character forward keeps the row's Delete inside the window at 900×500.
  - `TestAuditDetailInset` (`audit_test.go`), dark:
    - an expanded row's detail patch is `Raised`;
    - a denied row's background is `DangerTint` (tol 3) and its outcome text has `Danger` pixels;
    - the "AI's description · unverified" line keeps its `Wait` colour, now on `Raised`; `wait/raised` is already in `TestPaletteContrast`.
- [ ] **Step 2: Run them to verify they fail.**
- [ ] **Step 3: Implement**
  - Files:
    - toolbar: the actions that need a selection (Download, Rename, Delete) sit after a 1 px `Divider` separator, apart from New folder / Upload files / Upload folder;
    - table: the header row is on `Raised`, rows get `RowHover` on hover, and the table is on `Surface` with `radiusCard`;
    - job strip: `Raised` background, `BorderWidth(1, 0, 0, 0)` in `Line`, `Padding(8, 12)`.
  - Tunnels:
    - the table sits in a `Surface` card (`radiusCard`) with a `Raised` header row and `Divider` lines between rows;
    - the status is a chip: running is `OkTint`/`Ok`, stopped and others are plain Muted text;
    - the footer sentence uses Text inside a `WaitTint` box (`radiusControl`, `Padding(8, 12)`);
    - the "Add tunnel" button stays as is.
  - Audit detail:
    - the expanded detail is a `Raised` inset (`radiusControl`, `Padding(10)`, start margin 16);
    - denied rows use a `DangerTint` background with `Danger` outcome text, instead of today's colour. Keep allowed rows as they are;
    - labels inside the detail use `Label`, not Muted, where they sit on `Raised`.
  - Any new text/tint pair goes into `TestPaletteContrast`.
- [ ] **Step 4: Run the whole `./view` suite with `-race`.** Expected `ok`.
- [ ] **Step 5: Commit** — `feat(app): Files, Tunnels and audit detail on Surface cards with status chips`

---

### Task 4: Host-key dialogs, delete confirm, hub screens

**Files:**
- Modify: `app/view/hostkey.go`, `app/view/editor.go` (delete confirm body), `app/view/filejobs.go` (confirm bodies), `app/view/screens.go` (`buildHub`, and the unlock chip's `t.Surface` → `tok(c).Raised`)
- Test: `app/view/hostkey_test.go`, `app/view/screens_test.go`

- [ ] **Step 1: Write the failing tests**
  - `TestHostKeyFingerprintsInBlocks` (`hostkey_test.go`), light and dark:
    - in the mismatch dialog, each fingerprint sits on `TermBg` pixels (tol 3);
    - "Presented now"'s fingerprint text has `Danger` pixels;
    - the unknown dialog's fingerprint is also on `TermBg`.
  - `TestConfirmListsInBlocks` (`filejobs_test.go`): the conflict dialog's file list sits on a `Raised` block.
  - `TestHubFailedLook` (`screens_test.go`), dark:
    - the failed screen's shield has `Danger` pixels and no `Accent` pixels;
    - the stderr lines sit on `TermBg`.
  - `TestHubStartingShowsSpinner`: the starting screen has a `ui.Spinner`. Assert with `tt.FramePending()` true and a node whose role is the spinner's (see mygo `Spinner`), or by any stable means the Tester offers.
- [ ] **Step 2: Run them to verify they fail.**
- [ ] **Step 3: Implement**
  - Host-key dialogs: each fingerprint in a `TermBg` block with `TermFg` mono text (`radiusControl`, `Padding(6, 10)`); the presented fingerprint's text is `Danger`. Add `danger/termBg` if missing (it exists from slice 3). The red warning panel stays.
  - File-job and delete confirms: the list of names sits in a `Raised` block (`radiusControl`, `Padding(8, 10)`, a max height with scroll if it already has one).
  - Hub screens:
    - starting: `ui.Spinner` centred under the shield, and the text centred;
    - failed: the shield in `Danger`, and the stderr lines in a `TermBg` block (`radiusControl`, `Padding(8, 10)`, mono `TermFg`);
    - restarting: `ui.Spinner` like starting.
    
    The spinner is mygo's own and does not call the hub; check that in its source and say so in the report.
  - The unlock auto-allow host chip: `Background(tok(c).Raised)` by name. This is no visual change.
- [ ] **Step 4: Run the whole `./view` suite with `-race`.** Expected `ok`.
- [ ] **Step 5: Commit** — `feat(app): fingerprints and lists in blocks; hub screens with spinner and error shield`

---

### Task 5: Impeccable finish — screenshots, finish review, DESIGN.md

**Files:**
- Modify: as the finish review requires (`app/view/*`)
- Create: `DESIGN.md`, `.impeccable/design.json` (by the documenter)

- [ ] **Step 1: Screenshots.** Run `TestShots` (every screen, both themes) into a fresh directory and look at every image. List anything broken.
- [ ] **Step 2: Finish review.** The controller dispatches the `impeccable:impeccable-finish-reviewer` agent with:
  - the direction contract `.impeccable/surfaces/app-view-home-go.md`;
  - the spec;
  - the screenshot directory.

  It returns an ordered list of material fixes.
- [ ] **Step 3: Apply the material fixes** that keep the Global Constraints, with a test per behavioural change. Park the rest in the ledger with a ruling.
- [ ] **Step 4: DESIGN.md.** The controller dispatches the `impeccable:impeccable-documenter` agent to write `DESIGN.md` and `.impeccable/design.json` from the built app: tokens from `palette.go`, the components (cards, chips, decision buttons, dialogs, sheets), and the motion grammar.
- [ ] **Step 5: Full suite, vet, gofmt; commit** — `docs(app): DESIGN.md from the built app; finish-review fixes`
