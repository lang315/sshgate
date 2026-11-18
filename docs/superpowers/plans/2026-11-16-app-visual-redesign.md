# App Visual Redesign Implementation Plan (slices 1 and 2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the mygo app to the fork's latest `main`, then give it its own warm + teal theme, a left sidebar with a full-width top bar, designed host cards with the grant ring, and local motion.

**Architecture:** Slice 1 is a dependency bump in `app/go.mod`. Slice 2 adds one token file (`app/view/palette.go`) whose light and dark `ui.Theme` are installed with `ui.SetDefaultThemes` at startup, plus a small app token struct for colours `ui.Theme` lacks; `buildReady` is split into a top bar and a sidebar (`app/view/frame.go`); host cards are redrawn in `app/view/home.go`. No hub, protocol or vault change.

**Tech Stack:** Go, mygo `ui` (fork `github.com/lang315/mygo` at `3413a999e44f`), libghostty-vt terminal plugin.

**Spec:** `docs/superpowers/specs/2026-11-16-app-visual-redesign-design.md`

## Global Constraints

- `app/` only, plus PRODUCT.md and the impeccable surface brief. No hub, protocol or vault change.
- Colours come from named tokens; no view writes a raw colour (no `ui.Hex` outside `palette.go`).
- Every text/background pair passes 4.5:1 and every UI pair 3:1, both themes. `line` (card edges, dividers) is decoration and exempt.
- The app never calls the hub because of an animation, a timer or a redraw. No animation changes the size of a mounted terminal (each resize is a `term.resize`, `termconn/conn.go:221`).
- Sidebar show/hide snaps. AI column position snaps; only its content fades (120 ms, opacity).
- Safety rules unchanged: Deny default and keyboard-reachable; Allow, Send to tab, Trust, Enable, Resume, Overwrite all, Skip existing, Delete mouse-only with the 500 ms delay; "Deny all" always rendered; Stop auto-allow always visible on a card with a grant on or paused.
- View tests use the Tester's clock (`tt.Advance`), never a wall-clock wait. `view/term.go` keeps `NoClipboardWrite: true`.
- `Preferences.ReduceMotion`: mygo's `Animate`/`AnimateTo` already go to target; any motion the app drives itself checks `c.Preferences().ReduceMotion`.
- Each slice is its own branch and PR; the PR body carries command output (build, vet, test with exit status) and, for slice 2, screenshots in both themes (repo rule: no PR without evidence).

## Review Focus

1. **The 800 × 500 test window with the AI column open:** sidebar 200 + column 380 leaves 220 for the host area, narrower than one 240 card. Expect the card to shrink or scroll, never to overlap the column. Test in Task 4.
2. **A terminal tab open while the AI column or sidebar toggles:** at most one `term.resize` per toggle. Test in Task 3.
3. **Clicking a tab while on Hosts:** the sidebar hides, and the tab strip must not move (the next click lands where the user aimed). Test in Task 3.
4. **A timed grant longer than 4 h, or already expired but not yet swept:** the ring is full for > 4 h left and absent once `Until` has passed. Test in Task 4.
5. **System light/dark switch while running:** both themes are installed up front, so the window takes the other one in its next frame with no app code. Checked by hand in Task 5 (both-theme screenshots).

---

## Slice 1: mygo pin (branch `chore/mygo-pin-3413a99`, one PR)

### Task 1: Move the pin to fork `main` `3413a999e44f`

**Files:**
- Modify: `app/go.mod`, `app/go.sum`
- Modify (only if the build or tests break): files under `app/` the errors name

**Interfaces:**
- Produces: `app/go.mod` replace line `github.com/egoist/mygo => github.com/lang315/mygo <pseudo-version of 3413a999e44f>`. Slice 2 relies on `ui.Sidebar`, `ui.SidebarItem`, `ui.Badge`, `ui.SetDefaultThemes`, `Element.Shadow`, `Element.Animate`, `Path.Circle`/`Trim`/`Dash`.

- [ ] **Step 1: Bump**

Run (in `app/`): `go mod edit -replace github.com/egoist/mygo=github.com/lang315/mygo@3413a999e44f && go mod tidy`
Expected: `go.mod` shows a `v0.0.0-20261116…-3413a999e44f` pseudo-version. Never edit `/Users/lang/GolandProjects/github.com/lang315/mygo` (other sessions use it).

- [ ] **Step 2: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: exit 0 (it did on 2026-11-16 against a local copy).

- [ ] **Step 3: Rebuild libghostty-vt and run the full suite**

