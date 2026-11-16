# App Visual Redesign, Slice 3 Implementation Plan (AI column, unlock, create vault, Settings, dialogs)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish slice 2's open test gaps, then give the AI approval column, the unlock and create-vault screens, Settings and every dialog the warm + teal look and the spec's dialog and card motion.

**Architecture:** All work is in `app/view`. A new `modal()` helper (`view/modal.go`) wraps `ui.Modal`, restyles the panel it returns and gives it the entrance pose; the nine `ui.Modal` calls move to it. The AI column (`view/approvals.go`) gets card, command-block and button styling from `tok(c)`, and fades in only cards that arrive while it is open. `card()` in `view/screens.go` becomes the 360-wide centred card with the shield. No hub, protocol or vault change.

**Tech Stack:** Go, mygo `ui` (fork `github.com/lang315/mygo` at `3413a999e44f`).

**Spec:** `docs/superpowers/specs/2026-11-16-app-visual-redesign-design.md` (sections "AI approval column", "Unlock, create vault, Settings", "Motion grammar", "Slices" item 3).

**Branch:** `feat/app-redesign-dialogs`, stacked on `feat/app-redesign-shell` (PR #56). Its PR targets `feat/app-redesign-shell`.

## Global Constraints

- `app/` only. No hub, protocol or vault change.
- Colours come from named tokens; no `ui.Hex` outside `palette.go`. **`c.Theme().Surface` is `tok(c).Raised`** (`Themes()` maps it so): every new background in this plan names `tok(c).Surface`, `tok(c).Raised`, `tok(c).Bg` or `tok(c).TermBg` explicitly, never `t.Surface`.
- Every text/background pair passes 4.5:1 and every UI pair 3:1, both themes, checked in `TestPaletteContrast`.
- The app never calls the hub because of an animation, a timer or a redraw. No animation changes the size of a mounted terminal.
- Safety rules unchanged: Deny default and keyboard-reachable; Allow, Send to tab, Trust, Enable, Resume, Overwrite all, Skip existing, Delete mouse-only with the 500 ms delay (`model.AllowDelay`); "Deny all" always rendered; "Stop auto-allow and lock" has no password and no delay.
- Nothing in the approvals list animates position or size; a new card fades in by opacity only (120 ms), and its arrival restarts the Allow delay as today.
- Dialogs: fade + scale 0.98 → 1 over 160 ms, from the centre; their buttons' delays start at mount, as now.
- **Reduce Motion policy (one rule, as slice 2):** under `c.Preferences().ReduceMotion` every fade or pose the app drives is skipped, so the content is at rest from its first frame.
- An app-driven animation calls `Animate`/`AnimatePose` only while it runs (animating every element at rest made `-race` 7× slower and broke `TestFlowSoftLock` in slice 2).
- View tests use the Tester's clock (`tt.Advance`), never a wall-clock wait. `view/term.go` keeps `NoClipboardWrite: true`.
- Test command: `cd app && MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race ./view` (about 9 minutes); `go vet ./...` and `gofmt -l .` clean.
- Commits end with `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>` (implementers) and never carry a session URL.

## Review Focus

1. **A request arrives while the pointer rests on another card's Allow:** the cards above do not move; the new card is appended and only fades. Test in Task 3.
2. **Enter pressed in a dialog's first frame, during its entrance:** Cancel has the focus, so the dialog cancels. Test in Task 2.
3. **A request withdrawn (decided elsewhere, expired) mid-fade:** its fade state is dropped and no frame loop is left behind. Test in Task 3.
4. **Reduce Motion on:** dialogs and arriving cards are at rest in their first frame. Tests in Tasks 2 and 3.
5. **Light theme, Allow's green text on a raised card:** today's light `Ok` `#3F7A57` on `Raised` `#eef0f6` is 4.46:1, under 4.5. Task 3 changes light `Ok` and tests the pair.

---

### Task 1: Slice 2 follow-ups

**Files:**
- Create: `app/view/pixels_test.go`
- Modify: `app/view/home.go` (`buildHosts` lift map), `app/view/frame.go:101-114` (`buildAIColumn`), `app/view/shots_test.go`
- Test: `app/view/home_test.go`, `app/view/cards_test.go`, `app/view/frame_test.go`

**Interfaces:**
- Produces (test helpers, used by Tasks 2–5), in `pixels_test.go`, at the Tester's default scale (1 DIP = 1 px):
  - `func region(img *image.RGBA, r ui.Rect) []byte`: a copy of the pixels inside `r`, clipped to the image.
  - `func sameRegion(a, b *image.RGBA, r ui.Rect) bool`
  - `func countNear(img *image.RGBA, r ui.Rect, col ui.Color, tol uint8) int`: pixels whose R, G and B are each within `tol` of `col`.
  - `func grow(r ui.Rect, d float32) ui.Rect`

- [ ] **Step 1: Write the failing tests**
  - `TestViewChangeFadesInThenRests` (existing, `home_test.go`): keep its flag checks and add pixels. Take `tt.Image()` 40 ms after clicking Audit and again after `tt.Advance(time.Second)` plus two frames. The region of the "echo one" row (`tt.Find("echo one")`, grown by 4) must differ between the two, and two frames at rest must be `sameRegion`.
  - `TestGrantRingStartsAtTwelve` (`cards_test.go`): one server `build` with `AutoAllow{Until: a.Now().Add(time.Hour)}` (a quarter of the 4 h scale). Take the tile as the box around `tt.Find("B")`'s centre, half-size = the tile's half-size + 6. Count `tok` `Wait` pixels (tol 40) per quadrant around the centre: top-right > 0; top-left, bottom-left and bottom-right == 0.
  - `TestOpenButtonShowsFocusRing` (`cards_test.go`): no `Accent` pixels (tol 30) in `grow(Open build, 4)` before focus; after `tt.FocusNode(<id of "Open build">)` and a frame, the node is `Focused` and the count is > 0.
  - `TestAIColumnReduceMotionShowsAtOnce` (`frame_test.go`): `tt.SetPreferences(ui.Preferences{ReduceMotion: true})`, one pending request, open the column. The column's region in the first frame after opening is `sameRegion` with the one after `tt.Advance(time.Second)`.
  - `TestAIColumnKeepsItsNodeWhenSidebarHides` (`frame_test.go`): column open; note the `ID` of the `"Close AI requests"` node from `tt.Nodes()`; click "New host" (the editor hides the sidebar); the node's `ID` is the same.
  - `TestLiftForgetsRemovedHost` (`cards_test.go`): hover `hostA` until `a.lift["hostA"]` > 0; remove `hostA` from the servers (as the hub's `servers` refresh does in the fake door); after a frame `a.lift` has no `hostA`, and after `tt.Advance(time.Second)` `tt.FramePending()` is false.

- [ ] **Step 2: Run them to verify the new ones fail**

Run: `cd app && go test ./view -run 'TestViewChangeFadesInThenRests|TestGrantRingStartsAtTwelve|TestOpenButtonShowsFocusRing|TestAIColumnReduceMotionShowsAtOnce|TestAIColumnKeepsItsNodeWhenSidebarHides|TestLiftForgetsRemovedHost' -count=1`
Expected: `TestAIColumnKeepsItsNodeWhenSidebarHides` and `TestLiftForgetsRemovedHost` FAIL. The others may already pass: they pin behaviour that had no test. If one fails, the code is wrong; fix the code, not the test.

- [ ] **Step 3: Fix the code**
  - `buildAIColumn`: build the column on `c.Key("ai")`, so a sidebar that hides or shows does not remount it.
  - `buildHosts`: after the cards are built, delete every `a.lift` key whose host was not built this frame.
  - `shots_test.go`: the grant's `Until` comes from the app's clock (`a.Now().Add(25 * time.Minute)`), not a fixed date.

- [ ] **Step 4: Run the whole `./view` suite with `-race`.** Expected: `ok`.

- [ ] **Step 5: Commit** — `test(app): pixel checks for fades, ring and focus; AI column keyed; lift forgets removed hosts`

---

### Task 2: `modal()` helper and dialog motion

**Files:**
- Create: `app/view/modal.go`, `app/view/modal_test.go`
- Modify: the nine `ui.Modal` calls: `filejobs.go:378`, `filejobs.go:412`, `autoallow.go:71`, `editor.go:436`, `files.go:342`, `hostkey.go:48`, `hostkey.go:116`, `settings.go:55`, `tunnels.go:286`

**Interfaces:**
- Consumes: `tok(c)`, `radiusDialog` (14), `raisedShadow(c).On(e)` from `palette.go`.
- Produces: `func modal(c *ui.Context, open *bool, fn func()) ui.Element` — same contract as `ui.Modal` (callers key `c` as today and read `.Dismissed()` and `.Label(...)` on the result).

- [ ] **Step 1: Write the failing tests** (`modal_test.go`)
  - `TestModalPanelLook`: open Settings (as `settings_test.go` does). After `tt.Advance(time.Second)` and a frame, a 4×4 patch just inside the panel's top-left corner (8 px in from the panel's bounds; the panel is the node with `Role == ui.RoleDialog`) is within tol 2 of `tok` `Surface`, in both themes (`tt.SetDark`).
  - `TestModalEntersThenRests`: right after opening, `tt.FramePending()` is true and the dialog's region differs from its region after `tt.Advance(time.Second)`; after that advance and a frame, `tt.FramePending()` is false.
  - `TestModalReduceMotionAtRest`: with `ReduceMotion: true`, the dialog's region in its first frame is `sameRegion` with the one after `tt.Advance(time.Second)`.
  - `TestEnterCancelsSettingsDuringEntrance`: open Settings, then at once `tt.Key(0, ui.KeyEnter)` with no advance: `a.settings` is nil.
  - `TestEveryDialogUsesModal`: read each non-test `.go` file in `view/`; none but `modal.go` contains `ui.Modal(`.

- [ ] **Step 2: Run them to verify they fail.** Run: `go test ./view -run 'TestModal|TestEnterCancelsSettingsDuringEntrance|TestEveryDialogUsesModal' -count=1`. Expected: FAIL (`modal` undefined).

- [ ] **Step 3: Implement `modal`**
  - Call `ui.Modal(c, open, fn)`. It returns the panel, which `coreModal` has already styled (theme `Background`, radius `Space(2.5)`, its own shadow). Restyle that panel after the call: `Background(tok(c).Surface)`, `Radius(radiusDialog)`, and `raisedShadow(c).On(panel)`. `TestModalPanelLook` proves the restyle wins; if it does not, stop and report rather than wrapping the content.
  - Entrance, per `AnimatePose`'s own doc comment: `entered := ui.Local(panel, "entered", func() bool { return false })`. In the first frame, unless Reduce Motion is on, pose `ui.Pose{Scale: 0.98, Fade: 1}` and `c.Invalidate()`. In every later frame pose `ui.Pose{}` with `ui.Animation{Duration: 160 * time.Millisecond}`. Under Reduce Motion, pose nothing.
  - A closed modal (`*open` false) returns the zero `Element`; calling methods on it is safe in mygo.
  - Replace the nine calls. Keyed modals stay keyed exactly as they are, because Cancel's `AutoFocus` depends on it.

- [ ] **Step 4: Run the whole `./view` suite with `-race`.** Expected: `ok`. Tests that click a dialog button right after opening may need a `tt.Advance`. Add the advance; never loosen an assertion. A focus test that fails means the entrance broke focus: fix `modal`.

- [ ] **Step 5: Commit** — `feat(app): dialogs on Surface with a 160 ms fade and scale entrance`

---

### Task 3: AI column cards, Deny and Allow, arriving-card fade

**Files:**
- Modify: `app/view/palette.go`, `app/view/palette_test.go`, `app/view/approvals.go`, `app/view/frame.go` (column background), `app/view/home.go:24-26` (reset on close)
- Test: `app/view/approvals_test.go`

**Interfaces:**
- Consumes: `tok(c)`, `radiusCard` (10), `radiusControl` (6), `MouseButton` and `mouseButtonNamed` (`guard.go`), Task 1's pixel helpers.
- Produces:
  - token `DangerText` (`#ffffff` in both themes), the text on `DangerFill`;
  - `func allowButton(c *ui.Context, enabled bool) bool` in `approvals.go`: mouse-only like `MouseButton`. Enabled, it draws a green outline (`Border(1, tok(c).Ok)`) and green text (`tok(c).Ok`). Disabled, it looks exactly like a disabled `MouseButton`;
  - `colState` fields `seen map[string]bool` and `fading map[string]int` (request ID → frames into its fade).

- [ ] **Step 1: Palette.** Change light `Ok` from `#3F7A57` to `#3D7554` (on `Raised` `#eef0f6` that is 4.76:1). Add `DangerText`. In `TestPaletteContrast`:
  - add `ok/raised` (4.5);
  - add `danger/termBg` (4.5): non-ASCII runs in the command block are `Danger`;
  - replace the `ui.Hex("#ffffff")` literal in `white/dangerFill` with `p.DangerText`.
  
  Run `go test ./view -run TestPaletteContrast`. Expected: PASS.

- [ ] **Step 2: Write the failing tests** (`approvals_test.go`; push requests the way the existing tests do, e.g. `d.set(func() { d.pending = ... })` plus the pending notification)
  - `TestArrivingRequestFadesWithoutMovingOthers`: column open with `r1`; `tt.Advance(time.Second)`. Note `r1`'s Allow bounds and check it is enabled. Then `r2` arrives.
    - At +40 ms: `r2`'s card region differs from its rest image; `tt.FramePending()` is true; `r1`'s Allow bounds are unchanged; `r2`'s Allow node is `Disabled`.
    - At +499 ms from arrival, `r2`'s Allow is disabled; at +501 ms it is enabled.
    - After `tt.Advance(time.Second)`: `len(a.col.fading) == 0` and `!tt.FramePending()`.
  - `TestRequestsPresentAtOpenDoNotFade`: open the column with `r1` and `r2` pending. After the first frame, `a.col.fading` is empty.
  - `TestRequestGoneMidFadeLeavesNoFade`: `r2` arrives; at +40 ms it is withdrawn (pending list without it, plus its decided notification). After a frame, `a.col.fading` has no `r2`; after `tt.Advance(time.Second)`, `!tt.FramePending()`.
  - `TestReduceMotionNoArrivalFade`: with `ReduceMotion: true`, `r2`'s arrival leaves `a.col.fading` empty, and its first-frame region is `sameRegion` with its rest region.
  - `TestAllowTurnsGreenWhenEnabled`, light theme, in `grow(Allow bounds, 1)`:
    - during the delay, no `Ok` pixels (tol 20);
    - after `tt.Advance(time.Second)`, more than 10.

- [ ] **Step 3: Run them to verify the new ones fail.** Expected: FAIL (no fade state, no green).

- [ ] **Step 4: Implement**
  - **Column:** background `tok(c).Surface`, replacing `t.Surface`, which is `Raised`.
  - **Request card:** `Background(tok(c).Raised)`, `Radius(radiusCard)`, `Padding(12)`, `Border(1, tok(c).Line)`. A sudo card has `Border(1, tok(c).Danger).BorderStartWidth(4)`: the red left edge.
  - **SUDO chip:** text `SUDO`, bold, `Padding(2, 8)`, `Radius(radiusControl)`, `Background(tok(c).DangerTint)`, `TextColor(tok(c).Danger)`.
  - **Command and stdin:** each sits in a block with `Background(tok(c).TermBg)`, `TextColor(tok(c).TermFg)`, `Padding(8, 10)` and `Radius(radiusControl)`. `segments` keeps its `Danger` underline for non-ASCII runs.
  - **Deny:** stays a `ui.Button`, so it keeps the focus and Enter. Style it `Background(tok(c).DangerFill)` with `TextColor(tok(c).DangerText)`.
  - **Allow:** `allowButton(c, enabled)`. Keep the click re-check that is there today (`model.ClickAllowed(it, a.Now(), ...)`). Send to tab is unchanged.
  - **Order and sizes:** button order, sizes, Deny all and the Close button are unchanged.
  - **Arriving-card fade:**
    - When the column mounts (`!col.mounted`), set `seen` to the IDs present and empty `fading`.
    - On each build, an ID not in `seen` joins `seen`, and joins `fading` at 0 unless Reduce Motion is on.
    - A card in `fading` animates its opacity with `card.Animate("arrive", target, 120*time.Millisecond)`, where `target` is 0 at step 0 and 1 after. Bump the step. Delete the ID once the step is past 0 and the value has reached 1; otherwise `c.Invalidate()`.
    - Prune both maps of IDs no longer in `a.items`.
    - The reset in `buildReady` when the column closes also clears both maps.

- [ ] **Step 5: Run the whole `./view` suite with `-race`.** Expected: `ok`, with every existing approvals, Allow-delay, Deny-default and "Deny all" test passing unchanged.

- [ ] **Step 6: Commit** — `feat(app): AI request cards on raised, red Deny, green Allow, arriving cards fade in`

---

### Task 4: Unlock, create vault and the hub screens' card

**Files:**
- Modify: `app/view/screens.go` (`card`, `buildUnlock`, `buildCreateVault`), `app/view/icons.go` (drop `lockIcon` if it becomes unused)
- Test: `app/view/screens_test.go`

**Interfaces:**
- Consumes: `shieldIcon` (`icons.go`), `tok(c)`, `radiusDialog`, `raisedShadow`.
- Produces: `card(c, body)` keeps its signature. Unlock, create vault and the hub's starting, restarting and failed screens all use it.

- [ ] **Step 1: Write the failing tests** (`screens_test.go`)
  - `TestUnlockCardCentred`: window 900×500, locked vault. The "Master password" field's centre X is within 1 of 450, and its width is at most 312 (360 − 2 × 24).
  - `TestCreateVaultErrorUnderFields`: with `a.createErr` set, the error text's top is below the "Both passwords match" line and above the "Create vault" button.
  - `TestCardOnSurface`: a 4×4 patch 8 px inside the card's top-left corner (the card is the parent column of the "Unlock vault" title; take its bounds from the field's bounds grown by 24) is within tol 2 of `tok` `Surface`, in both themes.

