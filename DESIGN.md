---
name: sshgate
description: A native macOS gate between an AI agent and real servers; a calm cool-neutral app with one teal accent, where every command waits for a human.
colors:
  bg-light: "#F4F5F7"
  surface-light: "#FCFCFD"
  raised-light: "#EDEFF2"
  surface-hover-light: "#E4E7EB"
  line-light: "#DADEE3"
  control-line-light: "#7D8590"
  divider-light: "#E3E6EA"
  text-light: "#1A1E23"
  muted-light: "#5D6570"
  label-light: "#3F4650"
  accent-light: "#0F726D"
  accent-hover-light: "#0C6662"
  accent-pressed-light: "#0A5754"
  accent-text-light: "#FCFCFD"
  accent-tint-light: "#DCEFEC"
  danger-light: "#B5412F"
  danger-tint-light: "#FBE3DE"
  wait-light: "#87601F"
  wait-tint-light: "#FBEFDC"
  ok-light: "#3B7352"
  ok-tint-light: "#E6F4EC"
  term-bg-light: "#FCFCFD"
  term-fg-light: "#1A1E23"
  rail-light: "#EBEDF0"
  rail-text-light: "#3F4650"
  rail-button-light: "#FCFCFD"
  wait-fill-light: "#9A5800"
  wait-fill-hover-light: "#864C00"
  on-wait-fill-light: "#FFFFFF"
  bg-dark: "#16181B"
  surface-dark: "#1C1F23"
  raised-dark: "#23272C"
  surface-hover-dark: "#2B2F35"
  line-dark: "#363B42"
  control-line-dark: "#737B86"
  divider-dark: "#2A2E34"
  text-dark: "#E5E8EC"
  muted-dark: "#959CA7"
  label-dark: "#B3B9C2"
  accent-dark: "#55B8B2"
  accent-hover-dark: "#6BC4BF"
  accent-pressed-dark: "#45A29D"
  accent-text-dark: "#0F1114"
  accent-tint-dark: "#172E2E"
  danger-dark: "#EE8A74"
  danger-tint-dark: "#3B1D21"
  wait-dark: "#E0A048"
  wait-tint-dark: "#30281A"
  ok-dark: "#6CC08F"
  ok-tint-dark: "#1B2D24"
  term-bg-dark: "#0F1114"
  term-fg-dark: "#D4D8DE"
  rail-dark: "#1D2024"
  rail-text-dark: "#BFC5CD"
  rail-button-dark: "#2F3237"
  wait-fill-dark: "#E9A23B"
  wait-fill-hover-dark: "#ECAF59"
  on-wait-fill-dark: "#0E1012"
  danger-fill: "#B9432F"
  danger-fill-hover: "#A63B2A"
  danger-fill-pressed: "#923425"
  danger-text: "#ffffff"
  tile-hues: "#7E4F86 #3D67A8 #6A55A8 #A44C68 #8A6230 #56657A"
typography:
  title:
    fontFamily: "system"
    fontSize: "18px"
    fontWeight: 700
  heading:
    fontFamily: "system"
    fontSize: "16px"
    fontWeight: 700
  body:
    fontFamily: "system"
    fontWeight: 400
  body-strong:
    fontFamily: "system"
    fontWeight: 700
  section-label:
    fontFamily: "system"
    fontSize: "12px"
    fontWeight: 700
  mono:
    fontFamily: "monospace"
    fontWeight: 400
rounded:
  control: "6px"
  card: "10px"
  dialog: "14px"
  tile: "8px"
spacing:
  xs: "6px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  xl: "24px"
