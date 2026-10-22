---
displayName: Agent activity plan
tags: plan
status: active
date: "2026-10-22"
spec: "[[superpowers/specs/2026-10-22-agent-activity-design|Agent activity design]]"
---
# Agent Activity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** People watching a desktop see which agents (outside, through the agent API, and inside, through the in-desktop `deskyard`) are present, what each is doing now and did in the last 15 minutes, and their clicks and typing drawn on the screen.

**Architecture:**
- **Shared vocabulary** in a new pure-Go package `internal/activity`: secret masking, screen-action parsing, the inside event format, and an in-memory `Tracker` that turns actions into presence, summaries and live screen events.
- **Outside agents** are recorded by the supervisor in the agent API handlers (exec, CDP, forward). **Inside agents** are recorded by the in-desktop `deskyard`, which sends one UDP datagram per action to a hidden `deskyard activity-serve` (a 15-minute ring, under supervisord); the supervisor reads it through Caddy at `/_activity/` while UI pages are open. Every exec the supervisor starts carries `DESKYARD_ACTIVITY=off`, so nothing is counted twice.
- **The UI** shows agents after people in the desktop bar's avatar row, the viewers popover, wall tiles and the details page, with a summary per agent. The **screen overlay** (last task) draws marks from `/api/desktops/{name}/screen-events` over the frame, through a `screenGeometry()` interface that the Selkies work replaces.

**Tech Stack:** Go 1.27 (supervisor and CLI), Preact + TypeScript strict + Tailwind (web), pytest + Playwright (integration), Node's built-in test runner (`node --test`, native TS type stripping, Node ≥ 24) for pure web modules.

**Spec:** `docs/superpowers/specs/2026-10-22-agent-activity-design.md` (context: `docs/superpowers/specs/2026-10-21-supervisor-design.md`; the streamer change: `docs/superpowers/specs/2026-10-22-selkies-design.md`).

## Task order and parallelism

Each task's files are listed in the task; the table says which tasks may run at the same time in separate worktrees.

| Task | Needs merged first | Can run in parallel with |
|---|---|---|
| 1. Activity vocabulary (`internal/activity`) | — | 8 |
| 2. Tracker | 1 | 3, 4, 8 |
| 3. In-desktop activity stream (`activity-serve`, image) | 1 | 2, 4, 8 |
| 4. Recording in the in-desktop CLI | 1 | 2, 3, 8 |
| 5. Supervisor wiring, API and events | 2 | 3, 4, 8 |
| 6. Recording outside agents | 5 | 7, 8 |
| 7. Reading the inside stream | 3, 4, 5 | 6, 8 |
| 8. Presence and summary UI | — (JSON shapes are fixed in this plan); its Playwright step needs 6 | 1–7 |
| 9. Screen overlay | 6, 8 | — (last; streamer-dependent) |

Shared-file notes for merging: Tasks 6 and 7 both touch `internal/server/server.go` (6 only `New`, 7 only `Run`). Tasks 3 and 4 both add files under `internal/activity/` and `internal/cli/app/` but no file is touched by both. Tasks 8 and 9 both edit `web/src/desktop/desktop.tsx`, so 9 follows 8.

## Global Constraints

