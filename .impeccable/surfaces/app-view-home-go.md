---
version: 1
slug: "app-view-home-go"
primary_target: "app/view/home.go"
related_targets: ["app/view/frame.go"]
---

Mode: Operate, code-led. Surface: the Hosts view, frame.go shell.

## Direction contract

THESIS: A calm work area framed by a teal shell; each host has its own tile hue (ring removed 2026-11-17 at the author's request; the chip carries the grant). Refuses the grey app with one accent and a top-tab strip.

OWN-WORLD: Warm-neutral work area (bg, surface, raised steps, never pure grey), deep teal `rail` under the sidebar and the full-width top bar with pale railText. Filled letter tiles, teal by default, green when a terminal is open. Radii 6/10/14, two shadows (card, raised). Tints at full strength for chips and banners. Monospace only for addresses and commands.

STORY: The user sees at once which hosts exist, which are connected, which the AI may run on without a click. They believe nothing runs unseen, and they open a terminal or answer a request from the same screen.

FIRST VIEWPORT: Hosts view. Left sidebar (teal rail, fixed width) with the nav; full-width teal top bar over the whole window with the title and the lock control; the work area beside it holds host cards in a grid, each a surface card with a letter tile at left; the approval column sits at the right edge; Add host is the primary action, top right of the work area.

FORM: seed: none (pinned) "Giữ ấm + teal, làm đậm hơn", "Sidebar trái như Termius".

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