components:
  rail-button:
    backgroundColor: "{colors.rail-button-light}" # dark {colors.rail-button-dark}; 1 px Control Line border
    textColor: "{colors.rail-text-light}"
    rounded: "{rounded.control}"
  button-primary-light:
    backgroundColor: "{colors.accent-light}"
    textColor: "{colors.accent-text-light}"
    rounded: "{rounded.control}"
  button-primary-dark:
    backgroundColor: "{colors.accent-dark}"
    textColor: "{colors.accent-text-dark}"
    rounded: "{rounded.control}"
  button-deny:
    backgroundColor: "{colors.danger-fill}"
    textColor: "{colors.danger-text}"
    rounded: "{rounded.control}"
    padding: "6px 12px"
  button-allow:
    textColor: "{colors.ok-light}"
    rounded: "{rounded.control}"
    padding: "6px 12px"
  host-card:
    backgroundColor: "{colors.surface-light}"
    textColor: "{colors.text-light}"
    rounded: "{rounded.card}"
    padding: "12px"
    width: "240px"
  host-tile:
    backgroundColor: "#3D67A8" # one of colors.tile-hues, by host name; Go Green while connected
    textColor: "#ffffff"
    rounded: "{rounded.tile}"
    size: "36px"
  request-card:
    backgroundColor: "{colors.raised-light}"
    rounded: "{rounded.card}"
    padding: "12px"
  term-block:
    backgroundColor: "{colors.term-bg-light}"
    textColor: "{colors.term-fg-light}"
    rounded: "{rounded.control}"
    padding: "8px 10px"
  chip-ai:
    backgroundColor: "{colors.accent-tint-light}"
    textColor: "{colors.accent-light}"
    rounded: "{rounded.card}"
    padding: "2px 8px"
  chip-wait:
    backgroundColor: "{colors.wait-tint-light}"
    textColor: "{colors.wait-light}"
    rounded: "{rounded.card}"
    padding: "2px 8px"
  dialog:
    backgroundColor: "{colors.surface-light}"
    rounded: "{rounded.dialog}"
  section-card:
    backgroundColor: "{colors.raised-light}"
    rounded: "{rounded.card}"
    padding: "12px"
---

# Design System: sshgate

