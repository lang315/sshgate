---
name: sshgate
description: A native macOS gate between an AI agent and real servers; a calm warm-neutral work area inside a deep-teal shell, where every command waits for a human.
colors:
  bg-light: "#F6F4F0"
  surface-light: "#FFFDFB"
  raised-light: "#EEEBE5"
  surface-hover-light: "#E6E2DA"
  line-light: "#DFDAD2"
  control-line-light: "#857E73"
  divider-light: "#E6E2DB"
  text-light: "#1F1C19"
  muted-light: "#6B655D"
  label-light: "#4A453F"
  accent-light: "#12766F"
  accent-hover-light: "#0F6A64"
  accent-pressed-light: "#0C5A55"
  accent-text-light: "#ffffff"
  accent-tint-light: "#dcf1ec"
  danger-light: "#B5412F"
  danger-tint-light: "#fbe2dc"
  wait-light: "#8B6123"
  wait-tint-light: "#fcefdc"
  ok-light: "#3D7554"
  ok-tint-light: "#e9f6ee"
  term-bg-light: "#FFFDFB"
  term-fg-light: "#1F1C19"
  rail-light: "#0F5F59"
  bg-dark: "#161514"
  surface-dark: "#1C1A19"
  raised-dark: "#23211F"
  surface-hover-dark: "#2B2927"
  line-dark: "#3D3A37"
  control-line-dark: "#7A746D"
  divider-dark: "#2E2B29"
  text-dark: "#ECE8E0"
  muted-dark: "#9A948C"
  label-dark: "#B8B3AB"
  accent-dark: "#56BFB9"
  accent-hover-dark: "#6CCAC4"
  accent-pressed-dark: "#45A9A3"
  accent-text-dark: "#0E1012"
  accent-tint-dark: "#16312F"
  danger-dark: "#F28B73"
  danger-tint-dark: "#3D1D18"
  wait-dark: "#E9A23B"
  wait-tint-dark: "#3A2C14"
  ok-dark: "#6CC58F"
  ok-tint-dark: "#1A3324"
  term-bg-dark: "#100F0E"
  term-fg-dark: "#D8D4CC"
  rail-dark: "#134542"
  rail-text: "#E6F2F0"
  danger-fill: "#B5412F"
  danger-fill-hover: "#A23A2A"
  danger-fill-pressed: "#8F3325"
  danger-text: "#ffffff"
  wait-fill: "#E9A23B"
  on-wait-fill: "#0E1012"
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
    backgroundColor: "#276F6A" # light; dark #2A5855: 11 % Rail Mist over the rail (railButtonFill)
    textColor: "{colors.rail-text}"
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
    backgroundColor: "{colors.accent-light}"
    textColor: "{colors.accent-text-light}"
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

