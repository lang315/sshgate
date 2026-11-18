# App visual redesign (mygo app)

Date: 2026-11-16. Status: approved by the author on 2026-11-16.

## Intent

The mygo app in `app/` works but looks plain: default theme colours, one row of
buttons at the top, no depth, almost no motion. The author uses Termius beside
it and wants sshgate to reach that level of colour, layout and effect, so that
the app is a pleasure to keep open all day. The Electron app had a full visual
system (spec `2026-09-25-desktop-ui-redesign-design.md`); the port to mygo
(spec `2026-10-14-mygo-desktop-design.md`) kept the behaviour and lost the look.

A Keychain feature follows this work as its own project. This redesign gives it
a place (a sidebar entry) but does not build it.

## Goals

- The app's own colours: a cool neutral in both themes with a teal accent
  (2026-11-17; before that, warm-neutral dark and its light pair inside a teal
  shell, as the author chose on 2026-09-25 and confirmed on 2026-11-16).
- A left sidebar for main navigation, as Termius has.
- Host cards, the AI approval column, and the unlock / create vault / Settings
  screens look designed: colour that carries state, depth, clear hierarchy.
- Motion on navigation, panels, dialogs, hover and press.
- The latest `lang315/mygo` (`main`), so the app can use `ui.Sidebar`,
  `ui.Badge` and the newer animation calls.

## Non-goals

- Keychain, snippets, known-hosts screen, a global port-forwarding screen.
  Tunnels stay per host. The sidebar shows only what exists (PRODUCT.md
  principle 4).
- Any change to hub, vault, protocol, or to what a screen does.
- Windows or Linux.
- A skin in the mygo fork (see Approaches, C).

## Anti-goals

The result fails, however good it looks, if any of these happens:

- Allow becomes easier than Deny, or anything moves under the cursor near a
  decision button. Every rule in PRODUCT.md "Capabilities and Constraints" and
  in the 2026-09-25 spec's Safety section holds unchanged.
- The app calls the hub because of an animation, a timer or a redraw (the idle
  lock would never fire). Motion is local only. A terminal's width change sends
  `term.resize` to the hub (`termconn/conn.go`), so no animation may change the
  size of a mounted terminal: see Motion grammar.
- The terminal loses space. With a terminal or Files tab active, the sidebar is
  hidden.
- A secret is displayed.

## Constraints

- `app/` only, plus PRODUCT.md and, at the end, DESIGN.md.
- Colours come from named tokens; no view writes a raw colour.
- Every text/background pair passes 4.5:1 and every UI pair 3:1, in both
  themes. The 2026-09-25 token table already passed; any new pair is checked by
  a test.
- View tests use the Tester's clock: `tt.Advance`, never a wall-clock wait.
  Animated content has no size until then.
- `Preferences.ReduceMotion` is honoured: mygo's animation calls already go
  straight to rest; any motion the app drives itself must check it.
- `view/term.go` keeps `NoClipboardWrite: true`.

## Approaches

| | Approach | Trade-off |
|---|---|---|
| A | The fork's skin: `skin.Use(skin.Calm.WithPalette(skin.Teal))` | Fastest. The ground is Calm's neutral grey, not the warm one the author chose. |
| **B** | **The app sets its own `ui.Theme` pair with `ui.SetDefaultThemes`, plus app tokens for what `ui.Theme` lacks** | **Exact colours, no fork change. The app carries its own elevation and timing values.** |
| C | A new `sshgate` skin inside the fork (skin tokens live in an `internal` package there) | Exact colours and the skin's full token set, but two repositories. |

The author chose **B**. **C is the fallback**: if B cannot reach something the
skin tokens give (for example a control's shape that `ui.Theme` does not
expose), write the skin in the fork then and switch the app to it. Record that
decision in this spec when it is made.

## Design

### Tokens (`app/view/palette.go`, new)