Recorded from the shipped app in `app/view/` (palette.go, guard.go, modal.go, approvals.go, home.go, frame.go, screens.go, section.go, icons.go, tabs.go, audit.go, files.go, tunnels.go) and the screenshots of every screen in both themes. A native macOS app on the mygo `ui` toolkit, not web. Hex values are normative in the frontmatter; the code names them as `tokens` fields in palette.go and no view writes a raw colour (a few raw radii exist, see Don'ts). Light and dark values are separate keys (`-light`, `-dark`); the `danger-fill` family, `danger-text` and `tile-hues` are the same in both themes.

## Overview

**Creative North Star: "The Quiet Gatehouse"**

A calm cool-neutral app with one teal accent. The shell (a full-width top bar and a left sidebar) is one neutral step off the work area and a 1 px Divider, not a colour block. Everything is quiet grey, so the few things that ask for a decision stand out: an amber Auto chip on a host the AI may run on without a click, a filled red Deny, a red edge on a SUDO request.

It is an Operate surface for one person who keeps it open all day, so it is dense and flat, with system type and no decoration. Colour carries state, not mood: teal is identity and action, green is connected or allowed, amber is waiting or a live grant, red is refusal and risk. Tints are used at full strength for chips and banners, never faded.

The ground is cool neutral in both themes (hue about 210-220 degrees, chroma low enough to read as grey, not blue): a cool off-white in light, a cool charcoal in dark.

**Key Characteristics:**
- A neutral shell (rail) one step off the work area; teal only as the accent. The terminal and Files yield the sidebar.
- Three surface steps, two shadows, three radii.
- Filled letter tiles, one hue per host (never green); the grant shows as the Auto chip (ring removed 2026-11-17 at the author's request; the chip carries the grant).
- System font; monospace only for what is literal.
- Decisions are slow on purpose: Allow is outline and mouse-only, Cancel is the filled default.

## Colors

Cool neutrals, one teal accent, and four state hues each with a tint. Every text and UI pair is checked in `palette_test.go` (see Accessibility).

### Primary
- **Deep Teal** (light `#0F726D`, dark `#55B8B2`): the one accent. Primary buttons, focus rings, the chosen sidebar item (icon and label on the tint), the home mark's shield, the AI chip, tunnel chips, links, selected rows. Hover `#0C6662` / `#6BC4BF`, pressed `#0A5754` / `#45A29D`. Text on it: `#FCFCFD` in light, `#0F1114` in dark. Tint `#DCEFEC` / `#172E2E` for chip and selection fills.
- **Rail** (light `#EBEDF0`, dark `#1D2024`): the shell, a neutral step off the work area (darker than Bg in light, lighter in dark, as macOS sidebars are), with a 1 px Divider under the top bar and beside the sidebar. Text and icons on it are **Rail Text** (`#3F4650` / `#BFC5CD`). Never used in the work area.

### Secondary (state hues)
- **Go Green** (light `#3B7352`, dark `#6CC08F`): connected tile and its dot, the Allow outline and label, success text, "Ready" chip. Tint `#E6F4EC` / `#1B2D24`.
- **Wait Amber** (light `#87601F`, dark `#E0A048`): pending or live-grant state. Chip text, warnings, amber banners. Tint `#FBEFDC` / `#30281A`.
- **Wait Fill** (light `#9A5800` with white text, hover `#864C00`; dark `#E9A23B` with `#0E1012` text, hover `#ECAF59`): the lit AI button when requests wait, and the auto-allow dot on a tab. Light is a deep amber because a pale amber cannot reach 3:1 on the light rail. A fill, not a pill.
- **Stop Red** (light `#B5412F`, dark `#EE8A74`): text and outline red. Errors, the Delete and Enable tones, SUDO label, the red edge. Tint `#FBE3DE` / `#3B1D21` (a cool wine in dark, so it never reads as the brown Wait tint).
- **Deny Fill** (`#B9432F`, both themes; hover `#A63B2A`, pressed `#923425`, text white): the filled Deny and Delete-for-good buttons.

### Neutral
- **Bg** (light `#F4F5F7`, dark `#16181B`): window ground and text-field fill.
- **Surface** (light `#FCFCFD`, dark `#1C1F23`): host cards, the AI column, dialogs, sheets, the unlock card.
- **Raised** (light `#EDEFF2`, dark `#23272C`): request cards, section groups, table headers, row hover. `c.Theme().Surface` maps to this step.
- **Surface Hover** (`#E4E7EB` / `#2B2F35`) and **Line** (`#DADEE3` / `#363B42`, also the pressed fill): hover on flat items, card and divider outlines.
- **Divider** (`#E3E6EA` / `#2A2E34`): hairlines, and the rail's edge.
- **Control Line** (`#7D8590` / `#737B86`): the border of buttons and inputs, rail buttons included; the only line that must reach 3:1.
- **Ink** (`#1A1E23` / `#E5E8EC`), **Label** (`#3F4650` / `#B3B9C2`), **Muted** (`#5D6570` / `#959CA7`): primary text, section titles, secondary text and addresses.
- **Terminal** (bg `#FCFCFD` / `#0F1114`, text `#1A1E23` / `#D4D8DE`): the terminal and every block that shows what the AI sent. Always outlined with Line so it reads on a surface that shares its colour.

### Named Rules
**The State-Not-Style Rule.** Teal, green, amber and red each mean one thing and are never used for decoration. Green is a human-confirmed or connected state; amber is waiting or a live grant; red is refusal or risk.

**The Shell Stays Shell Rule.** Rail and Rail Text appear only on the top bar and sidebar. The work area never borrows them, and the shell never takes a hue: it is told apart by a neutral step and a 1 px Divider.

**The Full-Strength Tint Rule.** Chips and banners use the tint colour at full strength with the matching hue as text. No translucent washes except the rail's own 8 % Rail Text hover.

### Decision: cool neutrals, teal as the accent only (2026-11-17)
On 2026-11-17 the author asked for a slightly cool neutral that is not as heavy as the deep-teal shell; the cool neutral replaced the warm-neutral-plus-teal-shell look of the same day. Neutrals are cool grey (blue a few points above red, low chroma) with the same lightness roles; the rail is a neutral step, not Rail Teal; teal is the accent only; the tile hues and state colours were muted a little to sit on grey. Light Surface and terminal background are `#FCFCFD`, not pure white.

## Typography

**System font** for all text (the toolkit default; the app sets no family). **Monospace** (`Font("monospace")`) only for literal text: host addresses (`user@host:port`), commands, stdin, paths, key fingerprints and key paths, timestamps, ports.

**Character:** plain and neutral, so a command or fingerprint in monospace is the loudest typographic thing on screen. Hierarchy is made with weight and colour, not size.

### Hierarchy
- **Title** (bold, 18 px): the unlock and create-vault cards.
- **Heading** (bold, 16 px): dialog and sheet titles; red (Stop Red) on a destructive confirm.
- **Body** (regular, the toolkit's default size): everything else. The app sets no body size.
- **Body strong** (bold, default size): host names, request server names, group headings, table headers, "Hosts · N".
- **Section label** (bold, 12 px, Label colour): the heading above a section group in editor and settings.
- **Mono** (regular, default size): the literal cases above, Muted when it is an address, Terminal text when it is a command.

### Named Rules
**The Literal-Only Rule.** Monospace marks text the user may have to copy or compare exactly. Do not set labels, buttons or prose in it.

**The Weight-Not-Size Rule.** Emphasis is bold or Muted. There is no display size; new screens use Body, Body strong, Heading and Title.

## Layout

A full-width top bar (rail) over the window; below it a 200 px sidebar (rail) at left, the work column that grows, and the AI column at right (380 px, never narrows). Editor and import sheets (420 px) sit beside the work column. The sidebar is hidden while a terminal, Files or Tunnels tab is active, since those want the width; it hides and returns without animation. A sheet beside the AI column also hides the sidebar so the column never clips or moves. The AI column never moves under the cursor and the terminal never resizes during animation.

Host cards sit in a wrapping grid: 240 px wide (minimum 200), 12 px gap. Page padding 16, card padding 12, card stack gap 8, section groups gap 10 with 12 padding, auth cards 360 px wide with 24 padding. The spacing steps in use are 6, 8, 10, 12, 16, 24. Banners (store error, ran-while-locked, alerts) sit in the work column above the content, never over the AI column, so one appearing cannot move Allow.

## Elevation & Depth

Tonal layering plus two shadows. Depth is mostly steps of colour: Bg (the ground), Surface (cards, the AI column, dialogs), Raised (request cards, section groups, headers). Shadow is secondary.

### Shadow Vocabulary
- **Card** (`0 1px 3px 0`, ink at 20/255 alpha light, black at 90/255 dark): host cards at rest.
- **Raised** (`0 6px 18px 0`, ink at 38/255 alpha light, black at 150/255 dark): hovered host card, dialogs, sheets, the AI column's left edge, the 360 px auth card.

### Named Rules
**The Two-Shadows Rule.** Card and Raised are the whole vocabulary. Dialogs and the AI column take Raised; nothing takes a third.

**The Border-Plus-Shadow Rule.** A card also has a 1 px Line border so it reads in dark, where its shadow is nearly lost.

## Shapes

Three radii and one tile exception. **Control 6 px** (buttons, inputs, the terminal block, the SUDO label, filter toggles); **Card 10 px** (host cards, request cards, section groups, table containers, status chips); **Dialog 14 px** (modals, the auth card). Letter tiles are 36 px squares with an 8 px radius; the connected dot is a 10 px circle with a 2 px Surface border. Outlines are 1 px; a SUDO request adds a 4 px red left edge (`BorderStartWidth(4)`). Icons are 24 px viewBox, 2 px round-cap strokes in the current colour, drawn as SVG.

## Components

### Rail top bar and buttons
The full-width Rail bar with a 1 px Divider below it, padding 8, gap 8: the home mark (a 20 px Accent shield and "sshgate" in bold Ink, left-aligned in 200 px to line up with the sidebar; a button underneath for focus and clicks, but with no face at rest, hovered, pressed or while Home shows), the tab strip (grows), the AI button, Lock. A tab is one rounded piece (34 px tall, 14 pt label, at least 140 wide, the × inside); an inactive one is Rail Text with an 8 % hover, the open one is filled Bg (Terminal for a terminal) with bold Ink and a 2 px Accent bar inside its bottom edge (inset 8), so it reads as the page it shows. A host with a live or paused auto-allow grant puts an 8 px Wait Fill dot, vertically centred, before its tab. Rail buttons are Rail Text on **Rail Button** (`#FCFCFD` light, a white face as on a macOS toolbar, since a grey pill there reads as disabled; `#2F3237` dark) with a 1 px Control Line outline, hover 8 % Rail Text over the rail; the same style on every top-bar button. The AI button appears while the column is open, requests wait or a grant is on; label `AI n` (plus ` · auto n`). When requests are waiting it is a lit Wait Fill button with On Wait Fill text, not a pill.

### Sidebar
Rail, 200 px, a 1 px Divider at its right edge, Hosts, Audit and Keychain with 24 px stroke icons. Items are Rail Text (icon and label); the chosen one is the Accent Tint row with its icon and label in Accent, label semibold (`accentTheme`); hover is 8 % Rail Text. Focus rings stay Accent. At the foot: the Moon / Sun / Auto theme control (chosen segment on Rail Button) and a Settings gear (16 px), both Rail Text.

### Host card
Surface, Line border, card shadow, 240 px, radius 10, padding 12. Top row is a flat "Open" button (no face at rest, Surface Hover on hover) holding the tile, the bold name and the monospace Muted address. Below: chips at left (`AI` teal, `New key` amber, the timed-grant label amber, `n ⇄` running tunnels teal), and the actions at right (Stop auto-allow while a grant is on or paused, Files, Tunnels, Edit, Delete as icon buttons, Muted until the card is hovered, then ink). Hover lifts the shadow to Raised.
- **Tile**: 36 px, the host's letter-tile hue (below) with a white letter; Go Green with a 10 px "connected" dot while a terminal tab is open.
- **Letter tile**: a 36 px square, white letter on one of six muted hues, the same in both themes (plum `#7E4F86`, blue `#3D67A8`, violet `#6A55A8`, rose `#A44C68`, amber-brown `#8A6230`, slate `#56657A`; each at least 4.8:1 with white) picked by FNV-1a of the host name; green is only the connected tile, and teal only the accent. The grant is shown by the Auto chip (ring removed 2026-11-17 at the author's request; the chip carries the grant).

### Key card
The Keychain tab's card (`buildKeyCard`): Surface, Line border, card shadow, radius 10, padding 12, gap 10. Top row: a 36 px Accent Tint tile holding the key icon in Accent, the bold key name, "ECDSA P-256 · Secure Enclave" and "Created <date>" in Muted, and a chip at right ("Not used" Muted on Raised; "Used by N host(s)" Accent on Accent tint, the host names in its tooltip). Below, the OpenSSH public key in a **termBlock** (selectable). At the foot, right-aligned: "Copy public key" and Delete. Delete is disabled while a host uses the key (tooltip names the hosts); its confirm is a dialog with Cancel as the filled primary with focus and Delete as a delayed `toneDangerFill` decision button. The tab header is the bold 16 px "Keychain" with New key as the primary button.

### AI request card
Raised, Line border, radius 10, padding 12, gap 6. Header: bold server name, a SUDO label (Stop Red on Stop Red tint) when sudo, time in monospace Muted. Then the target (`user@host:port`, monospace Muted), then the command in a **termBlock** (Terminal colours, 1 px Line border, radius 6, padding 8 × 10, monospace, non-ASCII runs shown as `\uXXXX` in Stop Red and underlined). A sudo request has a red outline with a 4 px red left edge. Stdin and transfers use the same block; transfer lines are `UPLOAD` / `DOWNLOAD` bold, paths in monospace. The AI's description is labelled "unverified". A "Reason (optional)" field sits above the buttons. Buttons, left to right: **Deny** (filled Deny Fill, white text, the default and keyboard-reachable), **Send to tab** (plain, disabled for stdin and transfers with a tooltip), **Allow** (outline and label in Go Green once enabled, mouse-only).

### Decision buttons and tones
`MouseButton` and `decisionButton` are a row that never takes focus: 1 px Line border (`t.Border`; the tone colour once enabled), padding 6 × 12, radius 6. While disabled (the 500 ms delay, or busy) every tone looks neutral. Once enabled the tone shows: Ok (Allow), Accent (Trust and connect, Resume), Danger (Enable, Overwrite all: red outline and label), DangerFill (Delete host, Delete for good, Deny: filled). Skip existing stays neutral.

### Dialogs and sheets
Dialogs use Surface, radius 14, a 1 px Line border (dark loses the shadow), Raised shadow, a bold 16 px title. **Cancel is the filled primary button and takes focus** (the safe default); the risky action beside it is a delayed decision button and neutral until its delay ends. Destructive titles are Stop Red. Sheets (host editor, import) are 420 px columns on Surface with a 1 px Line left edge and Raised shadow: title row with an × (padding 12 × 16), a scrolling body, a footer row with the error at left and buttons at right; Save is the primary button. Both enter with the 160 ms fade and 0.98 to 1 scale.

### Section groups
A bold 12 px Label-colour heading above a Raised card (radius 10, padding 12, gap 8) that holds the fields. Used in the editor and settings.

### Status chips and banners
Chip (one `chip()` helper in home.go, also the import and tunnel status chips): tinted pill, padding 2 × 8, a fixed radius of 10 (the Card step), text in the hue (AI teal, New key and timed grant amber, Ready green, Already in vault and Skipped amber). Banners are full-width rows with padding 8 × 12 on a tint: Stop Red tint for store errors, Wait tint for paused or ran-while-locked and tunnel warnings, each with a Dismiss button where the user may clear it.

### Tables and lists
Raised header row (padding 8 × 12, bold) over Surface rows inside a Surface, radius-10, clipped container (tunnels, files). The audit list is a header-less list with Surface date headings (bold, padding 6 × 12), monospace times and commands, filter toggles in a toggle group, and a search field; its table scrolls sideways inside the work column so the AI column is never pushed.

### Auth and status card
The 360 px Surface card, Line border, radius 14, Raised shadow, padding 24, gap 12, a 32 px shield in Accent (Stop Red when the hub failed), centred and scrolling when short. Used for create vault, unlock, and hub starting, restarting and failed.

## Motion

Only these animate; all app-driven motion is skipped under Reduce Motion and the toolkit's own `Animate` goes to rest.

| Where | Motion | Time |
|---|---|---|
| Hover, press, selection colour | colour fade | 120 ms |
| Host card hover | shadow Card to Raised | 120 ms |
| AI column open | position snaps; content fades in (opacity only) | 120 ms |
| New AI request card | opacity only | 120 ms |
| View change (Hosts, Audit) | cross-fade | 120 ms |
| Dialogs and sheets | fade and scale 0.98 to 1 from centre | 160 ms |
| Sidebar hide and show | none; it snaps | 0 |

No animation calls the hub; clocks redraw from the app's own time. No animation changes the size of a mounted terminal or moves an element near a decision button; a dialog's 0.98 to 1 entrance does move its buttons for 160 ms, which is safe because decision buttons are disabled for their first 500 ms, so no click can land on one in motion.

## Do's and Don'ts

### Do:
- **Do** read every colour from `tok(c)` / `palette(dark)`; add a new pair to `palette_test.go` before using it (4.5:1 text, 3:1 UI).
- **Do** make Deny the filled, default, keyboard-reachable button and Allow an outline that waits 500 ms and takes a mouse click.
- **Do** make Cancel the filled primary with AutoFocus in any dialog that has a risky action.
- **Do** show a request's command, stdin, paths and addresses in monospace on the Terminal colours with a Line outline.
- **Do** put banners in the work column, not in the AI column.
- **Do** keep the tile as the way a host shows its state: its own hue by default, green connected; the Auto chip shows a grant.
- **Do** use 6 / 10 / 14 radii, the two shadows, and the Bg / Surface / Raised steps.
- **Do** keep terminal-resizing layout changes instant (snap), never animated.

### Don't:
- **Don't** animate the sidebar or AI column width, or anything that moves a decision button or resizes a terminal.
- **Don't** call the hub from an animation, timer or `autoAllow` notification; the app polls only `status`.
- **Don't** use Rail or Rail Text outside the top bar and sidebar, or give the shell a hue.
- **Don't** put a monospace face on labels, buttons or prose.
- **Don't** write a raw colour or add a third shadow.
- **Don't** make Allow, Trust, Enable, Resume, Overwrite all, Skip existing or Delete easier than their safe counterpart: they are never the default, never focus-takers, and are neutral until their delay ends.
- **Don't** show a secret.

### Not canonized (defects the build carries)
- The Stop auto-allow button is the text glyph `■` and the tunnel chip uses `⇄` in its text; glyph icons are not a pattern to repeat, use `cardIcon` SVGs.
- A few radii are raw literals off the scale: 8 on the letter tile and the unlock notice card, 6 on the feed rows and the "AI" request-feed boxes; new work uses `radiusControl`, `radiusCard`, `radiusDialog`.
