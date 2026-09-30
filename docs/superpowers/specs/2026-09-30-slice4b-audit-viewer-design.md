# sshgate: Audit Viewer — Design (Slice 4, part b)

Date: 2026-09-30
Status: Approved in chat 2026-09-30; not yet implemented.
Depends on: `2026-09-24-desktop-app-design.md` (slice 1), `2026-09-28-auto-allow-design.md`, and `2026-09-30-slice4a-output-redaction-design.md`, whose `redacted` counts this viewer shows. Everything there still holds unless this document says otherwise.

## Goal

Read the audit log in the app instead of in a terminal. `audit.jsonl` is the ground truth for "did this command need a click, who allowed it, what ran on auto-allow, what changed in the vault". In this project's live testing, reading that file by hand twice overturned a conclusion reached from the tester's recollection.

Exit gate: on the author's real vault, the Audit tab shows the day's AI requests with their outcomes. Filtering to Auto shows only auto-allowed runs. A new request appears in the tab while it is open, without a refresh.

## Scope

**In:**
- An **Audit** tab, fixed, second in the tab strip after `⌂ Hosts`.
- A UI-door method `audit.read`, with filters and paging.
- A notification `audit.appended`, so the tab updates live.
- Every record kind: exec, config, file, tunnel.
- An expandable detail view for each row.

**Out, recorded in ROADMAP:**
- **Audit rotation.** The log is 14 KB today. Rotation comes with the move to reading the file backwards (see Hub).
- **Export.** `audit.jsonl` is already JSONL, and the tab's footer shows its path.
- **Viewing while the vault is locked.**
- **MCP-door access.** The AI never reads the audit log.

## Decisions made in chat

- The viewer is its own tab next to Hosts. It is not a section of the AI column, and not a per-host tab.
- It shows every record kind, updates live, and has filters: host, kind/outcome chips, and text.
- The viewer needs an unlocked vault. Records held in the renderer are dropped on lock.
- There is no export.

## Hub

**`broker.Audit`** keeps a line counter. `OpenAudit` counts the lines already in the file, and each append increments the counter. It gains two methods:

- `OnAppend(fn func(seq int, line json.RawMessage))`. The callback is set once, by the hub. It is called after the line is written, with `a.mu` released.
- `Read(q ReadQuery) (ReadResult, error)`. It holds `a.mu`, so reads never see a half-written line. It reads the whole file and parses each line into a `json.RawMessage` plus the few fields it needs to filter on. It applies the filters, then returns up to `limit` records, newest first, before the cursor.
  - `ponytail:` whole-file scan. Switch to a backwards reader from EOF when the file is large; that is also when rotation is needed.

**`audit.read {before?, limit?, server?, kinds?, outcomes?, text?}`** is a UI-door method. It returns `{records: [{seq, record}], next?, skipped}`.

| Parameter or field | Meaning |
|---|---|
| `before` | A `seq`. Only records with a smaller `seq` are returned. Omitted means from the newest. |
| `limit` | Default 200, maximum 500. More than 500 is refused with -32602. |
| `server` | An exact server name. |
| `kinds` | Any of `exec`, `config`, `file`, `tunnel`. An exec record has no `kind` field. |
| `outcomes` | Any of `allowed`, `auto`, `denied`, `expired`, `cancelled`, `error`. `auto` means `approval: "auto"`. `allowed` means allowed and not auto. The others match `outcome`, with `cancelled` matching `cancelled` and `cancelled_running`. These apply to exec records only. |
| `text` | Case-insensitive substring match on the raw JSON line. |
| `next` | The smallest `seq` returned. Present only when older matching records exist. |
| `skipped` | The number of lines that did not parse. |

- Params are decoded strictly, as for `files.*`. `audit.read` needs an unlocked vault; otherwise it returns `ErrLocked`. It counts as UI activity, as every UI-door request except `status` does.
- **`audit.appended {seq, record}`** is a notification. The hub sends it for every record written, of every kind, through the `OnAppend` callback, and only while the vault is unlocked. It never waits on the renderer.
- **Protocol 8** (`idle.go`, `protocol.ts`, the `fakeHub` fixture). Nothing is added to the MCP door.
- **Redaction.** Records are returned as stored. The audit already holds no output and no vault secret, and 4a adds only counts.

## Desktop