Recorded from the shipped app in `app/view/` (palette.go, guard.go, modal.go, approvals.go, home.go, frame.go, screens.go, section.go, icons.go, tabs.go, audit.go, files.go, tunnels.go) and the screenshots of every screen in both themes. A native macOS app on the mygo `ui` toolkit, not web. Hex values are normative in the frontmatter; the code names them as `tokens` fields in palette.go and no view writes a raw colour (a few raw radii exist, see Don'ts). Light and dark values are separate keys (`-light`, `-dark`); `rail-text`, the `danger-fill` family, `wait-fill` and `on-wait-fill` are the same in both themes.

## Overview

**Creative North Star: "The Teal Gatehouse"**

A calm warm-neutral work area held inside a deep-teal shell. The shell is the gate: a full-width top bar and a left sidebar in one teal, with pale text on it. Inside it the work is quiet, so the few things that ask for a decision stand out: an amber Auto chip on a host the AI may run on without a click, a filled red Deny, a red edge on a SUDO request.

It is an Operate surface for one person who keeps it open all day, so it is dense and flat, with system type and no decoration. Colour carries state, not mood: teal is identity and action, green is connected or allowed, amber is waiting or a live grant, red is refusal and risk. Tints are used at full strength for chips and banners, never faded.

The ground is warm neutral in both themes (hue about 30-40 degrees, low chroma). Neutrals warmed 2026-11-17 at the author's request.

**Key Characteristics:**
- Teal shell (rail) around a neutral work area; the terminal and Files yield the sidebar.
- Three surface steps, two shadows, three radii.
- Filled letter tiles, one hue per host (never green); the grant shows as the Auto chip (ring removed 2026-11-17 at the author's request; the chip carries the grant).
- System font; monospace only for what is literal.
- Decisions are slow on purpose: Allow is outline and mouse-only, Cancel is the filled default.

## Colors

A teal shell, warm neutrals, and four state hues each with a tint. Every text and UI pair is checked in `palette_test.go` (see Accessibility).

### Primary
- **Deep Teal** (light `#12766F`, dark `#56BFB9`): action and identity. Primary buttons, the AI chip, tunnel chips, focus, host tiles, selected rows. Hover `#0F6A64` / `#6CCAC4`, pressed `#0C5A55` / `#45A9A3`. Text on it: white in light, `#0E1012` in dark. Tint `#dcf1ec` / `#16312F` for chip and selection fills.
- **Rail Teal** (light `#0F5F59`, dark `#134542`): the shell. The top bar and sidebar background only. Text, icons, outlines on it are **Rail Mist** (`#E6F2F0`, both themes). Never used in the work area.

### Secondary (state hues)
- **Go Green** (light `#3D7554`, dark `#6CC58F`): connected tile and its dot, the Allow outline and label, success text, "Ready" chip. Tint `#e9f6ee` / `#1a3324`.
- **Wait Amber** (light `#8B6123`, dark `#E9A23B`): pending or live-grant state. Chip text, warnings, amber banners. Tint `#fcefdc` / `#3a2c14`.
- **Wait Fill** (`#E9A23B`, both themes, text `#0E1012`): the lit AI button when requests wait (hover: 12 % lighter). A fill, not a pill.
- **Stop Red** (light `#B5412F`, dark `#F28B73`): text and outline red. Errors, the Delete and Enable tones, SUDO label, the red edge. Tint `#fbe2dc` / `#3d1d18`.
- **Deny Fill** (`#B5412F`, both themes; hover `#A23A2A`, pressed `#8F3325`, text white): the filled Deny and Delete-for-good buttons.

### Neutral
- **Bg** (light `#F6F4F0`, dark `#161514`): window ground and text-field fill.
- **Surface** (light `#FFFDFB`, dark `#1C1A19`): host cards, the AI column, dialogs, sheets, the unlock card.
- **Raised** (light `#EEEBE5`, dark `#23211F`): request cards, section groups, table headers, row hover. `c.Theme().Surface` maps to this step.
- **Surface Hover** (`#E6E2DA` / `#2B2927`) and **Line** (`#DFDAD2` / `#3D3A37`, also the pressed fill): hover on flat items, card and divider outlines.
- **Divider** (`#E6E2DB` / `#2E2B29`): hairlines.
- **Control Line** (`#857E73` / `#7A746D`): the border of buttons and inputs; the only line that must reach 3:1.
- **Ink** (`#1F1C19` / `#ECE8E0`), **Label** (`#4A453F` / `#B8B3AB`), **Muted** (`#6B655D` / `#9A948C`): primary text, section titles, secondary text and addresses.
- **Terminal** (bg `#FFFDFB` / `#100F0E`, text `#1F1C19` / `#D8D4CC`): the terminal and every block that shows what the AI sent. Always outlined with Line so it reads on a surface that shares its colour.

### Named Rules
**The State-Not-Style Rule.** Teal, green, amber and red each mean one thing and are never used for decoration. Green is a human-confirmed or connected state; amber is waiting or a live grant; red is refusal or risk.

**The Shell Stays Shell Rule.** Rail Teal and Rail Mist appear only on the top bar and sidebar. The work area never borrows them.

**The Full-Strength Tint Rule.** Chips and banners use the tint colour at full strength with the matching hue as text. No translucent washes except the rail's own overlays (8 % hover, 11 % pill, 60 % outline of Rail Mist).

### Decision: warm neutrals
The author chose warm neutrals; neutrals warmed 2026-11-17 at the author's request (hue about 30-40 degrees, red >= green >= blue, same lightness roles as the slate they replace). Accent, rail and state colours did not change. Light Surface and terminal background are a warm white `#FFFDFB`, not pure white.

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
The full-width Rail Teal bar, padding 8, gap 8: the Home button ("sshgate" with the shield, 200 px wide to line up with the sidebar), the tab strip (grows), the AI button, Lock. A tab is a rail button; the open one is a Bg-filled button with Text colour, so it reads as the page it shows. A host with a live or paused auto-allow grant puts an 8 px Wait Fill dot, vertically centred, before its tab. Rail buttons are Rail Mist text on an 11 % Rail Mist pill with a 60 % Rail Mist outline, hover 8 %; the same style on every top-bar button. The AI button appears while the column is open, requests wait or a grant is on; label `AI n` (plus ` · auto n`). When requests are waiting it is a lit Wait Fill button with dark text, not a pill.

### Sidebar
Rail Teal, 200 px, Hosts, Audit and Keychain with 24 px stroke icons. Selected item is an 11 % Rail Mist pill; hover is 8 %. It is theme-scoped so icons and focus are Rail Mist. At the foot: the Moon / Sun / Auto theme control and a Settings gear.

### Host card
Surface, Line border, card shadow, 240 px, radius 10, padding 12. Top row is a flat "Open" button (no face at rest, Surface Hover on hover) holding the tile, the bold name and the monospace Muted address. Below: chips at left (`AI` teal, `New key` amber, the timed-grant label amber, `n ⇄` running tunnels teal), and the actions at right (Stop auto-allow while a grant is on or paused, Files, Tunnels, Edit, Delete as icon buttons, Muted until the card is hovered, then ink). Hover lifts the shadow to Raised.
- **Tile**: 36 px, Accent with the host's letter in Accent Text; Go Green with a 10 px "connected" dot while a terminal tab is open.
- **Letter tile**: a 36 px square, white letter on one of six hues (teal, blue, violet, rose, amber-brown, slate; each at least 4.5:1 with white) picked by FNV-1a of the host name; green is only the connected tile. The grant is shown by the Auto chip (ring removed 2026-11-17 at the author's request; the chip carries the grant).

### Key card
The Keychain tab's card (`buildKeyCard`): Surface, Line border, card shadow, radius 10, padding 12, gap 10. Top row: a 36 px Accent tile holding the key icon, the bold key name, "ECDSA P-256 · Secure Enclave" and "Created <date>" in Muted, and a chip at right ("Not used" Muted on Raised; "Used by N host(s)" Accent on Accent tint, the host names in its tooltip). Below, the OpenSSH public key in a **termBlock** (selectable). At the foot, right-aligned: "Copy public key" and Delete. Delete is disabled while a host uses the key (tooltip names the hosts); its confirm is a dialog with Cancel as the filled primary with focus and Delete as a delayed `toneDangerFill` decision button. The tab header is the bold 16 px "Keychain" with New key as the primary button.

### AI request card
Raised, Line border, radius 10, padding 12, gap 6. Header: bold server name, a SUDO label (Stop Red on Stop Red tint) when sudo, time in monospace Muted. Then the target (`user@host:port`, monospace Muted), then the command in a **termBlock** (Terminal colours, 1 px Line border, radius 6, padding 8 × 10, monospace, non-ASCII runs shown as `\uXXXX` in Stop Red and underlined). A sudo request has a red outline with a 4 px red left edge. Stdin and transfers use the same block; transfer lines are `UPLOAD` / `DOWNLOAD` bold, paths in monospace. The AI's description is labelled "unverified". A "Reason (optional)" field sits above the buttons. Buttons, left to right: **Deny** (filled Deny Fill, white text, the default and keyboard-reachable), **Send to tab** (plain, disabled for stdin and transfers with a tooltip), **Allow** (outline and label in Go Green once enabled, mouse-only).

### Decision buttons and tones
`MouseButton` and `decisionButton` are a row that never takes focus: 1 px Line border (`t.Border`; the tone colour once enabled), padding 6 × 12, radius 6. While disabled (the 500 ms delay, or busy) every tone looks neutral. Once enabled the tone shows: Ok (Allow), Accent (Trust and connect, Resume), Danger (Enable, Overwrite all: red outline and label), DangerFill (Delete host, Delete for good, Deny: filled). Skip existing stays neutral.

### Dialogs and sheets
Dialogs use Surface, radius 14, Raised shadow, a bold 16 px title. **Cancel is the filled primary button and takes focus** (the safe default); the risky action beside it is a delayed decision button and neutral until its delay ends. Destructive titles are Stop Red. Sheets (host editor, import) are 420 px columns on Surface with a 1 px Line left edge and Raised shadow: title row with an × (padding 12 × 16), a scrolling body, a footer row with the error at left and buttons at right; Save is the primary button. Both enter with the 160 ms fade and 0.98 to 1 scale.

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
- **Don't** use Rail Teal or Rail Mist outside the top bar and sidebar.
- **Don't** put a monospace face on labels, buttons or prose.
- **Don't** write a raw colour or add a third shadow.
- **Don't** make Allow, Trust, Enable, Resume, Overwrite all, Skip existing or Delete easier than their safe counterpart: they are never the default, never focus-takers, and are neutral until their delay ends.
- **Don't** show a secret.

### Not canonized (defects the build carries)
- The Stop auto-allow button is the text glyph `■` and the tunnel chip uses `⇄` in its text; glyph icons are not a pattern to repeat, use `cardIcon` SVGs.
- A few radii are raw literals off the scale: 8 on the letter tile and the unlock notice card, 6 on the feed rows and the "AI" request-feed boxes; new work uses `radiusControl`, `radiusCard`, `radiusDialog`.
