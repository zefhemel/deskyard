---
displayName: Desktop VPN plan
tags: plan
status: active
date: "2026-10-23"
spec: "[[superpowers/specs/2026-10-23-desktop-vpn-design|Desktop VPN design]]"
---
> **For agentic workers:** Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task by task. Check off verified steps only.

**Goal:** Provide administrator-managed per-desktop outbound NordVPN, with failure blocking, configurable LAN exceptions and visible status.

**Architecture:** Each enabled desktop shares a separate managed Gluetun runtime's network namespace. A VPN package owns secret-safe saved connections, policy and runtime readiness; desktop lifecycle owns ordered attachment and durable transitions. Existing agent forwarding is unchanged.

**Tech stack:** Go, Docker Engine SDK, pinned Gluetun, OpenVPN/NordVPN, Preact/TypeScript, existing jobs/events and Python/Docker integration fixtures.

**Spec:** [[superpowers/specs/2026-10-23-desktop-vpn-design|Desktop VPN design]].

# Global constraints

- NordVPN is the first provider; service credentials and country selection are required. Public APIs never return credentials.
- VPN covers desktop outbound internet, not access to Deskyard. Never silently restore direct internet after VPN failure.
- Home/LAN/host/other-desktop access is denied by default; only admins configure destination/protocol/port exceptions. Preserve stateful replies to supervisor-initiated Caddy requests.
- Keep agent port forwarding unchanged. Deliberate application tunnels are outside the enforcement scope.
- Each desktop has a separate runtime/network namespace. A saved connection can be reused but namespaces cannot.
- Desktop restarts for network configuration changes are accepted. Ordinary tunnel recovery leaves desktop applications running.
- Linux deployment and OrbStack/macOS tests; locally supplied provider credentials only, never source or chat.
- Work only in `/Users/zef/.codex/worktrees/desktop-vpn/container-computer` on `codex/desktop-vpn`; another agent owns the original checkout.
- Tests first for behavior, run and observe failures, then implement; no source-text tests.
- Read the current Selkies implementation rather than assuming the older KasmVNC image contract.

# File boundaries

- `supervisor/internal/vpn/{connections,policy,runtime,status}.go`: metadata/secrets, typed LAN rules, runtime construction/control, public health snapshots; split further when responsibilities diverge.
- `supervisor/internal/engine/engine.go`, `engine/docker/`: namespace sharing, capabilities, private port publication, health and restart policy.
- `supervisor/internal/desktops/networking*.go`: durable intent, ordered pair lifecycle and proxy targets; integrate from `service.go` without embedding provider configuration there.
- `supervisor/internal/server/vpn*.go`: administrator CRUD/tests, rollout jobs and public state. Extend existing desktop JSON/settings and events.
- `supervisor/web/src/pages/vpn.tsx`, shared VPN fields, desktop create/settings/status and navigation: guided administration.
- `supervisor/internal/vpn/*_test.go`, desktop/server tests and an isolated Docker fixture: behavioral checks and runtime failure evidence.
- `README.md`, `supervisor/API.md`, relevant architecture pages, this plan and the spec: final behavior and practical limits.

# Task 1: Secret-safe connections, exception policy and engine primitives

**Files:** create `supervisor/internal/vpn/connections.go`, `policy.go` and matching tests; modify `supervisor/internal/engine/engine.go`, `engine/docker/docker.go`, add focused adapter tests.

**Interfaces:** `vpn.NewStore(stateDir string) (*Store, error)`; public `Connection` (id, name, provider, country, revision, credentialsConfigured), write-only `Input` (name, provider, country, optional username/password), `Store.List`, `Get`, `Create`, `Update`, `Delete`; internal secret lookup never marshals credentials. `vpn.Exception` has destination, protocol and ports; `NormalizeExceptions` validates and canonicalizes. Engine `ContainerSpec.NetworkContainer` shares an existing namespace, `CapAdd` supplies explicit capabilities, container inspection reports health; `UpdateRestartPolicy(ctx,id,policy)` changes Docker restart policy. Update engine fakes to retain existing tests.

- [x] Write failing tests covering credential absence in serialized reads, secret file mode/atomic edits/reload, retention of omitted credentials and case-insensitive duplicate names. Include rejected empty credentials on creation and unsupported providers/countries.

```go
public, err := json.Marshal(saved)
if err != nil { t.Fatal(err) }
if bytes.Contains(public, []byte("test-password-never-public")) { t.Fatal("credentials leaked") }
```

- [x] Run `go test ./internal/vpn ./internal/engine/...` and record the expected missing-feature failures.
- [x] Implement immutable generated IDs, owner-only secret storage under `.deskyard/vpn/`, revision increments, secret-free public DTOs, typed errors and canonical exception validation. Reject catch-all/public/subnet exceptions that would undermine local policy and reserved metadata/control destinations.
- [x] Test Docker create requests against an HTTP test server: namespace sharing must omit exposed/published ports and network endpoints on the desktop, reject incompatible aliases/network/publication, and leave ordinary creates unchanged. Assert health and restart-policy requests through the real adapter.
- [x] Implement adapter primitives without weakening desktop defaults; run package tests and `go test ./...`.
- [x] Commit the deliverable and report tests and interface details for Task 2.