- **Presence:** an agent is present while something is running, or for 5 minutes after its last action; then it is gone. `active` means activity in the last 10 seconds.
- **History:** the supervisor keeps per desktop the last 15 minutes of events and what's running now, in memory only. Nothing is written to disk; nothing survives a supervisor restart.
- **Browser:** "using the browser" while any CDP connection is open, or during an HTTP call to the CDP endpoint, and for 30 s after the last; each session adds one "Recently" line ("used the browser · 4 min").
- **Recently:** up to 20 lines, newest first, adjacent similar screen actions merged ("clicked 14 times", "typed 3 times").
- **`Desktop.agents`:** a list of `{"id","name","owner","inside":bool,"color","active":bool,"lastActive":time}`, in Desktop JSON and in `viewers` events next to `viewers` and `forwards` (and `mics` once the audio work lands; all fields are additive).
- **API:** `GET /api/desktops/{name}/activity` for every signed-in account that can see the desktop; agent tokens can't use it. `activity` events on `/api/events` at most once a second per desktop. `GET /api/desktops/{name}/screen-events` (SSE), produced only while it has subscribers.
- **Inside naming:** `claude` gives "Claude Code", `codex` gives "Codex", an interactive shell gives "a terminal", otherwise the parent program's name; shown with "(in this desktop)". One entry per program name per desktop, id `inside:<name>`, no owner, colour from the id.
- **Inside stream:** each event has a sequence number and timestamp; the ring holds the last 15 minutes, capped at 5,000 events, oldest dropped. Served by a local process, read through the desktop's Caddy at `/_activity/` behind `X-Deskyard-Token`. Writing never blocks: if the stream isn't there or is full, the event is dropped.
- **Untrusted inside input:** the supervisor caps every field's length, masks again, allows at most 50 events a second per desktop, and never lets inside events change outside agents. A command start with no end after 15 minutes is dropped.
- **Secrets:** typed text is never recorded (`type` carries a character count only); key events carry key names; a command's display form is cut at 120 characters with `•••` for values after `--password`, `--token`, `-p`, `--secret`, `--key`, for `KEY=value` with a secret-looking key, and for token-like strings (`dyt_…`, `sk-…`, `ghp_…`, long base64 or hex); exec environment variables are never shown. Masking patterns live in one file with tests.
- **No double counting:** every exec the supervisor starts sets `DESKYARD_ACTIVITY=off` (agent API execs, thumbnails, `deskyard size`, the bar's key presses, `forward-helper`); the in-desktop `deskyard` records nothing when it is set.
- **Screen actions from outside:** an exec is a screen action when `argv[0]`'s base name is `deskyard` and the first non-flag argument is one of `click|double-click|triple-click|mouse-down|mouse-up|move|drag|scroll|type|key|hold-key|screenshot|zoom` (global `-d NAME` skipped). Anything wrapped in a shell is a plain command. Marks are drawn when the action starts, whatever its exit code.
- **Never slow an agent:** recording must not block, fail or slow an agent's command. Every send to a subscriber or socket is non-blocking.
- **Overlay:** drawn only in the viewer's page, never in the desktop. Marks in the agent's colour; click ripple + name label fading after 1.5 s; two/three rings for double/triple; line for drag; arrow for scroll; nothing for move; key cap near the last click point for 1 s; "<name> is typing…" near the last click point (or the bottom edge), hidden 1.5 s after the last type event. A mark outside the current screen is dropped.

## Review Focus

- **The desktop stops or restarts while an inside agent's command is running.** The running command must disappear from "Now" (it will never end), not linger for 15 minutes. Test: Task 2 (`TestResetInsideDropsRunning`) and Task 7 (epoch change).
- **Something inside floods the stream** (a loop sending datagrams, or fake timestamps). The supervisor keeps at most 50 events per second per desktop and 5,000 stored, `activity` events stay at one per second, and outside agents are untouched. Test: Task 2 (`TestInsideFloodIsCapped`), Task 3 (`TestRingCap`).
- **An outside agent passes `-e DESKYARD_ACTIVITY=on`** (or runs `sh -c "deskyard click …"`). The supervisor's `off` still wins, and the nested click is shown once, as a plain command, never as an inside agent. Test: Task 6 (`test_agent_cannot_turn_inside_recording_on`), Task 7 (`test_nested_deskyard_not_counted_inside`).
- **Access is revoked while the desktop page has `screen-events` open.** No further marks reach that account. Test: Task 5 (`test_screen_events_stop_after_revoke`).
- **An agent holds only a CDP connection or a forward link, and nothing else.** It stays present with "using the browser" or its ports under "Now"; after closing it goes away 30 s (browser) plus 5 minutes later. Test: Task 2 (`TestBrowserSession`, `TestForwardKeepsPresent`).

---

### Task 1: Activity vocabulary (`internal/activity`)

**Files:**
- Create: `supervisor/internal/activity/activity.go` — package doc, `Agent`, `Ref`, `InsideAgent`, `Color`, constants.
- Create: `supervisor/internal/activity/mask.go` — every masking pattern, nowhere else.
- Create: `supervisor/internal/activity/screen.go` — screen-action parsing.
- Create: `supervisor/internal/activity/event.go` — the inside stream's event and `Sanitize`.
- Test: `supervisor/internal/activity/mask_test.go`, `screen_test.go`, `event_test.go`

**Interfaces:**
- Produces:
  ```go
  const StreamAddr = "127.0.0.1:6092" // activity-serve: UDP in, HTTP out
  const MaxDatagram = 4096
  type Ref struct{ Name, ID string }            // a desktop
  type Agent struct{ ID, Name, Owner string; Inside bool }
  func InsideAgent(program string) Agent         // ID "inside:"+program, Inside true, Owner ""
  func Color(id string) string                   // one of the six avatar colours, h = h*31 + rune
  func DisplayCommand(argv []string) string      // masked, joined, ≤120 runes + "…"
  func MaskText(s string) string                 // the same masking on free text
  var ScreenCommands = []string{"click","double-click","triple-click","mouse-down","mouse-up","move","drag","scroll","type","key","hold-key","screenshot","zoom"}
  type ScreenAction struct {
      Action string   `json:"action"`
      Pos    []int    `json:"pos,omitempty"`    // [x y], or [x1 y1 x2 y2] for drag
      Button string   `json:"button,omitempty"`
      Dir    string   `json:"dir,omitempty"`
      N      int      `json:"n,omitempty"`
      Keys   []string `json:"keys,omitempty"`
      Chars  int      `json:"chars,omitempty"`  // type: character count only
  }
  func ParseScreenArgs(action string, args []string) (ScreenAction, bool)
  func ParseScreenArgv(argv []string) (ScreenAction, bool)
  type Event struct {
      Seq    uint64        `json:"seq"`
      T      time.Time     `json:"t"`
      Agent  string        `json:"agent"`             // program name, inside only
      Kind   string        `json:"kind"`              // "screen", "cmd-start", "cmd-end"
      Screen *ScreenAction `json:"screen,omitempty"`
      ID     string        `json:"id,omitempty"`      // pairs cmd-start and cmd-end
      Cmd    string        `json:"cmd,omitempty"`     // display form
      Cwd    string        `json:"cwd,omitempty"`
      Exit   *int          `json:"exit,omitempty"`
  }
  func Sanitize(e Event) (Event, bool)
  ```

- [ ] **Step 1: Write the failing tests**

`mask_test.go`, `TestDisplayCommand` (table; input argv → exact output):
```go
{[]string{"deploy", "--token", "abc"}, "deploy --token •••"},
{[]string{"deploy", "--token=abc"}, "deploy --token=•••"},
{[]string{"mysql", "-p", "hunter2", "db"}, "mysql -p ••• db"},
{[]string{"env", "GITHUB_TOKEN=x1", "API_KEY=y", "PATH=/bin", "make"}, "env GITHUB_TOKEN=••• API_KEY=••• PATH=/bin make"},
{[]string{"curl", "-H", "Authorization: Bearer dyt_ab12_cd34"}, "curl -H 'Authorization: Bearer •••'"},
{[]string{"sh", "-c", "OPENAI_KEY=sk-proj-abc123 ./run --secret s"}, "sh -c 'OPENAI_KEY=••• ./run --secret •••'"},
{[]string{"git", "clone", "https://ghp_aaaaaaaaaaaaaaaaaaaa@github.com/x"}, "git clone https://•••@github.com/x"},
{[]string{"echo", strings.Repeat("ab12", 10)}, "echo •••"},                 // 40 chars of hex
{[]string{"echo", "hello world"}, "echo 'hello world'"},
{[]string{"ls", "-la"}, "ls -la"},
```
`TestDisplayCommandCut`: `DisplayCommand([]string{"echo", strings.Repeat("a b ", 100)})` has `utf8.RuneCountInString(got) == 121` and ends with `"…"`.

`screen_test.go`, `TestParseScreenArgv` (argv → ok, result):
```go
{"deskyard","click","10","20"}                   → click Pos [10 20] Button "left"
{"/usr/local/bin/deskyard","-d","alice","drag","1","2","3","4"} → drag Pos [1 2 3 4]
{"deskyard","--desktop=alice","scroll","5","6","down"} → scroll Pos [5 6] Dir "down" N 3
{"deskyard","double-click","7","8"}              → double-click Pos [7 8]
{"deskyard","type","hunter2","x"}                → type Chars 9, Pos nil
{"deskyard","key","ctrl+l","Return"}             → key Keys ["ctrl+l","Return"]
{"deskyard","hold-key","shift","0.5"}            → hold-key Keys ["shift"]
{"deskyard","screenshot","-"}                    → screenshot (no Pos)
{"deskyard","zoom","0","0","10","10","-"}        → zoom (no Pos)
{"deskyard","mouse-down","right"}                → mouse-down Button "right"
not ok: {"sh","-c","deskyard click 1 2"}, {"deskyard","exec","--","click"}, {"deskyard","click","x","y"},
        {"deskyard","wait","1"}, {"deskyard","click","--help"}, {"desk","click","1","2"}, {}
```
Plus: `json.Marshal` of the `type` result does not contain `hunter2`.

`event_test.go`, `TestSanitize`:
- `Agent` of 100 runes is cut to 40; `Cmd` `"deploy --token abc"` comes back `"deploy --token •••"` and is cut to 120 runes; `Cwd` cut to 200; `ID` cut to 32.
- Dropped (`ok == false`): unknown `Kind`; `Kind "screen"` with nil `Screen` or an action not in `ScreenCommands`; `Pos` of length other than 0, 2 or 4; `cmd-start` without `ID`; empty `Agent`.
- `Keys` beyond 8 are cut, each to 32 runes; `Pos` values clamped to 0–65535; `N` clamped to 0–100; `Chars` clamped to 0–1,000,000; a `type` action keeps only `Action` and `Chars`.

`TestColor`: `Color("x") == Color("x")` and every result is one of `"#2457c5", "#1e8a5a", "#9a4fb5", "#b7791f", "#c2410c", "#0e7490"` (the palette in `web/src/components/avatar.tsx`).

- [ ] **Step 2: Run them to see them fail**

Run: `cd supervisor && go test ./internal/activity/`
Expected: FAIL (package doesn't exist / undefined names).

- [ ] **Step 3: Implement the four files**

- `mask.go`: masking works on strings so `DisplayCommand` and `MaskText` share it. Per argument: a value following a secret flag (`--password`, `--token`, `-p`, `--secret`, `--key`) becomes `•••`; then regex substitutions on every argument (so a script inside `sh -c` is masked too): `--(password|token|secret|key)(=|\s+)\S+`; `\b([A-Za-z0-9_]*(?i:pass|token|secret|key|auth|credential)[A-Za-z0-9_]*)=[^\s'"]+`; token-like substrings `dyt_[A-Za-z0-9_]+`, `sk-[A-Za-z0-9_-]+`, `gh[pousr]_[A-Za-z0-9]+`, `github_pat_[A-Za-z0-9_]+`, hex runs of 32+ and base64/url-safe runs of 32+ that contain both letters and digits. Arguments containing whitespace or quotes are wrapped in single quotes. Cut at 120 runes, append `…`.
- `screen.go`: `ParseScreenArgv` skips `-d NAME`, `--desktop NAME`, `--desktop=NAME`; any `-h`/`--help` makes it false. `ParseScreenArgs` checks each subcommand's arguments as the CLI's spec does (`internal/cli/app/spec.go`): integers where coordinates go, enums for button and direction, scroll `N` default 3, button default `left`, a trailing `FILE`/`-` ignored for screenshot and zoom.
- `event.go`: `Sanitize` as the tests pin; it runs `MaskText` on `Cmd`.

- [ ] **Step 4: Run them to see them pass**

Run: `cd supervisor && go test ./internal/activity/ && go vet ./internal/activity/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add supervisor/internal/activity
git commit -m "activity: masking, screen actions and the inside event format"
```

---

### Task 2: Tracker

**Files:**
- Create: `supervisor/internal/activity/tracker.go` — state per desktop, the recording methods, `Tick`, screen subscriptions.
- Create: `supervisor/internal/activity/summary.go` — `Present`, `Summary`, merging of Recently.
- Test: `supervisor/internal/activity/tracker_test.go`

**Interfaces:**
- Consumes: Task 1 (`Ref`, `Agent`, `InsideAgent`, `Color`, `ScreenAction`, `Event`, `Sanitize`).
- Produces:
  ```go
  type Tracker struct {
      Now           func() time.Time
      Presence      time.Duration // 5m
      ActiveFor     time.Duration // 10s
      Window        time.Duration // 15m
      BrowserLinger time.Duration // 30s
      InsideRate    int           // 50 per second per desktop
      MaxEvents     int           // 5000 kept per desktop
      OnPresence    func(Ref)     // the Present list of a desktop may have changed
      OnSummary     func(Ref)     // the Summary of a desktop may have changed
  }
  func NewTracker() *Tracker // the defaults above, Now = time.Now
  func (t *Tracker) CmdStart(d Ref, a Agent, id, display, cwd string)
  func (t *Tracker) CmdEnd(d Ref, a Agent, id string, exit *int) // nil exit = "disconnected"
  func (t *Tracker) Screen(d Ref, a Agent, s ScreenAction)
  func (t *Tracker) Copy(d Ref, a Agent, direction, path string, size int64) // direction "in" | "out"
  func (t *Tracker) BrowserOpen(d Ref, a Agent) (done func())
  func (t *Tracker) BrowserTouch(d Ref, a Agent)
  func (t *Tracker) ForwardOpen(d Ref, a Agent) (done func())
  func (t *Tracker) Inside(d Ref, e Event) bool // false when sanitising or the rate limit dropped it
  func (t *Tracker) ResetInside(d Ref)          // drop running inside commands
  func (t *Tracker) Forget(agentID string)      // every desktop
  func (t *Tracker) ForgetDesktop(name string)
  func (t *Tracker) Tick()
  func (t *Tracker) Present(name string) []Presence // never nil; sorted by name, then ID
  func (t *Tracker) Summary(name string) []Summary  // never nil; same order
  func (t *Tracker) WatchScreen(name string) (<-chan ScreenEvent, func())
  func (t *Tracker) Watched(name string) bool

  type Presence struct {
      ID         string    `json:"id"`
      Name       string    `json:"name"`
      Owner      string    `json:"owner"`
      Inside     bool      `json:"inside"`
      Color      string    `json:"color"`
      Active     bool      `json:"active"`
      LastActive time.Time `json:"lastActive"`
  }
  type NowItem struct {
      Kind  string    `json:"kind"`           // "command", "browser", "forward"
      Text  string    `json:"text,omitempty"` // command display form; forward: "3000 in · 5173 out"
      Since time.Time `json:"since"`
  }
  type RecentItem struct {
      Kind         string    `json:"kind"`  // "command", "screen", "browser", "copy"
      T            time.Time `json:"t"`     // end time (command, browser) or time of the newest merged action
      Text         string    `json:"text,omitempty"`
      Exit         *int      `json:"exit,omitempty"`
      Disconnected bool      `json:"disconnected,omitempty"`
      DurationMs   int64     `json:"durationMs,omitempty"`
      Action       string    `json:"action,omitempty"`
      Count        int       `json:"count,omitempty"`
      Direction    string    `json:"direction,omitempty"`
      Path         string    `json:"path,omitempty"`
      Size         int64     `json:"size,omitempty"`
  }
  type Summary struct {
      Presence
      Now    []NowItem    `json:"now"`    // never nil
      Recent []RecentItem `json:"recent"` // never nil, ≤ 20, newest first
  }
  type ScreenEvent struct {
      Agent ScreenAgent `json:"agent"`
      T     time.Time   `json:"t"`
      ScreenAction
  }
  type ScreenAgent struct {
      ID     string `json:"id"`
      Name   string `json:"name"`
      Color  string `json:"color"`
      Inside bool   `json:"inside"`
  }
  ```

- [ ] **Step 1: Write the failing tests**

All tests use a fake clock (`tr.Now = func() time.Time { return now }`, advancing `now`), and count `OnPresence`/`OnSummary` calls. `d := Ref{"alice", "id-1"}`, `bot := Agent{ID: "tok1", Name: "deploy-bot", Owner: "zef"}`.

- `TestPresenceFollowsActivity`: `Screen(d, bot, click)` → `Present("alice")` is one entry with `ID "tok1"`, `Owner "zef"`, `Color == Color("tok1")`, `Active true`. At +11 s after `Tick`: `Active false`, still present, `OnPresence` called. At +5 m +1 s after `Tick`: `Present` is empty, `OnPresence` called again.
- `TestRunningCommandKeepsPresent`: `CmdStart(d, bot, "c1", "npm test", "/workspace")`; at +20 m after `Tick` still present and `Summary[0].Now == [{Kind "command", Text "npm test", Since t0}]`.
- `TestCommandEnds`: exit 3 after 2 s → Recently `{Kind "command", Text "npm test", Exit 3, DurationMs 2000}`; `CmdEnd(..., nil)` → `Disconnected true`, `Exit nil`; `CmdEnd` for an unknown id does nothing.
- `TestScreenMerge`: 14 clicks → one item `{Kind "screen", Action "click", Count 14}`; click, type, click → three items, newest first; 3 screenshots → `{Action "screenshot", Count 3}`.
- `TestRecentLimitAndWindow`: 25 commands → 20 items; an item older than 15 m is gone after `Tick`.
- `TestCopy`: `Copy(d, bot, "in", "/workspace/a.pdf", 1234)` → `{Kind "copy", Direction "in", Path "/workspace/a.pdf", Size 1234}`.
- `TestBrowserSession`: `done := BrowserOpen(d, bot)` → `Now` has `{Kind "browser", Since t0}`; `done()` at +4 m; at +4 m 29 s still in `Now`; at +4 m 31 s after `Tick`: not in `Now`, Recently has `{Kind "browser", DurationMs 240000}`; present until +9 m (5 minutes after the last browser use), gone after `Tick` at +9 m 1 s. A `BrowserTouch` alone also shows "using" for 30 s and makes one session.
- `TestForwardKeepsPresent`: `done := ForwardOpen(d, bot)`; at +20 m still present; after `done()` and 5 m 1 s more, gone.
- `TestInsideAgents`: `Inside(d, Event{Agent: "a terminal", Kind: "screen", Screen: &click})` → presence `{ID "inside:a terminal", Name "a terminal", Inside true, Owner ""}`; an inside `cmd-end` with an outside command's id ("c1") leaves that command running; a `cmd-start` with no end is dropped from `Now` after 15 m (`Tick`); an event with a timestamp 1 h in the future is stored at `Now()`, one 20 m in the past is dropped.
- `TestInsideFloodIsCapped`: 60 inside events with the same second → `Inside` returns true 50 times; 6,000 events spread over 15 m → at most 5,000 kept (oldest gone); outside agent `bot`'s summary is unchanged throughout.
- `TestResetInsideDropsRunning`: inside `cmd-start` then `ResetInside(d)` → not in `Now`; Recently has no entry for it.
- `TestForget`: `Forget("tok1")` → absent at once, `OnPresence` called.
- `TestWatchScreen`: with no watcher, `Screen` builds no `ScreenEvent` (`Watched("alice") == false`); with one, click, drag, scroll, key, hold-key, type arrive, but move, screenshot, zoom, mouse-down, mouse-up don't; the type event has `Chars` and no text; a watcher that never reads doesn't block `Screen` (1,000 calls return within 100 ms); after the cancel func, the channel is closed.
- `TestCallbacksOutsideLock`: `OnPresence` calls `Present` and `Summary` without deadlocking.

- [ ] **Step 2: Run them to see them fail**

Run: `cd supervisor && go test ./internal/activity/ -run 'Tracker|Presence|Command|Screen|Recent|Copy|Browser|Forward|Inside|Forget|Callbacks'`
Expected: FAIL (undefined: `NewTracker`).

- [ ] **Step 3: Implement `tracker.go` and `summary.go`**

One mutex for the tracker. Per desktop: agents by ID (with `lastActive`), running commands by `agentID+"/"+id` (inside ones also marked inside with their start time), open browser and forward counts per agent, browser session start and last use, recent items (append; prune by `Window` and `MaxEvents` on every write and in `Tick`), the last published `Present` (to decide in `Tick` whether to call `OnPresence`), a per-second bucket for inside events (bucket = clamped event time truncated to the second), and screen watchers (channel buffer 64, non-blocking send). Every recording method updates `lastActive`. Callbacks are collected under the lock and called after unlocking. Merging adjacent screen items happens in `Summary`, not at write time.

- [ ] **Step 4: Run them to see them pass**

Run: `cd supervisor && go test -race ./internal/activity/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add supervisor/internal/activity
git commit -m "activity: tracker for presence, summaries and screen events"
```

---

### Task 3: In-desktop activity stream

**Files:**
- Create: `supervisor/internal/activity/ring.go` — the 15-minute ring.
- Create: `supervisor/internal/cli/app/cmd_activity.go` — hidden `activity-serve`.
- Modify: `supervisor/internal/cli/app/spec.go` — the hidden command entry, next to `forward-helper`.
- Modify: `images/debian-xfce/rootfs/etc/supervisor/conf.d/workspace.conf` — `[program:activity]`.
- Modify: `images/debian-xfce/rootfs/etc/caddy/Caddyfile` — the `/_activity/` route.
- Create: `docs/Architecture/Activity stream.md` — component page (`status: built`, `partOf: "[[Architecture/Desktop]]"`, `uses: [[Architecture/Caddy]], [[Architecture/Deskyard CLI]]`, `spec:` this plan's spec).
- Test: `supervisor/internal/activity/ring_test.go`, `supervisor/internal/cli/app/cmd_activity_test.go`, `images/debian-xfce/tests/test_activity.py`

**Interfaces:**
- Consumes: Task 1 (`Event`, `Sanitize`, `StreamAddr`, `MaxDatagram`).
- Produces:
  ```go
  func NewRing(window time.Duration, max int, now func() time.Time) *Ring // 15m, 5000
  func (r *Ring) Epoch() string                // random, fixed for the ring's life
  func (r *Ring) Add(e Event) Event            // sets Seq (from 1) and T = now
  func (r *Ring) Since(after uint64) []Event   // events with Seq > after, oldest first
  func (r *Ring) Wait(ctx context.Context, after uint64) []Event // blocks until something newer or ctx ends
  ```
  The wire protocol Task 7 reads: `GET /events?epoch=E&after=N` answers `200`, header `X-Activity-Epoch: <epoch>`, body `application/x-ndjson`: one `Event` per line, first everything newer than `N` (everything, when `E` isn't the current epoch), then live events as they arrive, with an empty line every 15 s. Through the desktop's Caddy it is `/_activity/events?…` with `X-Deskyard-Token`.

- [ ] **Step 1: Write the failing tests**

- `ring_test.go`: `TestRingSeqAndSince` (three adds get Seq 1, 2, 3; `Since(1)` returns 2 and 3); `TestRingWindow` (an event 15 m 1 s old is gone from `Since(0)`); `TestRingCap` (5,001 adds keep 5,000, Seq 2–5001); `TestRingWait` (`Wait` returns when an event is added from another goroutine, and returns nil when ctx is cancelled).
- `cmd_activity_test.go`, `TestActivityServe`: start `runActivityServe` on a free `127.0.0.1` port in a goroutine (ctx-cancelled at the end); send two UDP datagrams (a valid screen event, then `not json`) and one of 5,000 bytes; `GET /events?after=0` streams exactly one event with `Seq 1`, `Agent` as sent, `T` set; a second valid datagram arrives on the same open stream; `GET /events?epoch=other&after=99` gets the full backlog; the response carries `X-Activity-Epoch`. `TestHiddenCommands` (existing) still passes, so `activity-serve` is absent from help, guide and describe.
- `images/debian-xfce/tests/test_activity.py`:
  - `test_activity_stream_behind_token(sandbox)`: `sandbox.get("/_activity/events?after=0", headers={})` without the token header is 401.
  - `test_activity_stream_receives_events(sandbox)`: `sandbox.exec("printf '%s' '{\"agent\":\"probe\",\"kind\":\"screen\",\"screen\":{\"action\":\"click\",\"pos\":[1,2]}}' > /dev/udp/127.0.0.1/6092")`, then reading `/_activity/events?after=0` (streamed, first line) gives `agent == "probe"`, `screen.pos == [1, 2]`, `seq == 1`.
  - `test_activity_process_runs_as_user(sandbox)`: `ps -o user= -C deskyard` for the `activity-serve` process is `user`.

- [ ] **Step 2: Run them to see them fail**

Run: `cd supervisor && go test ./internal/activity/ ./internal/cli/app/ -run 'Ring|ActivityServe'`
Expected: FAIL (undefined: `NewRing`, `runActivityServe`).

- [ ] **Step 3: Implement**

- `ring.go` as the interface says (slice plus a `sync.Cond`-style broadcast channel replaced on each `Add`).
- `cmd_activity.go`: `activity-serve --listen ADDR` (default `activity.StreamAddr`) opens a UDP socket and a TCP HTTP listener on the same address; each datagram ≤ `MaxDatagram` is unmarshalled, passed through `Sanitize` (dropped if not ok) and `Add`ed; `GET /events` as in Produces; any other path 404.
- `spec.go`: `{Name: "activity-serve", Group: "shell", Hidden: true, Summary: "the desktop's activity stream", Usage: "activity-serve [--listen ADDR]", Flags: --listen (string, default 127.0.0.1:6092), Where: "inside a desktop", run: runActivityServe}`.
- `workspace.conf`: `[program:activity]`, `command=/usr/local/bin/deskyard activity-serve`, `user=user`, `autorestart=true`, `environment=HOME="/workspace",USER="user",PATH="%(ENV_WORKSPACE_USER_PATH)s"`, stdout/stderr like the `cdp` program.
- `Caddyfile`: inside the token `route`, before the final `reverse_proxy`: `handle_path /_activity/* { reverse_proxy 127.0.0.1:6092 { flush_interval -1 } }`.

The image contract stays `1`: a supervisor reading an image without the route gets 404 and backs off (Task 7).

- [ ] **Step 4: Run them to see them pass**

Run: `cd supervisor && go test ./internal/activity/ ./internal/cli/app/` then `cd images/debian-xfce/tests && uv run pytest test_activity.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add supervisor/internal/activity/ring.go supervisor/internal/activity/ring_test.go supervisor/internal/cli/app/cmd_activity.go supervisor/internal/cli/app/cmd_activity_test.go supervisor/internal/cli/app/spec.go images/debian-xfce/rootfs images/debian-xfce/tests/test_activity.py "docs/Architecture/Activity stream.md"
git commit -m "image: an activity stream in each desktop (deskyard activity-serve)"
```

---

### Task 4: Recording in the in-desktop CLI

**Files:**
- Create: `supervisor/internal/activity/procname.go` — the inside agent's name from the process tree.
- Create: `supervisor/internal/activity/send.go` — the non-blocking UDP send.
- Create: `supervisor/internal/cli/app/record.go` — `App.record` and the helpers the commands call.
- Modify: `supervisor/internal/cli/app/app.go` — `App` fields `Record`, `Caller`; defaults in `New`.
- Modify: `supervisor/internal/cli/app/cmd_desktop.go` (local branch of `runDesktop`), `cmd_exec.go` (local branches of `runExec` and `runShell`), `cmd_more.go` (`runCp` when it acts locally).
- Modify: `supervisor/internal/cli/app/app_test.go` — the harness sets `Record` (collects events) and `Caller`.
- Test: `supervisor/internal/activity/procname_test.go`, `supervisor/internal/activity/send_test.go`, `supervisor/internal/cli/app/record_test.go`

**Interfaces:**
- Consumes: Task 1 (`Event`, `ParseScreenArgs`, `DisplayCommand`, `StreamAddr`, `MaxDatagram`).
- Produces:
  ```go
  func CallerName(proc fs.FS, pid int) string // pid = the caller (os.Getppid()); proc = os.DirFS("/proc")
  func Send(e Event)                          // UDP to StreamAddr; 50 ms write deadline; errors ignored
  // App fields:
  Record func(activity.Event) // default activity.Send
  Caller func() string        // default CallerName(os.DirFS("/proc"), os.Getppid())
  ```

- [ ] **Step 1: Write the failing tests**

- `procname_test.go`, `TestCallerName` with `fstest.MapFS` holding `N/stat` (the `ppid` field), `N/cmdline` (NUL-separated), `N/comm`, and `N/fd/0` as a symlink entry (`Mode: fs.ModeSymlink`, `Data` = target):
  - pid 50 `bash` (fd/0 → `/dev/pts/0`) under pid 40 `node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js` with `comm` `claude` → `"Claude Code"`.
  - pid 50 `sh -c …` under pid 40 `codex` → `"Codex"`.
  - pid 50 `bash` with fd/0 → `/dev/pts/1`, parents `xfce4-terminal` → `"a terminal"`.
  - pid 50 `sh` with fd/0 → `pipe:[123]` under pid 40 `make` → `"make"`.
  - pid 50 `python3 agent.py` → `"python3"`.
  - pid 50 `sh` (no tty) under pid 1 `sh` (no tty) → `"sh"`.
  - a `comm` of 100 runes is cut to 40; a ppid cycle stops after 32 steps; a missing pid gives `"a terminal"`.
- `send_test.go`, `TestSendNeverBlocks`: `Send` to a port nobody listens on returns within 100 ms; an event marshalling to more than `MaxDatagram` bytes is not sent (a listening UDP socket receives nothing).
- `record_test.go` (harness with `inside = true`, `Caller` returning `"a terminal"`):
  - `TestRecordsLocalScreenAction`: `click 10 20` records one `Event{Agent "a terminal", Kind "screen", Screen click Pos [10 20]}` even though running xdotool fails in the test; `type secret words` records `Chars 12` and no event field contains `secret`.
  - `TestRecordsLocalExec`: `exec -- sh -c "exit 3"` records `cmd-start` (`Cmd "sh -c 'exit 3'"`, an `ID`) then `cmd-end` with the same `ID` and `Exit 3`; `exec -- deploy --token abc` records `Cmd "deploy --token •••"`.
  - `TestRecordsShellAndCp`: local `shell` records start/end with `Cmd "shell"`; local `cp a b` records start/end with `Cmd "cp a b"`.
  - `TestNothingRecordedWhenOff`: with `DESKYARD_ACTIVITY=off` in the harness env, `click 1 2` and `exec -- true` record nothing.
  - `TestNothingRecordedRemotely`: in `remoteHarness`, `-d NAME click 1 2` records nothing (the supervisor counts it); `size`, `cursor`, `wait 0` record nothing locally either.

- [ ] **Step 2: Run them to see them fail**

Run: `cd supervisor && go test ./internal/activity/ ./internal/cli/app/ -run 'Caller|Send|Record|Nothing'`
Expected: FAIL.

- [ ] **Step 3: Implement**

- `CallerName`: walk from `pid` through parents (stat field 4), at most 32 steps. For each process, look at `basename(argv[0])`, `basename(argv[1])` and `comm`: `claude` or a path containing `@anthropic-ai/claude-code` → `"Claude Code"`; `codex` → `"Codex"`; the first such ancestor wins. If none: starting at `pid`, a shell (`bash`, `sh`, `dash`, `zsh`, `fish`, `ksh`) whose `fd/0` links to `/dev/pts/*` or `/dev/tty*` → `"a terminal"`; a non-interactive shell is skipped; the first non-shell gives its `basename(argv[0])` (or `comm`); if only shells remain, the outermost shell's name. Read links with `fs.ReadLink`.
- `App.record(e)`: returns at once when `Getenv("DESKYARD_ACTIVITY") == "off"` or `Record` is nil; fills `Agent` from `Caller` (computed once per process). Commands call it only on their local path, after argument validation and before the action runs; `cmd-end` after it ends (exit code, or 1 when it couldn't run). Command `ID` is 8 random hex characters.

- [ ] **Step 4: Run them to see them pass**

Run: `cd supervisor && go test ./internal/activity/ ./internal/cli/... && go vet ./...`
Expected: PASS; the golden help/describe files are unchanged.

- [ ] **Step 5: Commit**

```bash
git add supervisor/internal/activity supervisor/internal/cli/app
git commit -m "cli: record screen actions and commands inside a desktop"
```

---

### Task 5: Supervisor wiring, API and events

**Files:**
- Create: `supervisor/internal/server/activity.go` — the tracker on the server, JSON, handlers, the tick loop, publishing.
- Modify: `supervisor/internal/server/server.go` — `activity *activity.Tracker`, `activityPub *throttle` fields; `Options.ActivityPresence`; `go s.activityLoop(ctx)` in `Run`.
- Modify: `supervisor/internal/server/desktops.go` — `Agents []activity.Presence` in `desktopJSON`; `s.activity.ForgetDesktop(name)` where a desktop is removed.
- Modify: `supervisor/internal/server/viewers.go` — `"agents"` in `sendViewers`.
- Modify: `supervisor/internal/server/routes.go` — the two routes.
- Modify: `supervisor/internal/config/config.go`, `supervisor/cmd/deskyard-supervisor/main.go` — `DESKYARD_ACTIVITY_PRESENCE` (a duration; default 5m; a test knob, not documented for users).
- Modify: `supervisor/tests/conftest.py` — `"DESKYARD_ACTIVITY_PRESENCE": "12s"` in the supervisor's env.
- Modify: `supervisor/API.md` — `Desktop.agents`, the `viewers` event, `activity` events, the two routes.
- Test: `supervisor/internal/server/activity_test.go`, `supervisor/tests/test_activity.py` (new)

**Interfaces:**
- Consumes: Task 2 (`Tracker`, `Presence`, `Summary`, `NowItem`, `ScreenEvent`, `WatchScreen`).
- Produces:
  ```go
  func (s *Server) agentOf(t token) activity.Agent       // {ID: t.ID, Name: t.Name, Owner: t.CreatedBy}
  func refOf(c desktops.Desktop) activity.Ref            // {c.Name, c.ID}
  func (s *Server) activityJSON(name string) activityJSON // {"name", "agents": []Summary}, with forward NowItems added
  ```
  - `GET /api/desktops/{name}/activity` → `{"name":"alice","agents":[Summary…]}`.
  - `event: activity` on `/api/events` → the same object, at most once a second per desktop (`newThrottle(time.Second)`, trailing run).
  - `event: viewers` → `{"name","viewers","forwards","agents"}`.
  - `GET /api/desktops/{name}/screen-events` (SSE) → `event: screen`, data a `ScreenEvent` (`{"agent":{"id","name","color","inside"},"t","action","pos",…}`); comment keep-alive every 25 s.
  - For Task 6, forward links get an owner: Task 6 adds `tokenID` to `forwardLink`; `activityJSON` uses `s.forwards.listFor(name, tokenID) []forwardJSON` (added here, returning nothing until Task 6 fills `tokenID`).

- [ ] **Step 1: Write the failing tests**

- `activity_test.go` (with `newTestServer`):
  - `TestAgentsInDesktopJSON`: `s.activity.Screen(Ref{"alice","id1"}, bot, click)`; `s.desktopJSON(ctx, aliceDesktop, admin).Agents` has `bot` with `Active true`; with no activity it is `[]` (marshals as `[]`, not `null`).
  - `TestActivityJSONAddsForwards`: with a forward link of `tokenID "tok1"` declaring out 5173, `activityJSON("alice")` has a `NowItem{Kind "forward", Text "5173 out"}` for `tok1`.
  - `TestPresenceChangePublishesViewers`: subscribe to `s.events`; a first `Screen` call publishes a `viewers` event whose data has `agents`; 20 `Screen` calls in 100 ms publish at most two `activity` events.
  - `TestPresenceKnob`: `Options{ActivityPresence: 2 * time.Second}` sets `s.activity.Presence` (and `ActiveFor` to the smaller of 10 s and the presence).
- `supervisor/tests/test_activity.py`:
  - `test_activity_api_shape(admin, desktop, name)`: `GET /api/desktops/{name}/activity` is `{"name": name, "agents": []}`; `admin.desktop(name)["agents"] == []`.
  - `test_activity_api_access(admin, desktop, name, supervisor)`: a regular account without access gets 404 on both `/activity` and `/screen-events`; an agent token (`make_token`) as `Authorization: Bearer` gets 401 on both.
  - `test_screen_events_stop_after_revoke(admin, desktop, name, supervisor)`: a regular account with access opens `/screen-events` (streaming, in a thread); the admin removes its access with `PUT /api/desktops/{name}/access`; the stream ends within 5 s.

- [ ] **Step 2: Run them to see them fail**

Run: `cd supervisor && go test ./internal/server/ -run 'Agents|ActivityJSON|PresenceChange|PresenceKnob'`
Expected: FAIL.

- [ ] **Step 3: Implement**

- `New`: `s.activity = activity.NewTracker()`, presence from `Options.ActivityPresence` when non-zero, `OnPresence = func(d) { s.publishViewers(d.Name, d.ID) }`, `OnSummary = func(d) { s.activityPub.do(d.Name, func() { publish activity event with desktopID d.ID }) }`.
- `activityLoop`: `s.activity.Tick()` every second until ctx ends.
- `handleActivity`: `desktopFor(w, r, auth.View)`, then `writeJSON(200, s.activityJSON(c.Name))`.
- `handleScreenEvents`: `desktopFor(View)`, `startSSE`, `WatchScreen(c.Name)`; before each send, re-check `s.Accounts.Get` and `auth.Can(acct, auth.View, c.ID)`, returning when it fails. So that a revoke closes an idle stream too, register each stream as a `conn{username, desktopID, cancel}` in a second `*viewers`, `s.screenWatchers` (it never appears in `viewers` lists), and call `s.screenWatchers.revoke(…)` in `accountsChanged` next to `s.viewers.revoke(…)`.
- `API.md`: document the four shapes above exactly, and that summaries cover the last 15 minutes and nothing is stored.

- [ ] **Step 4: Run them to see them pass**

Run: `cd supervisor && go test -race ./internal/server/ && cd tests && uv run pytest test_activity.py -k "shape or access or revoke" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add supervisor/internal/server supervisor/internal/config supervisor/cmd supervisor/tests/conftest.py supervisor/tests/test_activity.py supervisor/API.md
git commit -m "supervisor: agents in desktops, the activity API and screen events"
```

---

### Task 6: Recording outside agents

**Files:**
- Modify: `supervisor/internal/server/agent.go` — exec (commands, screen actions, copies), CDP, `execEnv`, the masked exec log line.
- Modify: `supervisor/internal/server/forward.go` — `tokenID` on `forwardLink`; `ForwardOpen`.
- Modify: `supervisor/internal/server/tokens.go` — `onGone func(token)` called for every token removed (delete, creator lost access, desktop removed) and on expiry.
- Modify: `supervisor/internal/server/server.go` — `New` only: `s.tokens.onGone = func(t token) { s.activity.Forget(t.ID) }`.
- Modify: `supervisor/internal/cli/transfer/cp.go` — `func Classify(argv []string) (direction, remotePath string, ok bool)` next to the scripts it recognises.
- Modify: `supervisor/API.md` — under the exec route: `DESKYARD_ACTIVITY=off` is always set and can't be overridden.
- Test: `supervisor/internal/cli/transfer/transfer_test.go`, `supervisor/internal/server/activity_test.go`, `supervisor/tests/test_activity.py`

**Interfaces:**
- Consumes: Task 5 (`s.activity`, `agentOf`, `refOf`, `listFor`), Task 1 (`ParseScreenArgv`, `DisplayCommand`).
- Produces: `transfer.Classify` (`"in"` for the two upload scripts, `"out"` for the download script; `remotePath` is `argv[4]`); `execEnv = []string{"WORKSPACE_PROXY_TOKEN=", "DESKYARD_ACTIVITY=off"}`.

- [ ] **Step 1: Write the failing tests**

- `transfer_test.go`, `TestClassify`: the argv `Upload` builds for a file, a directory and stdin → `("in", path, true)`; `Download`'s argv → `("out", path, true)`; `{"sh","-c","cat > x","sh","/p"}` → false.
- `activity_test.go`, `TestTokenGoneForgetsAgent`: a token with presence on a desktop; `ts.remove` (delete) and, for a second token with `Expires` in the past, one pass of the expiry check (extract the loop body into `ts.expireNow(now time.Time)`) each call `onGone` once, after which `s.activity.Present(name)` no longer lists it.
- `activity_test.go`, `TestExecEnvWins`: `execEnvFor(map[string]string{"DESKYARD_ACTIVITY": "on", "FOO": "1"})` contains `DESKYARD_ACTIVITY=off` and `FOO=1` and no `DESKYARD_ACTIVITY=on` (extract this merge into `func execEnvFor(agentEnv map[string]string) []string` in `agent.go`).
- `supervisor/tests/test_activity.py` (helpers `make_token` and `exec_ws` imported from `test_agent`; `remote`/`cli` fixtures from `test_cli`):
  - `test_outside_exec_click_type(admin, desktop, name)`: token `"deploy-bot"`; open `/screen-events` in a thread first; `exec_ws(["sh","-c","exit 3"])`, `exec_ws(["deskyard","click","100","120"])`, `exec_ws(["deskyard","type","hunter2-typed"])`. Then `admin.desktop(name)["agents"]` is one entry `{"id": t["id"], "name": "deploy-bot", "owner": "admin", "inside": False}`; the activity summary's `recent` has `{"kind":"command","text":"sh -c 'exit 3'","exit":3}`, `{"kind":"screen","action":"click","count":1}` and `{"kind":"screen","action":"type"}`; `now == []`; the stream delivered `action == "click"` with `pos == [100, 120]` and a `type` event; `"hunter2-typed"` appears in none of: the activity JSON, the desktop JSON, the stream text.
  - `test_token_on_command_line_is_masked(admin, desktop, name)`: `exec_ws(["echo","--token","abc123"])` → recent has `"echo --token •••"`; `"abc123"` isn't in the activity JSON.
  - `test_disconnected_command(admin, desktop, name)`: start `exec_ws(["sleep","30"])` as a task, see it under `now` (`"sleep 30"`), cancel the task → within 5 s recent has `{"text":"sleep 30","disconnected":true}`.
  - `test_agent_cannot_turn_inside_recording_on(admin, desktop, name)`: `exec_ws(["sh","-c","echo $DESKYARD_ACTIVITY"])` with env `{"DESKYARD_ACTIVITY":"on"}` prints `off`. (Extend `exec_ws` with an optional `env` argument.)
  - `test_browser_presence(admin, desktop, name)`: `GET /api/agent/v1/cdp/json/version` with the token → `now` has `{"kind":"browser"}`.
  - `test_cp_recorded(cli, remote, admin, name, tmp_path)`: `deskyard cp` of a 1,000-byte file to `NAME:/workspace/f.bin` → recent has `{"kind":"copy","direction":"in","path":"/workspace/f.bin","size":1000}`; copying it back → `"direction":"out"`.
  - `test_forward_in_now(cli, remote, forwards, admin, name)`: with `forward -R 5173` running, the token's summary `now` has `{"kind":"forward","text":"5173 out"}`.
  - `test_revoke_removes_presence(admin, desktop, name)`: after an exec the agent is listed; `DELETE /api/desktops/{name}/tokens/{id}` → `agents == []` on the next GET.
  - `test_presence_expires(admin, desktop, name)`: (presence is 12 s in tests) after one `exec_ws(["true"])` the agent is listed; it is still listed at 8 s and gone by 20 s (poll every second).

- [ ] **Step 2: Run them to see them fail**

Run: `cd supervisor && go test ./internal/cli/transfer/ ./internal/server/ -run 'Classify|ExecEnv'`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `agentExec`, after `Eng.Exec` succeeds (never before; an exec that didn't start records nothing):
- `ParseScreenArgv(argv)` ok → `s.activity.Screen(ref, agent, act)`; nothing else is recorded for it.
- `transfer.Classify(argv)` ok → count the bytes relayed (stdin for `in`, stdout for `out`); on exit 0, `Copy(ref, agent, dir, path, n)`.
- otherwise `CmdStart(ref, agent, randomID(8), DisplayCommand(argv), cwd)`, and `CmdEnd` with the exit code, or `nil` when the WebSocket or context ended first.
The `agent exec` log line logs `DisplayCommand(argv)` instead of the raw argv. In `agentCDP`: `/devtools/…` → `defer s.activity.BrowserOpen(ref, agent)()`; anything else → `BrowserTouch`. In `agentForward`, after `tryAdd` → `defer s.activity.ForwardOpen(ref, agent)()`. `tokenStore` calls `onGone` after releasing its lock.

- [ ] **Step 4: Run them to see them pass**

Run: `cd supervisor && go test -race ./... && cd tests && uv run pytest test_activity.py test_agent.py test_cli.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add supervisor/internal/server supervisor/internal/cli/transfer supervisor/tests supervisor/API.md
git commit -m "supervisor: record outside agents' commands, screen actions, copies and browser use"
```

---

### Task 7: Reading the inside stream

**Files:**
- Create: `supervisor/internal/server/activity_inside.go` — the reader per running desktop.
- Modify: `supervisor/internal/server/server.go` — the `inside *insideReader` field in the struct, and in `Run`: `s.inside = newInsideReader(s)` before the goroutines, then `go s.inside.run(ctx)`. `New` stays untouched (Task 6 edits it).
- Modify: `supervisor/internal/server/events.go` — `s.inside.wake()` next to `s.stats.wake()` in `handleEvents` and `watchEngine`.
- Create: `docs/Architecture/Agent activity.md` — component page (`status: built`, `partOf: "[[Architecture/Supervisor]]"`, `uses: [[Architecture/Agent API]], [[Architecture/Activity stream]], [[Architecture/Web UI]]`); add `[[Architecture/Agent activity]]` to `uses` of `docs/Architecture/Web UI.md`.
- Test: `supervisor/internal/server/activity_inside_test.go`, `supervisor/tests/test_activity.py`

**Interfaces:**
- Consumes: Task 3 (the `/_activity/events` protocol), Task 4 (the in-desktop CLI writes), Task 5 (`s.activity`, `refOf`), `s.Desktops.Target`.
- Produces (unit-tested without a desktop):
  ```go
  type insideCursor struct{ Epoch string; Seq uint64 }
  func readInside(ctx context.Context, body io.Reader, ref activity.Ref, tr *activity.Tracker, cur *insideCursor) error // the line loop
  func consumeInside(ctx context.Context, resp *http.Response, ref activity.Ref, tr *activity.Tracker, cur *insideCursor) error // epoch check, then readInside
  ```

- [ ] **Step 1: Write the failing tests**

- `activity_inside_test.go`:
  - `TestReadInsideFeedsTracker`: a body of three NDJSON lines (screen click by `"a terminal"`, `cmd-start` id `x`, `cmd-end` id `x` exit 0) → tracker has `inside:a terminal` with one click and a finished command; `cur.Seq` is the last seq.
  - `TestReadInsideSkipsBadLines`: a 20 KiB line, `not json`, and an empty keep-alive line are skipped; the next good line is read.
  - `TestReadInsideSkipsOldSeq`: lines with `seq <= cur.Seq` are ignored (duplicates after reconnect).
  - `TestEpochChangeResetsInside`: with `cur.Epoch "a"` and a running inside command, a response with `X-Activity-Epoch: b` (exercise through the function that handles the response, `consumeInside(resp, …)`) calls `ResetInside` and sets `cur = {b, 0}` before reading lines.
- `supervisor/tests/test_activity.py` (each keeps an `/api/events` stream open in a thread, so the reader runs; helper `container_of(name)` = `docker ps --filter label=deskyard.name=<name> --format {{.Names}}`):
  - `test_inside_click_is_a_terminal(admin, desktop, name)`: `docker exec -t -u user <container> bash -ic 'deskyard click 10 10; true'` → within 15 s `agents` has `{"id": "inside:a terminal", "name": "a terminal", "inside": True, "owner": ""}` and its summary's recent has a click.
  - `test_nested_deskyard_not_counted_inside(admin, desktop, name)`: `exec_ws(["sh","-c","deskyard click 5 5"])` with a token → after 5 s, the only agent is the token's, with a command `sh -c 'deskyard click 5 5'` and no screen item.
  - `test_housekeeping_not_counted(admin, desktop, name)`: `GET /api/desktops/{name}/screen.png`, `POST /api/desktops/{name}/keys {"keys":"Escape"}`, `GET /api/agent/v1` with a token (runs `deskyard size`) → after 5 s, `agents` has no `inside:` entry.
  - `test_inside_backfill(admin, desktop, name)`: with no UI page open (no events stream), run the inside click; then open an events stream → the inside agent appears (backfilled from the ring).

- [ ] **Step 2: Run them to see them fail**

Run: `cd supervisor && go test ./internal/server/ -run 'ReadInside|EpochChange'`
Expected: FAIL.

- [ ] **Step 3: Implement `activity_inside.go`**

Same rule as stats: follow every running desktop while `s.events.count() > 0` and for `statsLinger` after (reuse the constant); reconcile every 10 s and on `wake()`. Per desktop: `GET http://<addr>/_activity/events?epoch=E&after=N` with `X-Deskyard-Token` (from `s.Desktops.Target`), no redirects, a `bufio.Reader` with lines over 8 KiB discarded; every event goes to `s.activity.Inside(ref, e)`. On 404, retry after 60 s (older image); on other errors back off 1 s doubling to 30 s. When following stops because the desktop stopped (not because the UI closed), call `ResetInside(ref)`. Cursors are kept per desktop name in memory, so a reconnect resumes.

- [ ] **Step 4: Run them to see them pass**

Run: `cd supervisor && go test -race ./internal/server/ && cd tests && uv run pytest test_activity.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add supervisor/internal/server supervisor/tests/test_activity.py "docs/Architecture/Agent activity.md" "docs/Architecture/Web UI.md"
git commit -m "supervisor: read each desktop's activity stream while someone looks"
```

---

### Task 8: Presence and summary UI

**Files:**
- Modify: `supervisor/web/src/api.ts` — types; `api.activity(name)`.
- Modify: `supervisor/web/src/live.tsx` — `agents` from `viewers` events; `onActivity(listener)`.
- Create: `supervisor/web/src/lib/activity-format.ts` (+ `activity-format.test.ts`) — every user-visible string.
- Create: `supervisor/web/src/components/agent-avatar.tsx` — `AgentAvatar`.
- Create: `supervisor/web/src/components/agent-summary.tsx` — `AgentSummary` and the `useActivity(name)` hook.
- Modify: `supervisor/web/src/components/viewers-popover.tsx` — an "Agents" section.
- Modify: `supervisor/web/src/desktop/desktop.tsx` — agent avatars after people's in the bar's toggle; the toggle shows when there are agents too.
- Modify: `supervisor/web/src/components/screen.tsx` (wall tile badge), `supervisor/web/src/pages/details.tsx` (viewing card), `supervisor/web/src/components/avatar.tsx` (`Avatars` accepts trailing agents).
- Modify: `supervisor/web/src/index.css` — the pulsing dashed ring keyframes.
- Modify: `supervisor/web/src/dev/mock-data.ts`, `supervisor/web/src/dev/mock.ts` — sample agents (one outside, one inside) and the activity route.
- Modify: `supervisor/web/package.json` — `"test": "node --test src/**/*.test.ts"`; `supervisor/Makefile` — `typecheck` also runs `npm test`.
- Test: `supervisor/web/src/lib/activity-format.test.ts`, `supervisor/tests/test_ui.py`

**Interfaces:**
- Consumes: the JSON shapes of Task 5 (fixed in its Interfaces block); none of the Go code.
- Produces:
  ```ts
  export interface AgentPresence { id: string; name: string; owner: string; inside: boolean; color: string; active: boolean; lastActive: string }
  export interface ActivityNow { kind: "command" | "browser" | "forward"; text?: string; since: string }
  export interface ActivityRecent { kind: "command" | "screen" | "browser" | "copy"; t: string; text?: string; exit?: number; disconnected?: boolean; durationMs?: number; action?: string; count?: number; direction?: "in" | "out"; path?: string; size?: number }
  export interface AgentSummary extends AgentPresence { now: ActivityNow[]; recent: ActivityRecent[] }
  export interface ActivityEvent { name: string; agents: AgentSummary[] }
  // Desktop gains `agents: AgentPresence[]`; ViewersEvent gains `agents: AgentPresence[]`.
  export function agentLabel(a: AgentPresence): string   // overlay label: name, or "Claude Code (in this desktop)"
  export function agentTitle(a: AgentPresence): string   // "deploy-bot · zef's agent" | "Claude Code · in this desktop"
  export function nowLine(n: ActivityNow, now?: number): string
  export function recentLine(r: ActivityRecent): string
  ```

- [ ] **Step 1: Write the failing tests**

`activity-format.test.ts` (`node:test`, `node:assert/strict`), exact strings:
```ts
agentTitle({ name: "deploy-bot", owner: "zef", inside: false, … })      === "deploy-bot · zef's agent"
agentTitle({ name: "Claude Code", owner: "", inside: true, … })         === "Claude Code · in this desktop"
agentLabel(inside Claude Code)                                          === "Claude Code (in this desktop)"
nowLine({ kind: "command", text: "npm test", since: t0 }, t0 + 125_000) === "npm test · 2 min"
nowLine({ kind: "browser", since: t0 })                                 === "using the browser"
nowLine({ kind: "forward", text: "3000 in · 5173 out", since: t0 })     === "ports 3000 in · 5173 out"
recentLine({ kind: "screen", action: "click", count: 14 })              === "clicked 14 times"
recentLine({ kind: "screen", action: "click", count: 1 })               === "clicked"
recentLine({ kind: "screen", action: "type", count: 3 })                === "typed 3 times"
recentLine({ kind: "screen", action: "screenshot", count: 3 })          === "took 3 screenshots"
recentLine({ kind: "screen", action: "screenshot", count: 1 })          === "took a screenshot"
recentLine({ kind: "command", text: "make", exit: 0, durationMs: 12_000 }) === "make · exit 0 · 12 s"
recentLine({ kind: "command", text: "sleep 30", disconnected: true, durationMs: 4_000 }) === "sleep 30 · disconnected · 4 s"
recentLine({ kind: "browser", durationMs: 240_000 })                    === "used the browser · 4 min"
recentLine({ kind: "copy", direction: "in", path: "/workspace/a.pdf", size: 1_200_000 }) === "copied a.pdf in · 1.2 MB"
```
Other screen verbs: double-click "double-clicked", triple-click "triple-clicked", drag "dragged", scroll "scrolled", key "pressed keys", hold-key "held a key", move "moved the mouse", mouse-down "pressed the mouse button", mouse-up "released the mouse button", zoom "zoomed in" / "zoomed in 3 times". Durations: under a minute "N s", under an hour "N min", otherwise "N h M min" (extend `lib/format.ts` `duration` only if its output matches; otherwise add `shortDuration` there). Sizes use `lib/format.ts` `bytes`.

`supervisor/tests/test_ui.py`, `test_agent_presence_and_summary(cli, remote, supervisor, name)`: `remote("-d", name, "click", "100", "100")` and `remote("-d", name, "exec", "--", "true")`; open `/{name}/`; the button "Viewers and port mappings" contains an element with `data-agent` (the agent avatar, `title` "cli test · admin's agent"); click it; the popover shows the heading "Agents" and "cli test · admin's agent"; hovering (desktop) or clicking the row reveals "Recently", "clicked" and "true · exit 0"; on `/desktops/{name}` the viewing card shows the agent avatar; the wall tile at `/` shows it too.

- [ ] **Step 2: Run them to see them fail**

Run: `cd supervisor/web && npm test`
Expected: FAIL (module not found).

- [ ] **Step 3: Implement**

- `AgentAvatar({ agent, ring, size })`: outside → the owner's initial on `agent.color` with a small robot badge (🤖 at 45% size, bottom-right); inside → a robot glyph on `agent.color`; a dashed 2 px ring in `agent.color`, pulsing (`animate-[agent-pulse_1.6s_ease-in-out_infinite]`) while `active`; `data-agent={agent.id}`, `title={agentTitle(agent)}`.
- `Avatars` gets `agents?: AgentPresence[]` rendered after people with the same overlap; `max` counts both.
- `useActivity(name)`: loads `api.activity(name)` when first used, then follows `onActivity` events for that desktop.
- `ViewersPopover` gets `agents: AgentPresence[]` and `name: string`; below "Viewing", an "Agents" heading and one row per agent (avatar + `agentTitle`). Each row is a `button` with `aria-expanded`; on devices with hover (`matchMedia("(hover: hover)")`) mouseenter expands it, and click/tap toggles it. Expanded, it shows `AgentSummary`: "Now" lines (or "Nothing running"), "Recently" lines (up to 20, newest first, or "Nothing in the last 15 minutes"). Port mappings stay listed as today.
- `desktop.tsx`: the toggle shows when `viewers`, `forwards` or `agents` is non-empty; agent avatars follow people's inside the button; its `title` adds the agent titles.

- [ ] **Step 4: Run them to see them pass**

Run: `cd supervisor && make typecheck && make web && cd tests && uv run pytest test_ui.py -k "agent or forwards" -v`
Expected: PASS (the Playwright test needs Tasks 5 and 6 merged).

- [ ] **Step 5: Commit**

```bash
git add supervisor/web supervisor/Makefile supervisor/tests/test_ui.py
git commit -m "web: agents next to the people viewing a desktop, with a summary each"
```

---

### Task 9: Screen overlay

Written against `screenGeometry()`; the KasmVNC implementation is the only streamer-specific code and is swapped by the Selkies work.

**Files:**
- Create: `supervisor/web/src/desktop/screen-geometry.ts` (+ `screen-geometry.test.ts`) — the interface, `toViewport`, and the KasmVNC implementation.
- Create: `supervisor/web/src/desktop/keycap.ts` (+ `keycap.test.ts`) — key caps.
- Create: `supervisor/web/src/desktop/overlay.tsx` — `ActivityOverlay`.
- Modify: `supervisor/web/src/desktop/desktop.tsx` — mount the overlay while running.
- Modify: `supervisor/web/src/index.css` — ripple, ring, fade keyframes.
- Modify: `docs/superpowers/specs/2026-10-22-agent-activity-design.md` (`status: implemented`) and this plan (`status: done`) when everything has landed.
- Test: the two `.test.ts` files, `supervisor/tests/test_ui.py`

**Interfaces:**
- Consumes: Task 5 (`/screen-events`, `ScreenEvent` JSON), Task 8 (`agentLabel`).
- Produces:
  ```ts
  export interface ScreenGeometry { remoteWidth: number; remoteHeight: number; displayed: DOMRect } // displayed: top-level viewport coordinates
  export type ScreenGeometrySource = () => ScreenGeometry | null;
  export function toViewport(g: ScreenGeometry, x: number, y: number): { left: number; top: number } | null // null outside the remote screen
  export function kasmScreenGeometry(frame: HTMLIFrameElement | null): ScreenGeometry | null
  export function keyCap(combo: string): string
  export function ActivityOverlay(props: { name: string; geometry: ScreenGeometrySource }): JSX.Element
  ```

- [ ] **Step 1: Write the failing tests**

- `screen-geometry.test.ts` (Node has no `DOMRect`: build plain `{left, top, width, height}` objects and cast them; `toViewport` reads only those four fields):
  - remote 1920×1080 shown at `{left: 0, top: 46, width: 1920, height: 1080}` (remote resize): `toViewport(g, 100, 200)` → `{left: 100, top: 246}`.
  - remote 1920×1080 scaled into `{left: 40, top: 46, width: 960, height: 540}` (Lock size): `(100, 200)` → `{left: 90, top: 146}`.
  - `(1920, 10)` and `(-1, 10)` → `null`.
- `keycap.test.ts`: `keyCap("ctrl+l") === "⌃L"`, `"ctrl+shift+t" → "⌃⇧T"`, `"alt+Tab" → "⌥⇥"`, `"super" → "❖"`, `"Return" → "↵"`, `"Escape" → "Esc"`, `"BackSpace" → "⌫"`, `"Delete" → "⌦"`, `"Up" → "↑"`, `"F5" → "F5"`, `"a" → "A"`.
- `test_ui.py`, `test_overlay_marks(cli, remote, supervisor, name)`: open `/{name}/`, wait for connected; `remote("-d", name, "click", "200", "150")` → a `[data-mark="click"]` appears within 5 s containing the text "cli test"; its centre is within 3 px of `toViewport` computed in the page from the frame's canvas (`page.evaluate`); `remote("-d", name, "type", "secret-text")` → `[data-mark="typing"]` with text "cli test is typing…", gone within 3 s; `remote("-d", name, "key", "ctrl+l")` → `[data-mark="key"]` text "⌃L"; `remote("-d", name, "move", "10", "10")` → no new mark; `"secret-text"` isn't in `page.content()`. (Marks never reach the agent's screenshots by construction: they live in the top-level page, outside the frame and the desktop.)

- [ ] **Step 2: Run them to see them fail**

Run: `cd supervisor/web && npm test`
Expected: FAIL (modules not found).

- [ ] **Step 3: Implement**

- `kasmScreenGeometry`: the screen canvas `frame.contentDocument?.querySelector<HTMLCanvasElement>('canvas[tabindex="-1"]')` (same origin; the client's files are not changed); `null` without it or when `canvas.width === 0`; `remoteWidth/Height` = `canvas.width/height`; `displayed` = the canvas's `getBoundingClientRect()` offset by the frame's (plus `clientLeft/Top`). Note in a comment that Selkies provides its own `ScreenGeometrySource`.
- `ActivityOverlay`: `position: fixed; inset: 0; pointer-events: none; z-index: 30` (below the bar's popovers at 40); one `EventSource` on `/api/desktops/${name}/screen-events`, closed on unmount; each event placed with `toViewport(geometry(), …)` at draw time and dropped when `null`. Marks per the Global Constraints, in `agent.color`, labelled with `agentLabel`: click ripple (600 ms) + label (fades after 1.5 s); double/triple: two/three staggered rings; drag: an SVG line start → end (1 s); scroll: an arrow pointing in `dir` (800 ms); key and hold-key: a cap with `keyCap` of each key joined by spaces near the agent's last click point (1 s); type: one tag per agent "<label> is typing…" near the last click point or centred on the bottom edge of `displayed`, removed 1.5 s after the agent's last type event. Last click points are kept per agent ID. Every mark element carries `data-mark` (`click`, `double-click`, `triple-click`, `drag`, `scroll`, `key`, `typing`).
- `desktop.tsx`: `{running && <ActivityOverlay name={c.name} geometry={() => kasmScreenGeometry(frame.current)} />}`.

- [ ] **Step 4: Run them to see them pass**

Run: `cd supervisor && make typecheck && make web && cd tests && uv run pytest test_ui.py -k overlay -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add supervisor/web supervisor/tests/test_ui.py docs/superpowers/specs/2026-10-22-agent-activity-design.md docs/superpowers/plans/2026-10-22-agent-activity.md
git commit -m "web: draw agents' clicks and typing over the desktop"
```