Run: `../scripts/build-ghostty-vt.sh build/natives && MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race ./...`
Expected: all packages `ok` (`./view` takes about 6 minutes). Fix any failure at its root; a fix that changes behaviour is out of scope, stop and report it. `TestHubNoClipboardWrite` must pass.

- [ ] **Step 4: Package**

Run: `./scripts/package-mac.sh`
Expected: exit 0, `app/out/sshgate.app` built, its own checks (codesign, hashes, `sshgate --version`) pass. Do not launch it.

- [ ] **Step 5: Commit and PR**

```bash
git add go.mod go.sum   # plus any fixed files
git commit -m "chore(app): pin mygo to fork main 3413a999e44f"
```
PR body: the output of Steps 2–4 with exit statuses. Update the memory file `mygo-fork-worktree.md` with the new pin after merge.

---

## Slice 2: tokens, shell, host cards (branch `feat/app-redesign-shell`, one PR, on top of slice 1)

### Task 2: Direction contract and tokens

**Files:**
- Create: `app/view/palette.go`
- Create: `app/view/palette_test.go`
- Modify: `app/cmd/sshgate-app/main.go:54` (install themes next to the existing `mygo.Theme.SetSource`)
- Create: the impeccable surface brief for `app/view/home.go`

**Interfaces:**
- Produces:
  - `type tokens struct { Bg, Surface, Raised, SurfaceHover, Line, ControlLine, Divider, Text, Muted, Label, Accent, AccentHover, AccentPressed, AccentText, AccentTint, Danger, DangerFill, DangerTint, Wait, WaitTint, Ok, OkTint, TermBg, TermFg, Rail, RailText ui.Color }`
  - `func palette(dark bool) tokens`
  - `func tok(c *ui.Context) tokens` — `palette(c.Theme().Dark)`
  - `func Themes() (light, dark *ui.Theme)` — exported for `main.go`
  - Radii and shadows: `const radiusControl, radiusCard, radiusDialog = 6, 10, 14`; `func cardShadow(c *ui.Context) ui.Shadow`, `func raisedShadow(c *ui.Context) ui.Shadow` (values builder's call; dark shadows deeper).

- [ ] **Step 1: Write the surface brief**

Run `"/Users/lang/.claude/plugins/cache/impeccable/impeccable/4.8.2/skills/impeccable/scripts/impeccable" surface-brief write app/view/home.go <body-file> app/view/frame.go` with the six blocks of `reference/new-work.md` §5 (THESIS, OWN-WORLD, STORY, FIRST VIEWPORT, FORM, FINISH). FORM: `seed: none (pinned)` and the author's words "Giữ ấm + teal, làm đậm hơn", "Sidebar trái như Termius". Mode Operate, code-led. Then read `reference/craft-floor.md` before Step 3.

- [ ] **Step 2: Write the failing tests** in `palette_test.go`

```go
func lum(c ui.Color) float64  // WCAG relative luminance of c's R, G, B
func ratio(a, b ui.Color) float64

func TestPaletteContrast(t *testing.T) {
	for _, dark := range []bool{false, true} {
		p := palette(dark)
		for _, c := range []struct {
			name   string
			fg, bg ui.Color
			min    float64
		}{
			{"text/bg", p.Text, p.Bg, 4.5}, {"text/surface", p.Text, p.Surface, 4.5}, {"text/raised", p.Text, p.Raised, 4.5},
			{"muted/bg", p.Muted, p.Bg, 4.5}, {"muted/surface", p.Muted, p.Surface, 4.5}, {"muted/raised", p.Muted, p.Raised, 4.5},
			{"label/surface", p.Label, p.Surface, 4.5},
			{"accent/surface", p.Accent, p.Surface, 4.5}, {"accent/accentTint", p.Accent, p.AccentTint, 4.5},
			{"accentText/accent", p.AccentText, p.Accent, 4.5},
			{"danger/surface", p.Danger, p.Surface, 4.5}, {"danger/dangerTint", p.Danger, p.DangerTint, 4.5},
			{"white/dangerFill", ui.Hex("#ffffff"), p.DangerFill, 4.5},
			{"wait/surface", p.Wait, p.Surface, 4.5}, {"wait/waitTint", p.Wait, p.WaitTint, 4.5},
			{"ok/surface", p.Ok, p.Surface, 4.5}, {"ok/okTint", p.Ok, p.OkTint, 4.5},
			{"termFg/termBg", p.TermFg, p.TermBg, 4.5},
			{"railText/rail", p.RailText, p.Rail, 4.5},
			{"controlLine/bg", p.ControlLine, p.Bg, 3}, {"controlLine/surface", p.ControlLine, p.Surface, 3}, {"controlLine/raised", p.ControlLine, p.Raised, 3},
			{"accent/bg (focus)", p.Accent, p.Bg, 3},
		} {
			if r := ratio(c.fg, c.bg); r < c.min {
				t.Errorf("dark=%v %s: %.2f < %.1f", dark, c.name, r, c.min)
			}
		}
	}
}

// Every ui.Color field of each theme differs from the stock theme, except
// the ones listed as kept on purpose.
func TestThemesOverrideStock(t *testing.T) // reflect over ui.Theme's ui.Color fields; kept := map[string]bool{...}
```

Run: `go test ./view -run 'TestPalette|TestThemes' -v` — Expected: FAIL, `palette` undefined.

- [ ] **Step 3: Implement `palette.go`**

Values (dark / light). From the spec: Bg `#14171A`/`#f5f6fa`, Surface `#1A1E22`/`#ffffff`, Raised `#21262B`/`#eef0f6`, Line `#3A4148`/`#dde0ea`, Divider `#2B3137`/`#e6e8ef`, Text `#ECE8E0`/`#1b1f2e`, Muted `#8B908E`/`#62698a`, Label `#B3B5B1`/`#474e6b`, Accent `#56BFB9`/`#12766F`, AccentTint `#16312F`/`#dcf1ec`, Danger `#F28B73`/`#B5412F`, DangerFill `#B5412F` both, DangerTint `#3D1D18`/`#fbe2dc`, Wait `#E9A23B`/`#8B6123`, WaitTint `#3A2C14`/`#fcefdc`, Ok `#6CC58F`/`#3F7A57`, TermBg `#0E1012`/`#ffffff`, TermFg `#D8D4CC`/`#1b1f2e`, Rail `#0F2A29`/`#0F5F59`, RailText `#E6F2F0` both, AccentText `#0E1012`/`#ffffff`.
Starting values, tune until the test passes and report the final ones in the PR: ControlLine `#737B82`/`#7C8196`, SurfaceHover `#282E34`/`#e6e9f1`, AccentHover `#6CCAC4`/`#0F6A64`, AccentPressed `#45A9A3`/`#0C5A55`, OkTint `#1A3324`/`#dff1e6`.

`Themes()` starts from `ui.LightTheme()`/`ui.DarkTheme()` and sets, per the spec's table: `Background`, `Field` = Bg; `Surface` = Raised (the face of a button); `SurfaceHover` = SurfaceHover; `SurfacePressed` = Line; `Border` = Line; `ControlBorder`, `ButtonBorder` = ControlLine; `Text`; `TextMuted` = Muted; `Accent`, `AccentHover`, `AccentPressed`, `AccentText`; `Focus` = Accent; `InactiveAccent` = Muted; `Selection`, `RowSelected` = AccentTint; `RowSelectedText` = Text; `RowHover` = Raised; `Danger`, `Warning` = Wait, `Success` = Ok; `Inverse` = Text, `InverseText` = Bg. `Radius` = `radiusControl`.

In `main.go`, before the window is made: `ui.SetDefaultThemes(view.Themes())`.

- [ ] **Step 4: Run tests** — `go test ./view -run 'TestPalette|TestThemes' -v` — Expected: PASS.

- [ ] **Step 5: Replace alpha tints** in `home.go` (`t.Danger.Alpha(0.15)` → `tok(c).DangerTint`, `t.Warning.Alpha(0.15)` → `tok(c).WaitTint`) and any other `.Alpha(` background in `app/view/*.go` that stands for a tint; run `go test ./view` — Expected: PASS.

- [ ] **Step 5b: Terminal colours.** If `terminal.TerminalOptions` (used in `view/term.go`) takes a background/foreground or theme, set `TermBg`/`TermFg`; if not, leave the terminal as is and say so in the PR. `NoClipboardWrite: true` stays.

- [ ] **Step 6: Commit** — `git commit -m "feat(app): warm + teal theme tokens installed with SetDefaultThemes"`

### Task 3: Shell — full-width top bar and sidebar

**Files:**
- Create: `app/view/frame.go`
- Create: `app/view/frame_test.go`
- Modify: `app/view/home.go` (`buildReady`: the top `ui.Row` and the Hosts/Audit buttons move to `frame.go`)
- Modify: `app/view/icons.go` (add `hostsIcon`, `auditIcon`, `shieldIcon`)
- Modify: `app/view/tabs_test.go:125` (`Click("Hosts")` while a terminal is active → `Click("Home")`), `:310` (`Find("Hosts")` → `Find("Home")`)
- Modify: `PRODUCT.md` (Platform: native macOS app, not `web`; "Termius-like structure (host cards, top tabs)" → "(left sidebar, host cards, terminal tabs in a full-width top bar)"; Keychain moves from "not to be designed" to "planned, own spec")

**Interfaces:**
- Consumes: `tok`, `raisedShadow` (Task 2); `model.TabSet.ShowHome`, `ShowAudit`, `Active`, `model.AuditTab`.
- Produces:
  - `func (a *App) sidebarShown() bool` — `a.tabs.Active == "" || a.tabs.Active == model.AuditTab`
  - `func (a *App) buildTopBar(c *ui.Context, autoN, liveAuto int)` — on `Rail`: home button (fixed width 200, `shieldIcon` + "sshgate", accessible label "Home", click → `a.tabs.ShowHome()`), `a.buildTabStrip(c)`, the AI button, Lock (both moved unchanged in behaviour).
  - `func (a *App) buildSidebar(c *ui.Context)` — when `sidebarShown()`: a column width 200 on `Rail` holding `ui.Sidebar(c, &sel, …)` with items `"hosts"` ("Hosts") and `"audit"` ("Audit"), `sel` derived from `a.tabs.Active`, `Changed` → `ShowHome`/`ShowAudit`; under it `a.buildThemeControl(c)` and the Settings button (moved from the top bar, same `Disabled` rule). Selected item: lighter teal pill drawn on the item, never via `Theme.RowSelected`.

- [ ] **Step 1: Write failing tests** in `frame_test.go`

```go
func TestSidebarChoosesHostsAndAudit(t *testing.T)   // readyApp; clickT "Audit" → a.tabs.Active == model.AuditTab; clickT "Hosts" → Active == ""
func TestSidebarHiddenWithTab(t *testing.T)          // tabApp; open box; tt.Find("Audit") not found; Click("Home") → HasText("Search hosts") and Find("Audit") found
func TestTabStripDoesNotMoveWhenSidebarHides(t *testing.T) // tabApp; open box, ShowHome; r1 := Find("box"); Click("box"); r2 := Find("box"); r1.X == r2.X
func TestToggleDoesNotResizeTerminalPerFrame(t *testing.T) // tabApp; open box; count "term.resize" in hd.calls; toggle AI column twice and Home/box; Advance 1 s; at most one new term.resize per toggle
func TestLockReachableWithSidebarHidden(t *testing.T)      // tabApp; open box; Find("Lock") inside the 800-wide window
```

Run: `go test ./view -run 'TestSidebar|TestTabStrip|TestToggle|TestLockReachable' -v` — Expected: FAIL.

- [ ] **Step 2: Implement `frame.go`** and reduce `buildReady` to: `buildTopBar`, then a row of `buildSidebar` + the existing work column + editor/import + AI column. AI column: no slide; its content wrapped in an element whose `Opacity` animates 0 → 1 over 120 ms on open (`Animate`).

- [ ] **Step 3: Update `tabs_test.go`** lines 125 and 310 as listed above.

- [ ] **Step 4: Run** `MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race ./view` — Expected: PASS. Any other test that clicked Hosts/Audit/Settings/theme while a tab was active gets `Home` first; no test's assertion changes meaning.

- [ ] **Step 5: Commit** — `git commit -m "feat(app): sidebar and full-width top bar"`

### Task 4: Host cards and the grant ring

**Files:**
- Create: `app/model/grantring.go`, `app/model/grantring_test.go`
- Modify: `app/view/home.go` (`buildHosts` card body)
- Modify: `app/view/home_test.go` or create `app/view/cards_test.go`

**Interfaces:**
- Consumes: `tok`, `cardShadow`, `raisedShadow`, `radiusCard` (Task 2); `a.tabs.OpenCount(server) int` (existing) as "connected".
- Produces:
  - `type Ring struct { Show, Dashed bool; Frac float64 }`
  - `func GrantRing(a *hubapi.AutoAllow, now time.Time) Ring` — nil → `{}`; `Paused` → `{Show, Dashed, Frac: 1}`; `Forever` → `{Show, Frac: 1}`; timed → `Frac = min(1, left/4h)`, `{}` when `Until` is past or unparsable.

- [ ] **Step 1: Write the failing table test** `TestGrantRing`: nil → `Ring{}`; forever → `{true,false,1}`; paused → `{true,true,1}`; 1 h left → Frac 0.25; 15 min left → 0.0625; 6 h left → 1; `Until` 1 s ago → `Ring{}`; `Until` "junk" → `Ring{}`.

Run: `go test ./model -run TestGrantRing -v` — Expected: FAIL.

- [ ] **Step 2: Implement `GrantRing`.** Run the test — Expected: PASS.

- [ ] **Step 3: Write failing view tests**

```go
func TestCardConnectedTile(t *testing.T)      // tabApp; open box; ShowHome; Find("connected") (the dot's label) found; close tab → not found
func TestCardFitsBesideAIColumn(t *testing.T) // readyApp with 3 hosts; openAI; card "Open host1" box right edge ≤ AI column's left edge
func TestStopAutoAllowStillOnCard(t *testing.T) // a host with a paused grant; "Stop auto-allow <name>" found on the card
```

- [ ] **Step 4: Redraw the card** per the spec's Host cards section: `Surface` background, `radiusCard`, `cardShadow` animating to `raisedShadow` on hover (120 ms), 36 px solid tile (Accent, or Ok with a dot labelled "connected" while `OpenCount > 0`) with the host letter in `AccentText`, the ring drawn around the tile with `Path.Circle` + `Trim(0, Frac)` (+ `Dash` when `Dashed`) in `Wait`, redrawn on the existing 15 s `c.After`; chips as tinted pills (`AI` Accent on AccentTint, `New key` Wait on WaitTint, tunnels Accent on AccentTint); action icons Muted at rest, Text on card hover. Width 240 with `MinWidth` small enough that Review Focus 1 holds.

- [ ] **Step 5: Run** `go test -race ./view ./model` — Expected: PASS.

- [ ] **Step 6: Commit** — `git commit -m "feat(app): host cards with tile, chips and grant ring"`

### Task 5: Motion, screenshots, PR

**Files:**
- Modify: `app/view/home.go` (view change)
- Create: `app/view/shots_test.go` (opt-in)

- [ ] **Step 1: View change.** Hosts ↔ Audit: the entering view fades in (opacity 0 → 1, 120 ms, `Animate`). If a leaving view would stay in layout during a cross-fade, fade only the entering one (spec, Left to the builder). Hover/press colour fades come from `ui.Theme` and need no app code. Run `go test ./view` — Expected: PASS.

- [ ] **Step 2: Screenshot test.** `TestShots` skips unless `SSHGATE_SHOTS=<dir>`; renders `readyApp` with 4 hosts (one timed grant, one paused, one unpinned, one with an open tab) at 1000 × 640 in light and dark (`ui.TesterOptions` theme, or `SetTheme`), with the AI column open on one request, and writes `tt.Image()` as PNG: `hosts-light.png`, `hosts-dark.png`, `audit-dark.png`, `terminal-dark.png`.

Run: `SSHGATE_SHOTS=$TMPDIR/shots go test ./view -run TestShots -v` — Expected: PASS, four PNGs. Look at each; fix what one look shows, look once more (two rounds max).

- [ ] **Step 3: Full checks.** `go vet ./... && MYGO_GHOSTTY_VT=$PWD/build/natives/libghostty-vt.dylib go test -race ./... && ./scripts/package-mac.sh` — Expected: exit 0 each.

- [ ] **Step 4: Commit and PR.** PR body: final token values that differ from the starting ones, the command output of Step 3 with exit statuses, the four screenshots, and a by-hand line for Reduce Motion on/off in the packaged app.

---

## Slices 3 and 4 (outline; each gets its own detailed plan when slice 2 merges)

**Slice 3 — AI column, unlock, create vault, Settings.** A `modal(c, …)` helper in `app/view` wrapping `ui.Modal` with `Surface`, `radiusDialog`, `raisedShadow`; all nine `ui.Modal` calls move to it. Request cards on `Raised`, command block on `TermBg`, sudo red left edge + `SUDO` chip (DangerTint), Deny filled `DangerFill`, Allow green outline; a new card fades in (opacity only) and still restarts the Allow delay. Unlock/create vault: centred 360 card on `Bg` with the shield, teal primary button; Stop auto-allow and lock unchanged. Dialog fade + scale 0.98 → 1, 160 ms, decision buttons at their final position from the first frame. Tests: every existing safety test unchanged in meaning; a test that an arriving request's Allow stays disabled for 500 ms after its fade starts.

**Slice 4 — Editor, Import, Files, Tunnels, Audit, host-key dialogs, hub starting/failed** on the same tokens; then the impeccable finish: screenshots of every screen in both themes, the finish reviewer, the documenter for `DESIGN.md` and `.impeccable/design.json`.

**Fallback C** (any slice): if a needed look is unreachable through `ui.Theme` (for example a control's shape), write an `sshgate` skin in the fork as a PR to fork `main` from a separate worktree, and record the switch in the spec.
