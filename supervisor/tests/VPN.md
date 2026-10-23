# Scoped VPN verification

Use this workflow for a Docker engine that also holds unrelated desktops. It never starts the production supervisor main or imports the Python full-main conftest. A `vpnintegration` test binary runs the actual HTTP server, embedded UI, VPN Manager and paired desktop lifecycle through an ownership-scoped engine. Only the existing typed WireGuard fixture input is substituted; ordinary builds still generate NordVPN/OpenVPN configuration. The fixture cannot build, retag or remove shared images.

The commands below target the macOS/arm64 OrbStack development host. They require Go, Node, Python, uv, Docker CLI, the current `deskyard-debian-xfce:selkies` image, the pinned Gluetun image, WireGuard kernel support and internet access for Alpine tools/public HTTPS. The container mode currently mounts `/Users/zef/.orbstack/run/docker.sock` and builds Linux/arm64. Adapt that socket/architecture explicitly for another host; these results do not establish native Linux support.

From `supervisor/`, after `make build`:

```sh
export DOCKER_HOST=unix:///Users/zef/.orbstack/run/docker.sock
RUN_DIR=$(python3 tests/vpn_e2e.py prepare direct)
python3 tests/vpn_e2e.py start "$RUN_DIR"
python3 tests/vpn_e2e_traffic.py traffic "$RUN_DIR"
python3 tests/vpn_e2e.py check "$RUN_DIR"
uv run --project tests python tests/vpn_e2e_traffic.py stream "$RUN_DIR"
python3 tests/vpn_e2e_traffic.py lifecycle "$RUN_DIR"
```

Repeat with `prepare container` for an actual containerized supervisor, same-path workspace/state bind and Docker socket bind. This starts the controlled endpoint with its tunnel down to verify initial blocked internet access and available local Caddy. A successful direct preparation starts with the tunnel up. Each desktop receives a distinct private WireGuard key/address, retained by stable identity across fresh Manager/Service recovery, even though the pair shares a saved connection. The endpoint joins both owned bridges and explicitly retains its original uplink route; Docker may otherwise change that route on network attachment.

`start` prints the local supervisor URL. `admin-password` in the owner-only temporary directory holds the generated fixture login for `admin`; do not copy it or provider credentials into a report. Before `lifecycle`, a person or browser tool can open either desktop and inspect the actual Selkies picture. The test stays at its ready checkpoint for this purpose. `check` independently uses real agent-token exec (UID1000), persistent file write/list/download, screenshots, activity, authenticated Caddy proxy and shared Chromium CDP navigation. `stream` opens actual session/same-origin WebSockets through the supervisor and requires three Selkies picture frames from each desktop. It uses the existing tests environment's `websockets` dependency without loading conftest or starting another supervisor.

`traffic` verifies two successful peers, denied cross-desktop requests, repeated failed HTTPS attempts during tunnel loss, local proxy availability and recovery. Counters at the endpoint distinguish packets entering from either desktop bridge from late public-server replies to the tunnel address. No observation rule accepts or drops traffic: production gateway firewall enforcement must do that. Taking the endpoint's `wg0` down removes its connected tunnel route, so late replies can follow the endpoint's default route. The test requires zero bridge-origin direct packets and full accounting of the broader forwarding counter as those return packets; any unknown packet fails. NFLOG captures that broader rule from preparation onward in `/tmp/forward-observation.txt` inside the owned endpoint, for source/destination diagnosis.

`lifecycle` releases the foreground checks: stop leaves an idle gateway, start/application restart reuse it, gateway stop blocks egress, and a fresh service reconciles the persisted selection into a new namespace/consumer. It checks retained workspace and restored proxy access, then saves `post-lifecycle-forward-counts.json` and repeats the zero bridge-origin/full-accounting assertions before releasing same-image recreation and removal. A missing observation rule or unaccounted packet also fails this phase. The harness detaches its extra test endpoint before production network removal. Background status/events run during the HTTP/traffic phase and are canceled before deliberate dependency-loss recovery so that the test controls which service performs reconciliation. This is a supervisor/service replacement and dependency stop, not an actual host or Docker daemon reboot.