- [ ] **Step 2: Run them to verify they fail.** Expected: FAIL (the card is 420 wide on `Raised`; the error sits under the server list).

- [ ] **Step 3: Implement**
  - `card`: `Width(360)`, `Padding(24)`, `Gap(12)`, `Radius(radiusDialog)`, `Background(tok(c).Surface)`, `Border(1, tok(c).Line)`, `raisedShadow`, centred in the window. Its first child is the shield, `ui.Icon(c, shieldIcon).FontSize(32).TextColor(t.Accent).AlignSelf(ui.Center)`.
  - `buildUnlock`: the title row loses the lock icon. The title, the idle line, the auto-allow box with "Stop auto-allow and lock", Touch ID, the field, the error and the vault path keep their order and behaviour.
  - `buildCreateVault`: move the `createErr` text directly below the two checks.
  - If `lockIcon` is unused, remove it.

- [ ] **Step 4: Run the whole `./view` suite with `-race`.** Expected: `ok`. The Touch ID, unlock and create-vault flow tests are unchanged.

- [ ] **Step 5: Commit** — `feat(app): unlock and create vault on a centred 360 card with the shield`

---

### Task 5: Settings sections and screenshots

**Files:**
- Modify: `app/view/settings.go`, `app/view/shots_test.go`
- Test: `app/view/settings_test.go`