One function builds the light and dark `ui.Theme`, called once at startup with
`ui.SetDefaultThemes`.

**Palette note, 2026-11-18: carbon.** The author chose the "carbon" palette
after seeing real screenshots. It replaced the cool-neutral-plus-teal look
(2026-11-17), whose notes below are kept as history. Greys are true neutral
(dark `#1E1E1E`, light `#F5F5F5`); the accent is near-white in dark and
near-black in light; colour is for meaning only: amber waits or auto-allow, red
is danger, green is ok or connected, plus the six tile hues (teal `#2F7D78` is
one of them now). A switch is on in green (`SwitchOn`, a scoped theme). The
app icon replaces the outline shield as the home mark and the card mark. The
table in `app/view/palette.go` and `DESIGN.md` is the source of the values.

**Palette note, 2026-11-17: cool neutral.** The author asked for "một màu
trung tính hơi lạnh, không quá đậm như hiện tại" (a slightly cool neutral, not
as heavy as now): the cool neutral replaced the warm-neutral-plus-teal look
(warm grounds, a deep-teal top bar and sidebar). Neutrals are a low-chroma
blue-grey in both themes; the rail is a neutral step with a 1 px `divider`
edge, not teal; teal is the accent only (primary buttons, focus, the chosen
sidebar item, the home mark's shield, links); tile hues and state colours are
muted a little; after the finish review the open tab got a 2 px accent bar,
dialogs a 1 px `line` border, the Keychain key tile the accent tint, and the
light rail buttons a white face. `palette.go` and DESIGN.md hold the full set; the table below
is the current one (old value in brackets):

| Token | Dark | Light | `ui.Theme` field |
|---|---|---|---|
| bg | `#16181B` (`#161514`) | `#F4F5F7` (`#F6F4F0`) | `Background`, `Field` |
| surface | `#1C1F23` (`#1C1A19`) | `#FCFCFD` (`#FFFDFB`) | none: cards, columns and dialogs paint it themselves |
| raised | `#23272C` (`#23211F`) | `#EDEFF2` (`#EEEBE5`) | `Surface` (the face of a button; so a button on a card is visible), `RowHover` |
| line | `#363B42` (`#3D3A37`) | `#DADEE3` (`#DFDAD2`) | `Border` (card edges and dividers: decoration, exempt from 3:1) |
| controlLine | `#737B86` (`#7A746D`) | `#7D8590` (`#857E73`) | `ControlBorder`, `ButtonBorder` (mygo's 3:1 border, `ui/theme.go:36`) |
| text | `#E5E8EC` (`#ECE8E0`) | `#1A1E23` (`#1F1C19`) | `Text` |
| muted | `#959CA7` (`#9A948C`) | `#5D6570` (`#6B655D`) | `TextMuted` |
| accent | `#55B8B2` (`#56BFB9`) | `#0F726D` (`#12766F`) | `Accent`, `Focus` |
| danger | `#EE8A74` (`#F28B73`) | `#B5412F` | `Danger` |
| wait | `#E0A048` (`#E9A23B`) | `#87601F` (`#8B6123`) | `Warning` |
| ok | `#6CC08F` (`#6CC58F`) | `#3B7352` (`#3D7554`) |
| rail | `#1D2024` (`#134542`) | `#EBEDF0` (`#0F5F59`) | none |
| railText | `#BFC5CD` (`#E6F2F0`) | `#3F4650` (`#E6F2F0`) | none |
| termBg | `#0F1114` (`#100F0E`) | `#FCFCFD` (`#FFFDFB`) | none |
| waitFill / onWaitFill | `#E9A23B` / `#0E1012` | `#9A5800` / white (`#E9A23B` / `#0E1012`) | none |
| railButton | `#2F3237` (was an 11 % railText pill) | `#FCFCFD` (was the same pill) | none |
| dangerTint | `#3B1D21` (`#3D1D18`) | `#FBE3DE` (`#fbe2dc`) | none |
| tile hues | plum `#7E4F86`, blue `#3D67A8`, violet `#6A55A8`, rose `#A44C68`, amber-brown `#8A6230`, slate `#56657A`, both themes (teal dropped: teal is the accent) | same | none |
| dangerFill | `#B9432F` (`#B5412F`) | same | none | `Success` |

The remaining `ui.Theme` fields are set too, never left at the stock theme's
values: `AccentHover` and `AccentPressed` (accent lightened / darkened one
step), `SurfaceHover` (one step above `raised`), `AccentText` (dark `#0E1012` on the dark theme's teal, white on the
light theme's; this is the label of a filled teal button), `SurfacePressed`
(`line`), `Selection` and `RowSelected` (`accentTint`), `RowSelectedText`
(`text`), `InactiveAccent` (`muted`), `Inverse` / `InverseText` (`text` /
`bg`). A test fails when a colour field of either theme still equals the stock
`ui.LightTheme()` / `ui.DarkTheme()` value by accident; the builder lists any
field kept on purpose. The desktop's accent colour does not replace the
theme's (`ui/theme.go`, `SetDefaultThemes`), so teal holds.

Tokens with no `ui.Theme` field live in an app struct picked by `c.Theme().Dark`:
`divider`, `label`, `accentTint`, `dangerFill`, `dangerTint`, `waitTint`,
`termBg`, `termFg`, `rail` and `railText` for the sidebar and top bar, and
`okTint`. The existing
`t.Danger.Alpha(0.15)` and `t.Warning.Alpha(0.15)` banner backgrounds become
`dangerTint` and `waitTint`.

**Where colour is stronger than in the 2026-09-25 table** (the author asked
for a bolder look, and that table alone is grey with one accent):

- (Superseded 2026-11-17.) The shell was teal: `rail` a deep teal (dark
  `#134542`, light `#0F5F59`) with `railText` `#E6F2F0`. The author found the
  block too heavy; the shell is now a neutral step (`rail` `#1D2024` /
  `#EBEDF0`, `railText` `#BFC5CD` / `#3F4650`) with a 1 px `divider` under the
  top bar and beside the sidebar, and the home mark is a teal shield with the
  name in `text`, with no face at rest, hovered or while Home shows.
- Host tiles are filled, not outlined: the letter sits on a solid tile in the
  host's own muted hue (no teal since 2026-11-17), green while that host has an
  open terminal.
- Chips, banners and the request cards' edges use the tints at full strength
  instead of 15 % alpha.

The builder may adjust these hex values to pass contrast, and reports the final
ones.

Colour jobs are law: teal = focus, links, selection, AI-visible; amber =
waiting on the user or auto-allow on; red = deny and danger; green = connected
and allow.

Shape and depth: radius 6 for controls, 10 for cards, 14 for dialogs. Two
shadows: card (rest) and raised (hover, dialogs, the AI column's left edge).
Type: system font; sizes 12 caption, 13 body, 15 heading, 20 title; monospace
for addresses, commands and fingerprints.

### Shell

```
┌──────────────────────────────────────────────────────────┐
│ ⛨ sshgate │ [web-1 ×] [db ×] [web-1 files ×]   AI 2  🔒 │  top bar, full width
├──────────┬───────────────────────────────────────────────┤
│          │  Search hosts…        [New host] [Import]     │
│ ▣ Hosts  │                                               │
│ ◷ Audit  │  Hosts · 6                                    │
│          │  ┌────────┐ ┌────────┐ ┌────────┐            │
│          │  │W web-1 │ │D db    │ │S stage │            │
│          │  │AI  ◔2h │ │New key │ │        │            │
│          │  └────────┘ └────────┘ └────────┘            │
│ ☾ ☀ Auto │                                               │
│ ⚙ Settings│                                              │
└──────────┴───────────────────────────────────────────────┘
```

- **Top bar**, across the whole window, on `rail`: at the left a fixed-width
  home button (the shield mark and "sshgate", as wide as the sidebar), then the
  terminal and Files tabs, then the AI button and Lock at the right. The tab
  strip starts at the same x whether the sidebar is shown or not, so a tab
  never moves under the cursor when the sidebar hides. Hosts and Audit buttons
  leave the bar.
- **Sidebar** (`ui.Sidebar`, width 200, `rail` background), below the top bar:
  items Hosts and Audit; at the bottom the theme control and Settings.
  Keychain is added by its own project. `ui.Sidebar` is one scroll with no
  footer (`ui/sidebar.go:43`), so the theme control and Settings are ordinary
  buttons in a wrapper column under it, on the same `rail`. The selected item
  is the `accentTint` row (the theme's `RowSelected`, already `accentTint`)
  with its icon and label in `accent`, through a theme scoped to that one item
  (`accentTheme`); the other items are `railText` (2026-11-17; before, a
  lighter teal pill).
- **Sidebar and tabs**: while a terminal or Files tab is active the sidebar is
  not shown; the home button, or choosing Hosts or Audit, shows it again. It
  appears and disappears without animation (see Motion grammar).
- **AI column**: unchanged position (right, 380 wide), now on `surface` with
  the raised shadow.
- **Dialogs**: `ui.Modal` paints its panel with `Theme.Background` and its own
  radius (`ui/widgets.go:749`). One helper in `app/view` wraps `ui.Modal` and
  sets `surface`, radius 14 and the raised shadow; all nine modals use it.

### Host cards

Width 240, `surface`, card shadow, radius 10. A 36 px solid tile with the
host's letter: teal, or green with a small "connected" dot while the host has
an open terminal tab. The source is the app's own tab list; no hub call. Name bold, address monospace muted. Chips are
real chips (tinted pill, coloured text): `AI` teal, `New key` amber, tunnels
teal. Hover: the card lifts to the raised shadow and the action icons go from
muted to full. The Stop auto-allow button is always visible while a grant is on
or paused, as now.

**Grant ring (removed)**: ring removed 2026-11-17 at the author's request; the chip carries the grant. A host on auto-allow shows the Auto chip only. The same amber is on the AI button when
requests wait. One glance at the Hosts view tells which servers an AI can act
on right now and for how long.

### AI approval column

Request cards on `raised`, radius 10. Command in a monospace block on `termBg`.
A sudo request has a red left edge and a `SUDO` chip; a transfer shows root,
sources and destination as now. Deny is the filled red button; Allow is the
green outline. Order, sizes, defaults, the 500 ms delay and "Deny all always
rendered" do not change. No element in the list animates position or size; a
new card may fade in (opacity only), and that arrival restarts the Allow delay
as it does today.

### Unlock, create vault, Settings

Unlock and create vault: a centred card (width 360) on `bg` with the shield
mark above it, title, one field, primary button filled teal. **Stop auto-allow
and lock** stays as it is: no password, no delay. Errors in red under the
field. Settings: a dialog with section headings and rows; same fields and Save
order as now.

### Motion grammar

| Where | Motion | Time |
|---|---|---|
| Hover, press, selection colour | colour fade | 120 ms |
| Card hover | shadow card → raised | 120 ms |
| Sidebar hide / show | none: it snaps | 0 |
| AI column open / close | position snaps; content fades in (opacity only) | 120 ms |
| Dialogs and editor sheet | fade + scale 0.98 → 1 | 160 ms |
| View change (Hosts ↔ Audit) | cross-fade | 120 ms |

The two snaps are the rule from Anti-goals: an animated width would resize a
mounted terminal on every frame, and each resize is a `term.resize` to the hub
and a resize of the remote PTY. A test opens a terminal tab, toggles the AI
column and the sidebar, and asserts one resize per toggle at most. The AI
column never slides for a second reason: its Allow buttons would move under
the cursor. Its Allow delay starts at mount, as now.

mygo's `Animate` / `AnimateTo` go to their target at once under
`Preferences.ReduceMotion` (their doc comments in `ui/api_gen.go`); opacity
fades marked `NonMotion` keep running, which is intended.

Nothing animates inside the approvals list except a new card's opacity.
Dialog buttons that follow the Allow rules (Trust, Enable, Resume, Overwrite
all, Skip existing, Delete) are at their final position from the first frame:
the dialog's scale is from its centre and at most 2 %, and their delay starts
when the dialog mounts, as now.

## Slices, one pull request each

1. **mygo pin** to the fork's `main`. `go build ./...` and `go vet ./...` in
   `app/` already pass against it (checked 2026-11-16); the slice runs the full
   test suite, including the terminal tests with `MYGO_GHOSTTY_VT` (the fork's
   terminal plugin may want a newer libghostty-vt: rebuild it with
   `scripts/build-ghostty-vt.sh`), then `scripts/package-mac.sh` (the
   `mygo_noinspector` build, the dylib hash), and fixes what breaks. No visual
   change intended.
2. **Tokens and shell**: `palette.go`, sidebar, top bar, host cards, grant
   ring, view-change and hover motion. PRODUCT.md: platform line corrected to
   native macOS, the "top tabs" preference updated to the sidebar, Keychain
   moved from "not to be designed" to "planned, own spec".
3. **AI column, unlock, create vault, Settings**, AI column and dialog motion.
4. **Editor, Import, Files, Tunnels, Audit, host-key dialogs, hub
   starting/failed screens** on the same tokens. DESIGN.md written from the
   built app.

Each slice updates the view tests it breaks (18 test files refer to the Hosts,
Audit or Lock controls) and ends with screenshots of the app in both themes in
the PR body.

## Design process (impeccable)

The author asked for the impeccable skill. Its playbook still requires:

- Before slice 2's first UI edit: a direction contract in a surface brief
  (`impeccable surface-brief write`), with `seed: none (pinned)` and the
  author's words: "Giữ ấm + teal, làm đậm hơn", "Sidebar trái như Termius".
  The surface is Operate, so the build is code-led: no image comps.
- `reference/craft-floor.md` read immediately before any UI edit.
- At the end of slice 4: screenshots of every screen in both themes, the
  finish reviewer, then the documenter for DESIGN.md and
  `.impeccable/design.json`.

## Testing

- A palette test: every token pair used for text or UI passes its contrast
  ratio, both themes.
- Shell tests: sidebar chooses Hosts and Audit; opening a tab hides it; the
  back button shows it.
- Grant ring: removed, with its `model` function (2026-11-17).
- All existing safety tests pass unchanged in meaning: Allow delay, mouse-only
  buttons, Deny default, "Deny all" always rendered.
- By hand per slice: the packaged app, both themes, Reduce Motion on and off.

## Open calls (the builder's, for the author to check)

- Sidebar width 200 and hiding it fully (not an icon rail) while a tab is
  active.
- A teal shell (`rail`) as the main way the app gets bolder (superseded 2026-11-17 by the cool-neutral shell).
- "Connected" on a card means this app has a terminal tab open on the host.
- Theme control and Settings at the bottom of the sidebar; Lock stays in the
  top bar so it is reachable with the sidebar hidden.
- The grant ring (removed 2026-11-17).
- The AI column does not slide; only its content fades in.
- A separate, darker border colour for buttons and inputs (3:1), while card
  edges keep the soft `line`.
- The shadow values.

## Left to the builder

Whether the terminal plugin's theme takes `termBg` / `termFg`, whether a
leaving view stays in layout during the cross-fade (if it does, no cross-fade:
fade the entering view only), exact spacing, the shadow values, icon drawings for Hosts and Audit, how the
sidebar's selected item is drawn within what `ui.Sidebar` allows, and the names
of the new functions.