Inspect the final test result before cleanup. Direct mode writes `supervisor.log`; container mode uses `docker logs` for the exact `SelfID` in `fixture.json`. `lifecycle` returning successfully only means its external assertions passed; wait for the Go test's final PASS, including pair/network removal. On an error, preserve the first failure, safe persisted phase/Manager messages and `output-diagnostics.json` before cleanup. No readiness assertions are relaxed for a retry.

```sh
python3 tests/vpn_e2e.py cleanup "$RUN_DIR"
```

Cleanup first stops the fixture supervisor so background recovery cannot recreate deleted dependencies. Native startup saves its UID, command and start time in `native-process.json`; cleanup checks that identity before signaling the PID and while waiting (TERM for up to 10 seconds, then KILL for up to 5 seconds). A mismatched/reused PID, missing identity for a live process or shutdown deadline aborts resource deletion. Older running fixtures without that record must be stopped through their original coordination/process owner first; cleanup can proceed once the recorded PID has exited. Container cleanup locates the exact scope-labeled supervisor, verifies its ID/label, stops that ID and verifies it is no longer running before removing any gateway or desktop. Cleanup then checks the exact random ownership label again and removes full resource IDs, including stopped containers, and asserts empty owned container/network inventories. Run it even after a failed phase. It preserves the private directory and evidence for investigation; remove those disposable files after collecting the report. Never use an unfiltered engine cleanup or delete another fixture's scope. Production-Nord previews are separate scopes and must remain while a person is entering credentials.

The lower-level controlled firewall/DNS suite remains:

```sh
DESKYARD_VPN_ENGINE_TEST=1 go test ./internal/vpn -run TestControlledWireGuard -count=1 -v
```

It checks public DNS through the tunnel, blocked Docker DNS and IPv6, LAN destination/port limits, authenticated control reads/mutations, ordered policy tampering and isolated namespaces. See `internal/vpn/testdata/README.md`.

Focused helper regressions exercise failed-phase shutdown ordering, process identity refusal/deadlines and lifecycle accounting without Docker or the production conftest:

```sh
python3 -m unittest discover -s tests -p 'vpn_e2e_helpers_test.py' -v
```

## Evidence and release limits

The recorded `make build` succeeded with npm's existing warning that `fsevents@2.3.3` has an install script not covered by `allowScripts`. No script was approved to silence that warning; successful compilation does not establish execution of that optional dependency's install script.

The 2026-10-23 OrbStack checks exercised real Selkies, tunneled traffic, direct/container supervisor paths, exec/files/activity/CDP, loss/recovery and paired lifecycle. A direct-mode Chrome view visibly rendered the real streamed Chromium desktop. Container-mode visual browser capture was interrupted by a browser extension popup; actual proxied picture frames and the shared browser are checked independently. Controlled WireGuard does not prove NordVPN authentication, country selection, credential updates, provider limits or native Linux support. Those and a full host/engine reboot remain release checks.

Earlier lifecycle work recorded isolated unclassified recovery failures, with persisted blocked selection and a successful retry. The confirmed redundant-private-network policy churn defect was separately fixed and regression-tested; a later passing run does not retroactively classify the earlier failures. Initial end-to-end broad forwarding observations of four uncaptured packets remain individually inconclusive. A later reproduced failure captured all 64 matches and proved them to be late public-server replies to tunnel addresses, motivating the precise direction/accounting assertions above; it required no production policy change.

For an optional real NordVPN UI preview, `prepare preview` followed by `start` runs an unregistered production Manager with the scoped engine, no controlled endpoint and an empty connection store. It is bound locally for at most 12 hours. Enter service credentials locally on `/_vpn`; never supply them to source, reports or chat. Touch `stop` in that private directory to stop the server, then clean only that scope when the person has finished. A preview's existence does not count as successful provider testing.