**Interfaces:**
- Consumes: `modal` (Task 2), `tok(c)`, `radiusCard`.

- [ ] **Step 1: Write the failing test.** `TestSettingsSectionsAndButtonOrder`, with Touch ID supported in the fake:
  - the headings "Unlock" and "Auto-allow" are shown;
  - each heading's section has a `tok` `Raised` patch (tol 2) under it, in the dark theme;
  - Save's centre X is less than Cancel's, at the same Y within 1;
  - "Cancel" is `Focused` in the first frame.

- [ ] **Step 2: Run it to verify it fails.** Expected: FAIL (no raised section).

- [ ] **Step 3: Implement.** Each section ("Unlock" when Touch ID is supported, then "Auto-allow") is:
  - a heading `Text`, bold, `FontSize(12)`, `TextColor(tok(c).Label)`;
  - above a `Column` with `Background(tok(c).Raised)`, `Radius(radiusCard)`, `Padding(12)` and `Gap(8)`, holding that section's fields, help text and notice.

  The notice's tints, the error, and the Save then Cancel row stay as they are, with the same keys, focus and delays.

- [ ] **Step 4: Screenshots.** Extend `TestShots` to write:
  - `unlock-light.png`, `unlock-dark.png` (locked; one host on auto-allow, so the amber box shows);
  - `settings-dark.png`;
  - `ai-light.png` (the column with a plain request, a sudo request, and a transfer).

  Run `SSHGATE_SHOTS=<dir> go test ./view -run 'TestShots$' -count=1 -v`. Expected: PASS and the files written. The controller looks at them.

- [ ] **Step 5: Run the whole `./view` suite with `-race`, plus `go vet ./...` and `gofmt -l .`.** Expected: `ok`, no output.

- [ ] **Step 6: Commit** — `feat(app): Settings in raised sections; screenshots of unlock, Settings and the AI column`