# Task 2: Managed VPN runtime and controlled Docker fixture

**Files:** create `supervisor/internal/vpn/runtime.go`, `firewall.go`, `status.go`, tests and fixture files under `supervisor/internal/vpn/testdata/` or a dedicated integration test helper directory.

**Interfaces:** `vpn.NewManager(eng engine.Engine, store *Store, stateDir string) *Manager`; `Manager.Ensure(ctx, desktopID, name, network, connectionID string, exceptions []Exception, publishLocal bool) (engine.Container,error)` returns the ready managed runtime. `Manager.Remove(ctx,desktopID)` removes only owned resources. `Manager.Status(ctx,desktopID)` returns sanitized `Status`; `Manager.Test(ctx,Input)` runs disposable candidate validation, and `Manager.TestSaved(ctx,connectionID,Input)` retains omitted credentials when testing an edit. Runtime names/labels use stable desktop ID and an explicit VPN role. Use a typed runtime configuration builder that can also be exercised with a controlled custom WireGuard fixture without exposing a test provider through user configuration.

- [x] Write failing tests for generated runtime configuration: pinned image, secret-file mounts, only VPN capabilities/device, authenticated control server, port-specific LAN exceptions, blocked Docker DNS and no extra desktop privilege. Tests must validate semantic policy or execute it, not grep script source.
- [x] Run `go test ./internal/vpn` and record red results.
- [x] Implement NordVPN/OpenVPN UDP runtime construction, bounded readiness and sanitized state. Use Gluetun's secret files, managed post-firewall rules and a private authenticated control API. Keep connection bootstrap, local reply traffic and local exceptions explicit; protect control routes despite shared localhost.
- [x] Implement a five-second health observation contract; distinguish connecting, connected, reconnecting and blocked. Do not call an unverified IP current. Bound disposable tests to two minutes and guarantee cancellation cleanup.
- [x] Run a controlled fixture on OrbStack: temporary WireGuard endpoint plus shared-network client, tunnel request, local HTTP access, tunnel loss, recovery, gateway stop, default-denied LAN and a port-specific exception. Generate ephemeral test keys and clean resources by unique labels on all exits.

```python
assert request_from_client(public_target).succeeded
endpoint.stop_tunnel()
assert not request_from_client(public_target).succeeded
assert request_from_host(desktop_http).succeeded
endpoint.start_tunnel()
assert eventually(lambda: request_from_client(public_target).succeeded)
```

- [x] Verify blocked IPv6 and direct Docker DNS paths and separate desktop namespaces. Record exact commands/results; do not claim a local WireGuard test verifies NordVPN/OpenVPN authentication.
- [x] Commit runtime and tests, with any environment limitations documented for the final checks.

# Task 3: Desktop pair lifecycle, administrator API and durable recovery

**Files:** create `supervisor/internal/desktops/networking.go`, persistence/reconciliation helpers and tests; modify `service.go`, server wiring, desktop JSON/settings, events and role-aware engine watchers; create `supervisor/internal/server/vpn.go` and tests.

**Interfaces:** desktop settings gain `VPNConnectionID` and `LANExceptions`. Desktop JSON gains selected public settings plus sanitized `vpn` status. API `GET/POST /api/vpn/connections`, `PUT/DELETE /api/vpn/connections/{id}`, `POST /api/vpn/test`; candidate tests and shared rollouts use existing jobs. Omitted settings retain current values, explicit null disables VPN. Connection edits identify affected desktops; deletion of any referenced connection conflicts. The manager/runtime API from Task 2 remains the provider boundary.

- [x] Write failing lifecycle tests: enable VPN, disable explicitly, no direct fallback on failed enable/change, ordinary reconnect without desktop restart, VPN role excluded from desktop lists/events, correct runtime Caddy target in direct/container supervisor modes, ordered runtime replacement and a persisted interrupted transition surfaced by list/get.
- [x] Write failing API tests: admin-only mutation/list of saved connections, permitted desktop status for regular users, no secrets in job/errors/events, referenced-delete conflict, missing-ID validation, existing direct settings unaffected.
- [x] Run focused package tests and record failures.
- [x] Implement durable per-desktop intent/journal under `.deskyard/vpn/desktops.json`, including enough snapshot information to recover missing containers. Disable independent desktop restart before network transitions. Start the desktop only after policy/control readiness, and never recover an enabled selection using direct networking.
- [x] Integrate create/recreate/start/stop/restart/remove and startup/engine-event reconciliation. Initialize networking before VPN recovery (the current main calls desktop Reconcile before server.New). Keep runtime idle when desktop stopped; remove owned runtime on desktop removal; preserve files/identity. Maintain one per-desktop operation lock and avoid deadlocking existing server jobs.
- [x] Extend administrator routes, tests and jobs using existing authorization/same-origin checks. Poll runtime status centrally, publish changed desktop snapshots and keep host-only details private. Roll out shared revision edits with per-desktop results; never silently drop VPN on partial failure.