- **Tab strip.** A fixed **Audit** tab after `⌂ Hosts`, with a list icon. Like Hosts it cannot be closed, and it keeps its state (filters, scroll, expanded rows) while other tabs are active. `TabSet` gains an `audit` home-like entry beside `active === undefined` (Hosts).
- **Filter bar:**
  - a host select: All, then each server;
  - toggle chips: **Exec**, **Auto**, **Denied**, **Config**, **Files**, **Tunnels**. Several can be on; none on means all;
  - a search box. It applies on Enter, or 300 ms after the last keystroke. That timer only follows the human's typing and is never a poll;
  - a **Refresh** button.
- **Table, newest first.** Columns:
  - **time**: local, `HH:MM:SS`, with the date shown when it is not today;
  - **host**;
  - **badge**: allowed, auto, denied, expired, cancelled or error for exec, plus a `sudo` badge; the action for config; upload, download, delete, mkdir or rename for files; start or end for tunnels;
  - **main text**: the command for exec, mono and cut to one line; `action` plus its detail for config (`autoAllowOn 15m`, `save: host → 10.0.0.2`); the first path for files; the listen address → target for tunnels;
  - **side**: `exit N`, the wait (`waitMs` as `12 s`), and `N masked` when `redacted` is present.
- **Expanding a row** (click, or Enter on the focused row) shows:
  - the full command in a code block;
  - the AI's description, labelled "AI's description · unverified";
  - reason, client, timeout and duration;
  - `redacted` by kind;
  - the raw JSON, collapsed.
- **Paging and live updates:**
  - **Load older** calls `audit.read` with `before: next`.
  - A footer shows "N malformed lines skipped" when `skipped > 0`, and the audit file's path.
  - `audit.appended` records that match the current filters are inserted at the top. If the table is scrolled away from the top, a pill "N new" appears instead of moving the rows under the cursor; clicking it scrolls up.
  - Records are deduplicated by `seq`.
  - On a `locked` notification the list is cleared. It is read again when the tab is next shown after an unlock.
  - The renderer never calls the hub in response to `audit.appended`, `locked`, or a timer.
- **Hub calls** go only through `transport.ts` (`hub.auditRead`). `ipc.ts`'s `REQUEST_METHODS` gains `audit.read`, and `NOTIFY_METHODS` stays as it is, since notifications from the hub are not in that list.
- **Nothing on this tab can change state.** It has no decision buttons, so the 500 ms rule does not apply.

## Testing

- **`broker`:**
  - the line counter across reopen;
  - `OnAppend` fires once per record, with the right `seq`;
  - `Read`: paging with `before`/`next`, each filter, a combined filter, `limit` bounds, a malformed line counted in `skipped`, and a read during concurrent appends (race).
- **`hub`:**
  - `audit.read` returns `ErrLocked` while locked and counts as activity;
  - strict params, with `limit: 501` giving -32602;
  - `audit.appended` is sent for an exec, a config change, a file job and a tunnel, and nothing is sent while locked;
  - `hello` returns protocol 8.
- **vitest:**
  - the row label and badge for each kind;
  - the outcome/kind chip → query mapping;
  - the merge: dedupe by `seq`, insert at the top, the "N new" count when scrolled;
  - the record list is cleared on lock.
- **e2e** (`desktop/e2e/audit.spec.ts`, fixture vault):
  - make an exec and deny it; arm auto-allow and run an exec; save a host edit;
  - open Audit: all of them are listed, newest first;
  - the Auto chip shows only the auto run;
  - expanding a row shows the full command and the description;
  - with the tab open, a new exec appears without Refresh;
  - Lock, then unlock: the list reloads.
- **Checks:** `go vet ./...`, `go test -race ./...`, the desktop typecheck, `npm test`, and `npm run e2e`.

## ROADMAP, README, PRODUCT and CLAUDE changes

- **ROADMAP:** 4b is done when the exit gate is met. Rotation stays deferred, with its trigger: when the audit file is large enough that a whole-file read shows.
- **README:** "The desktop app" gains an **Audit** bullet.
- **PRODUCT.md:** Capabilities lists the Audit tab among the screens.
- **CLAUDE.md:**
  - The UI-door method list gains `audit.read` and the `audit.appended` notification, and the protocol becomes 8.
  - The desktop section gets an Audit tab bullet.
