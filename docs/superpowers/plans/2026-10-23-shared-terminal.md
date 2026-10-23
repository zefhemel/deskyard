---
displayName: Shared terminal plan
tags: plan
status: done
date: "2026-10-23"
spec: "[[superpowers/specs/2026-10-23-shared-terminal-design|Shared terminal design]]"
---
> **For agentic workers:** Use the subagent-driven-development skill to execute the approved work continuously. User approval already covers implementation.

**Goal:** Production shared terminal access across browser, mobile, CLI and native desktop.

**Architecture:** Bundled xterm.js connects through an authenticated supervisor WebSocket to a fixed tmux helper in the desktop. Each connection is independently detachable; the shell remains in the container.

**Tech Stack:** Go, Preact/TypeScript, xterm.js, tmux, Debian, Docker, Playwright, XCTest/simctl, Android emulator.

**Spec:** [[superpowers/specs/2026-10-23-shared-terminal-design|Shared terminal design]].

# Global Constraints
- Work only in the existing terminal-prototype worktree on `codex/shared-terminal`; preserve unrelated main-checkout changes.
- No visible Zellij branding, patched vendor assets, separate terminal HTTP service, listener, or browser terminal credentials.
- Default session `main`; names match `[A-Za-z0-9][A-Za-z0-9_-]{0,63}`.
- The owned tmux configuration disables status and uses `window-size latest`; shell jobs survive disconnect, end on container restart.
- Use existing login and View authorization; explicit same-origin WebSocket checks and active revocation/expiry/logout cancellation.
- Raw binary input/output; first text `{cols,rows}`, then `{resize:[cols,rows]}` or `{ack:N}`; server `{ready:true,generation:string}`, `{exit:number}`, `{error:string}`.
- Dimensions: 2–500 columns, 1–300 rows. Input limit 64 KiB; output chunks 16 KiB, outstanding output at most 256 KiB. ACK after renderer consumes output.
- Mobile bar: Ctrl-C, Ctrl-D, Ctrl-L, Esc, Tab, arrows, one-shot Ctrl/Alt. Native iOS simulator test with software keyboard open is mandatory.
- Use tests first for behavior. Run focused checks while iterating, full root `make test` before final commit. Individual tasks remain uncommitted until that gate, with stable diff review packages.

### Task 1: Shared tmux runtime and CLI
**Files:** create `supervisor/internal/terminal/terminal.go`, `terminal_test.go`, `supervisor/internal/cli/app/cmd_terminal.go`; modify CLI `spec.go`, relevant help fixtures/guide; modify `images/Dockerfile`; create common tmux config and desktop launcher under `images/common/rootfs`; add `images/tests/test_terminal.py`. Update `docs/Architecture/Shared terminal.md`, CLI architecture pages and README terminal usage.

**Interfaces:** produce `terminal.ValidSession(string) bool`, `terminal.Command(string) []string` returning fixed `deskyard terminal-helper SESSION`, and a cancellable in-container attach implementation consumed by hidden `terminal-helper SESSION`. The public `deskyard terminal [session]` uses existing remote interactive exec or local terminal helper; no new remote API.

- [x] Write validation and CLI behavior tests first. Include exact-name versus prefix session separation, malformed names and extra arguments; add Docker tests for concurrent creation, disconnect persistence, private socket permissions, latest-client sizing and no status line.
```go
func TestSessionNames(t *testing.T) {
 for _, name := range []string{"main", "build-2", "A_b"} { if !terminal.ValidSession(name) { t.Fatal(name) } }
 for _, name := range []string{"", "-x", "a.b", "a:b", "a b", "a;id"} { if terminal.ValidSession(name) { t.Fatal(name) } }
}
```
- [x] Run focused new tests and record missing behavior failure; implement validation, fixed argv, private runtime socket, race-safe session creation and attach, image tmux/config/launcher. Ensure native and remote interactive calls detach without killing the shell, exact tmux targets, cancellation-safe locking, no user config overwrite. Use Bash login shell in `/workspace`.
- [x] Run Go CLI/package tests and image terminal tests against a newly built image; self-review and save report. Leave changes uncommitted for the full-suite gate.

### Task 2: Authenticated browser terminal transport
**Files:** create `supervisor/internal/server/terminal.go`, `terminal_connections.go`, `terminal_test.go`, `terminal_flow.go` and covering tests; modify `routes.go`, `server.go`, `desktops.go` (image capability boolean `terminal` in desktop JSON), account/power/event cancellation hooks as required; extend `supervisor/internal/auth/session.go` with verified expiry claims and tests; update `supervisor/API.md`, architecture API/Session/Shared terminal pages.