```go
// A failed VPN attachment must remain visible and never become direct.
got, err := service.Get(ctx, "alice")
if err != nil { t.Fatal(err) }
if got.Settings.VPNConnectionID == "" { t.Fatal("lost selected VPN") }
if got.VPN.State != "blocked" { t.Fatalf("state = %s", got.VPN.State) }
```

- [x] Run `go test ./...`, focused race tests for new store/operation/status concurrency, and relevant engine lifecycle tests against the current desktop image.
- [x] Commit the integrated backend and report the final public TypeScript/API shapes for Task 4.

# Task 4: Guided administration and desktop controls

**Files:** create `supervisor/web/src/pages/vpn.tsx` and reusable VPN fields/status components; modify `api.ts`, routing, existing navigation, desktop create/settings and status rendering; add tests for data normalization and user-visible status decisions.

**Interfaces:** consume the backend DTOs from Task 3. Add an admin-only VPN connections page at `/_vpn` (the underscore namespace is already reserved, so an existing desktop named `vpn` remains reachable) and desktop settings fields: saved connection selector, LAN exception rows and clear connection state. Use the existing forms, jobs, error text and live events.

- [ ] Write failing behavioral tests for form payloads: blank edit credentials retain existing secrets, explicit no-VPN sends null, unchanged fields remain omitted, exception rows validate ports/addresses, and stale exit IP is not presented as a current connection. Use real pure helpers consumed by the UI rather than source-text assertions.
- [ ] Run `npm test` under `supervisor/web` and observe red results.
- [ ] Implement guided NordVPN add/edit/test, links to service-credential instructions, country selector, affected-desktop confirmation and reference-aware delete errors. Never fill credential inputs from API reads.
- [ ] Add desktop create/settings VPN selection and exceptions, existing Save and restart warning, and visible separate VPN health. Regular users see status but no admin controls. Keep technical enforcement configuration out of ordinary desktop-opening flows.
- [ ] Run `make typecheck` and `make build`. Exercise the UI against a disposable supervisor: create/edit/test connection, choose it for a desktop, read errors, change settings and confirm no-VPN. Use actual browser/UI inspection for the meaningful user paths.
- [ ] Commit the UI deliverable.

# Task 5: End-to-end verification, review and documentation

**Files:** extend integration fixture/lifecycle tests as needed; update `README.md`, `supervisor/API.md`, `docs/Architecture/{VPN connections,VPN runtime,Lifecycle,Desktop proxy,Engine adapter}.md`, the spec, use cases and this plan.

- [ ] Run `make test`, `make typecheck`, `make build`, `make vet` and relevant controlled engine tests. Fix meaningful failures with regression tests; avoid repeatedly running unchanged suites without cause.
- [ ] Exercise a real fresh Selkies desktop through the supervisor on macOS: token-authenticated proxy, screen connection, exec, file access and lifecycle, with two namespaces and the controlled VPN endpoint. Use a scoped engine wrapper in a test supervisor: add a unique fixture ownership label on creates and filter list/network/event reads to that label. Do not run broad production startup reconciliation against the shared engine's existing desktops. Observe direct-egress attempts through startup/loss/replacement/recovery rather than merely checking a connected indicator.
- [ ] Check optional local NordVPN secret-file availability without printing values. If absent, finish all credential-independent work and request local credential entry through the completed UI. Mark provider and native Linux validation unverified until actually run.
- [ ] Obtain spec-compliance and maintainability review of the whole branch, address important findings, and run covering tests after fixes.
- [ ] Document actual setup, resource/connection cost of idle runtimes, failure behavior, LAN exception semantics, forwarding scope and any unverified platforms/provider checks. Mark only built components as built and plan tasks done when verified; retain explicit release limitations.
- [ ] Validate changed docs using local links/metadata checks and SilverBullet against this worktree (do not overwrite or validate the other agent's docs as if they were these changes). Commit completed work and leave the branch ready for review; do not merge into the other agent's checkout.

# Execution notes

The existing local spike uses `/tmp/deskyard-vpn-probe/probe.py` and a real `deskyard-debian-xfce:local` desktop. It is throwaway context, not production code to copy blindly. The current repository has since merged Selkies; use a current `CC_TEST_IMAGE` for full lifecycle tests.

The user approved the VPN design and requested implementation in a worktree. DNS follows this feature's networking foundation and is not part of this plan's implementation scope.