**Interfaces:** consume `terminal.ValidSession` and `terminal.Command`; produce the WebSocket endpoint and exact wire contract in Global Constraints. Register browser terminals in existing viewer presence. Extend session claims through one shared authenticated parser; preserve the public `Read` interface used elsewhere.

- [x] Add failing HTTP/WebSocket tests using existing fake engine test setup: no cookie, wrong Origin, missing Origin, no access, invalid name/size, stopped desktop, fixed argv/env/user/PTY, binary stdin and output, resize, clean shell exit, cancel disconnect, logout, expiry, epoch change/revoke, stop, viewer lifecycle, output credit exhaustion/release and malformed ACK.
```go
// HTTP authentication tests must assert engine Exec was never called.
// Flow test: reserve 256 KiB, prove next byte waits, acknowledge a chunk,
// prove next write proceeds, and prove cancellation unblocks a waiter.
```
- [x] Run new tests red, implement bounded transport and lifecycle with short handshake/write timeouts. Keep browser protocol separate from existing agent exec where flow control differs; share only stable primitives to avoid regressions. Never log input/output; error text must identify old-image upgrade requirement.
- [x] Run focused server/auth tests with race detector, then existing server package; self-review and save report. Leave changes uncommitted.

**Implementation adjustment after real Docker acceptance:** Socket close does not terminate Docker TTY execs. Task 2 now uses `terminal_process.go` with a non-TTY `terminal-lease SESSION ATTACHMENT_ID` and TTY attach-only `terminal-attach ATTACHMENT_ID`; `internal/terminal/lease.go` owns private grouped-session cleanup on EOF, including supervisor death. The exact remote CLI helper request uses this same path, while generic exec is unchanged. Readiness gates early input on tmux registration and first output. Regression coverage lives in `supervisor/tests/test_terminal_transport.py`; original single-helper execution is superseded, with browser frames unchanged.

### Task 3: Owned browser terminal and mobile keyboard bar
**Files:** create `supervisor/web/src/terminal/{terminal.tsx,connection.ts,keys.ts,key-bar.tsx,terminal.css}` plus focused `.test.ts` files; modify app routing, desktop and desktop list/details navigation, `api.ts`/dev mock desktop capability booleans, dependencies/lockfile, server desktop UI path routing; update Web UI architecture and README.

**Interfaces:** consume the endpoint/wire contract above; owned page `/<name>/terminal/?session=main`. Lazy-load the page/xterm assets. Connect only with current login cookie. Use xterm's native onData/onBinary and renderer callback ACK, no alternate text-input stream.

- [x] Write failing key byte and connection tests: Ctrl-C `\x03`, Esc `\x1b`, Tab `\t`, arrows CSI, Ctrl/Alt combinations, modifier reset, no disconnected input replay, ACK after write callback, retry stop on exit/auth, ready generation changes.
```ts
assert.equal(encodeKey("c", {ctrl:true,alt:false}), "\x03");
assert.equal(encodeKey("ArrowUp", {ctrl:false,alt:false}), "\x1b[A");
assert.equal(encodeKey("x", {ctrl:false,alt:true}), "\x1bx");
```
- [x] Run new tests red, install pinned xterm/addon-fit versions, implement page/transport/key bar. Supply 44px accessible controls and one-shot modifiers, keep textarea focused on bar taps, do not autofocus on reconnect; visualViewport + ResizeObserver sizing and safe areas; mobile font at least 16px; readable scrollable terminal when another client owns dimensions. Provide session selection, connection state, desktop link, error/reopen states. Disable automatic clipboard escape handling and unsafe links.
- [x] Run UI tests/typecheck/build, then headless browser integration for concurrent sessions/resize/reconnect/full-screen input/output stress. Self-review and save report. Leave changes uncommitted.

Browser checkpoint: the real headless suite verifies tmux history positions, Tab completion, shell state, application-cursor bytes and restored raw-mode termios. The base image has no vi/vim/nano; Task 4 subsequently installed GNU nano 8.4 only in the QA container and verified actual editor input, save/exit and UTF-8 rendering alongside native software-keyboard acceptance.

### Task 4: Native simulator acceptance, media and release verification
**Files:** add reusable integration acceptance checks under `supervisor/tests/test_terminal_shared.py`; add native mobile test harness and evidence under the design page's adjacent asset directory; update spec/plan statuses and prototype supersession note, README and all affected architecture/API docs.

**Interfaces:** exercise the real built supervisor and updated XFCE image, existing sign-in, owned terminal route and CLI. Use isolated test data/ports/subnet; no unlock required.

- [x] Add/run real end-to-end acceptance proving browser/CLI/native desktop share pane contents and shell PID, disconnect persistence, editor interaction, bounded output with responsive Ctrl-C, shell exit/reopen, reload, viewport resize and revocation.
- [x] Run native iPhone Safari using XCTest/simctl with software keyboard visibly open. Tap key bar Ctrl-C during a foreground command, test Tab completion, Esc, arrows, Ctrl/Alt one-shot state, paste/Unicode and portrait/landscape. Assert fresh unique files or tmux pane state independently of XCTest typing success. Capture clean screenshots and short videos with no credentials shown. Exercise iPad and Android native browsers where available, distinguish any limitations.
- [x] Run root `make test` with unique test image and subnet, supervisor `make build`, documentation link/path/frontmatter checks and SilverBullet CLI outside sandbox. Decode final videos with ffmpeg to ensure playable artifacts.
- [x] Obtain whole-branch code review; resolve material findings, update docs with actual checks/limits, mark completed plan steps, prepare the reviewed feature for the root-owned commit. Report readable results inline with direct media and absolute file links.

### Task 5: Native terminal defaults and running-session discovery

User amendment: ordinary native windows/tabs create independent shared shells and the picker lists all live base sessions.

- [x] Add authenticated View-scoped bounded discovery and helper filtering, with guards/error/parser tests first.
- [x] Add readable atomic native allocation preserving cwd; migrate saved XFCE, KDE and MATE defaults and keep explicit emulator argv behavior.
- [x] Verify native launch paths, tabs, working directories, restored sessions, concurrent allocation and explicit commands across all three images; update native/browser/CLI acceptance to discover the created session.
- [x] Update runtime/API/CLI/image architecture, README and spec for the user amendment.
- [x] Save scoped review diff and verification report.
- [x] Complete review of Task 5 fixes before the root full-suite gate.

### Task 6: Running-session picker and measured short-height fallback
- [x] Add discovery state tests first for fresh/empty/error lists, single-flight requests, cancellation and stale responses; consume the frozen names-only API and update development mocks.
- [x] Replace manual session disclosure with explicit existing-session joins, current indication, refresh/retry and separate validated new-name creation; refresh only while open and visible.
- [x] Derive compact/ultra-short/insufficient layout tiers from a readable row plus 44px controls, preserving the existing terminal/native-input lifetime through rotation.
- [x] Verify real native-created session discovery/picker joining, distinct-shell creation, transient-error recovery, periodic list changes and 68px/36px focus-preserving layout behavior; run UI typecheck/build and existing file/folder download regressions.
- [x] Complete final native picker/fallback/portrait-return acceptance, review and root full-suite gate with Task 4; save Task 6 scoped report/diff before handover.

Supplemental gate correction: give existing download anchors nonempty suggested file/folder ZIP names so preact-iso preserves native download navigation. Existing API Content-Disposition continues to determine the final download name; cover both folder actions and exact file/ZIP contents through the existing browser test.

### Native named-session joining (implemented finishing correction)

Within an existing native shared terminal, press **Ctrl-B then s** and choose the named session; this switches only that native client. The chooser shows public sessions, including browser-created shells, and the native title follows the selected name. Outside tmux, `deskyard terminal NAME` joins as before. Browser and remote CLI attachments keep their connection-owned groups: use browser **Sessions**, or detach and reconnect with `deskyard terminal NAME`, rather than the native chooser. The owned chooser filters private group rows and omits group metadata and initial previews. Advanced explicit tmux commands remain trusted-shell capabilities; this does not change lease ownership. Public base-session title options are applied on every ensure/join, including browser-created named sessions.

# Acceptance record

[[Testing/Shared terminal 2026-10-23|Shared terminal testing — 23 October 2026]] contains the create/join walkthrough, inline screenshots, verification results and explicit limits.

# Final acceptance and limits

Root `make test` exited 0: Go vet/race, 33 UI tests and engine checks passed; image tests passed 98 parallel plus one serial check, with three MATE appearance skips in XFCE; supervisor tests passed 133 parallel plus seven serial checks, with nine Firefox startup skips. `make build` and Windows/Linux amd64 cross-compilation passed. Final native iPhone picker/create/join, software keyboard, landscape fallback and portrait/dismiss/reopen acceptance passed; final Android picker create/join passed through CDP, with earlier native ADB/Gboard key-effect proof. iPad refreshed focus and earlier native key-effect checks passed. Actual nano editor save/exit/UTF-8 and browser/CLI/native shared PID/persistence passed. Whole-feature implementation review approved with its sole P3 supersession note corrected. Root owns the final scoped documentation/evidence rereview and commit.

The paste/Unicode item was exercised but native Unicode paste was not verified because the automation harness failed that operation; headless composition/Unicode and actual nano UTF-8 checks passed independently. Physical devices and amd64 runtime were unavailable. These explicit acceptance limits do not imply passing results for unavailable checks. See the acceptance record for screenshots, playable videos, precise mechanisms and the retained landscape crop qualification.
