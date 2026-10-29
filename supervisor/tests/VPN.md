# Scoped VPN verification

Use this workflow for a Docker engine that also holds unrelated desktops. It never starts the production supervisor main or imports the Python full-main conftest. A `vpnintegration` test binary runs the actual HTTP server, embedded UI, VPN Manager and paired desktop lifecycle through an ownership-scoped engine. Only the existing typed WireGuard fixture input is substituted; ordinary builds still generate NordVPN/OpenVPN configuration. The fixture cannot build, retag or remove shared images.

Like the supervisor, the fixture supervisor runs only in a container. `start` cross-compiles the test binary for the engine's Linux architecture (amd64 or arm64, from `make engine-goarch`), builds the desktop CLIs and the `deskyard:base` image (`make cli-linux base`), and starts the binary with `make run` in place of the supervisor's, as `tests/conftest.py` does, with the Docker socket, the private directory's `data/` folder at `/data` and the private directory itself at `/e2e`. As production `main` does, the binary reads the data folder's host path from its own container and translates mount sources at the engine, so gateways and desktops get host paths while the supervisor works with `/data`. The binary runs as root in its container (it is a test, not the supervisor, so it doesn't drop privileges); files it creates in the private directory may need `docker run --rm -v <dir>:/b alpine rm -rf /b/data` to delete on a Linux host.

The commands below were run on the macOS/arm64 OrbStack development host. They require Go, Node, Python, uv, Docker CLI, the current desktop image, the pinned Gluetun image, WireGuard kernel support and internet access for Alpine tools/public HTTPS. The private directory is created under `/private/tmp`, which OrbStack shares with its engine; these results do not establish Linux host support.

Set `CC_TEST_IMAGE` to a recent compatible desktop image to verify current image behavior. If omitted, the helper retains its historical `deskyard-debian-xfce:selkies` default.

From `supervisor/`, after `make build`:

```sh
RUN_DIR=$(python3 tests/vpn_e2e.py prepare fixture)
python3 tests/vpn_e2e.py start "$RUN_DIR"
python3 tests/vpn_e2e_traffic.py traffic "$RUN_DIR"
python3 tests/vpn_e2e.py check "$RUN_DIR"
uv run --project tests python tests/vpn_e2e_traffic.py stream "$RUN_DIR"
python3 tests/vpn_e2e_traffic.py lifecycle "$RUN_DIR"
```

The controlled endpoint starts with its tunnel down, to verify initial blocked internet access and available local Caddy. Each desktop receives a distinct private WireGuard key/address, retained by stable identity across fresh Manager/Service recovery, even though the pair shares a saved connection. The endpoint joins both owned bridges and explicitly retains its original uplink route; Docker may otherwise change that route on network attachment.

`start` prints the local supervisor URL. `admin-password` in the owner-only temporary directory holds the generated fixture login for `admin`; do not copy it or provider credentials into a report. Before `lifecycle`, a person or browser tool can open either desktop and inspect the actual Selkies picture. The test stays at its ready checkpoint for this purpose. `check` independently uses real agent-token exec (UID1000), persistent file write/list/download, screenshots, activity, authenticated Caddy proxy and shared Chromium CDP navigation. `stream` opens actual session/same-origin WebSockets through the supervisor and requires three Selkies picture frames from each desktop. It uses the existing tests environment's `websockets` dependency without loading conftest or starting another supervisor.

`traffic` verifies two successful peers, denied cross-desktop requests, repeated failed HTTPS attempts during tunnel loss, local proxy availability and recovery. Counters at the endpoint distinguish packets entering from either desktop bridge from late public-server replies to the tunnel address. No observation rule accepts or drops traffic: production gateway firewall enforcement must do that. Taking the endpoint's `wg0` down removes its connected tunnel route, so late replies can follow the endpoint's default route. The test requires zero bridge-origin direct packets and full accounting of the broader forwarding counter as those return packets; any unknown packet fails. NFLOG captures that broader rule from preparation onward in `/tmp/forward-observation.txt` inside the owned endpoint, for source/destination diagnosis.

`lifecycle` releases the foreground checks: stop leaves an idle gateway, start/application restart reuse it, gateway stop blocks egress, and a fresh service reconciles the persisted selection into a new namespace/consumer. It checks retained workspace and restored proxy access, then saves `post-lifecycle-forward-counts.json` and repeats the zero bridge-origin/full-accounting assertions before releasing same-image recreation and removal. A missing observation rule or unaccounted packet also fails this phase. The harness detaches its extra test endpoint before production network removal. Background status/events run during the HTTP/traffic phase and are canceled before deliberate dependency-loss recovery so that the test controls which service performs reconciliation. This is a supervisor/service replacement and dependency stop, not an actual host or Docker daemon reboot.

Inspect the final test result before cleanup with `docker logs` for the exact `SelfID` in `fixture.json`. `lifecycle` returning successfully only means its external assertions passed; wait for the Go test's final PASS, including pair/network removal. On an error, preserve the first failure, safe persisted phase/Manager messages and `output-diagnostics.json` before cleanup. No readiness assertions are relaxed for a retry.

```sh
python3 tests/vpn_e2e.py cleanup "$RUN_DIR"
```

Cleanup first stops the fixture supervisor so background recovery cannot recreate deleted dependencies. It locates the exact scope-labeled supervisor, verifies its ID/label, stops that ID and verifies it is no longer running before removing any gateway or desktop. Cleanup then checks the exact random ownership label again and removes full resource IDs, including stopped containers, and asserts empty owned container/network inventories. Run it even after a failed phase. It preserves the private directory and evidence for investigation; remove those disposable files after collecting the report. Never use an unfiltered engine cleanup or delete another fixture's scope. Production-Nord previews are separate scopes and must remain while a person is entering credentials.

The lower-level controlled firewall/DNS suite remains:

```sh
DESKYARD_VPN_ENGINE_TEST=1 go test ./internal/vpn -run TestControlledWireGuard -count=1 -v
```

It checks public DNS through the tunnel, blocked Docker DNS and IPv6, LAN destination/port limits, authenticated control reads/mutations, ordered policy tampering and isolated namespaces. See `internal/vpn/testdata/README.md`.

Focused helper regressions exercise failed-phase shutdown ordering, ownership refusal, the fixture container's mounts and lifecycle accounting without Docker or the production conftest:

```sh
python3 -m unittest discover -s tests -p 'vpn_e2e_helpers_test.py' -v
```

## Evidence and release limits

The recorded `make build` succeeded with npm's existing warning that `fsevents@2.3.3` has an install script not covered by `allowScripts`. No script was approved to silence that warning; successful compilation does not establish execution of that optional dependency's install script.

The 2026-10-23 OrbStack checks exercised real Selkies, tunneled traffic, supervisor paths outside and inside a container (the former since removed), exec/files/activity/CDP, loss/recovery and paired lifecycle. A Chrome view of the supervisor outside a container visibly rendered the real streamed Chromium desktop. Container-mode visual browser capture was interrupted by a browser extension popup; actual proxied picture frames and the shared browser are checked independently. Controlled WireGuard does not prove NordVPN authentication, country selection, credential updates, provider limits or Linux host support. At that checkpoint, those and a full host/engine reboot remained release checks. See the subsequent live check below.

Earlier lifecycle work recorded isolated unclassified recovery failures, with persisted blocked selection and a successful retry. The confirmed redundant-private-network policy churn defect was separately fixed and regression-tested; a later passing run does not retroactively classify the earlier failures. Initial end-to-end broad forwarding observations of four uncaptured packets remain individually inconclusive. A later reproduced failure captured all 64 matches and proved them to be late public-server replies to tunnel addresses, motivating the precise direction/accounting assertions above; it required no production policy change.

For an optional real NordVPN UI preview, `prepare preview` followed by `start` runs an unregistered production Manager with the scoped engine, no controlled endpoint and an empty connection store. It is bound locally for at most 12 hours. Enter service credentials locally on `/_vpn`; never supply them to source, reports or chat. Touch `stop` in that private directory to stop the server, then clean only that scope when the person has finished. A preview's existence does not count as successful provider testing.

## Live NordVPN and MATE check — 24 October 2026

The user entered service credentials locally in the production preview and created a MATE desktop with the Netherlands connection. Cached status reported connected with a freshly verified public exit IP; a desktop screenshot and three authenticated supervisor-proxied picture frames succeeded, and the user browsed the internet from desktop Chromium. This establishes live authentication and ordinary desktop egress/access on OrbStack, without recording credential values or claiming that the exit country was independently geolocated. Country switching, credential edits, provider limits, Linux hosts and host/engine reboot remain unverified.

The user initially reported a missing picture, then wallpaper without the MATE taskbar. The panel process and window existed at the bottom of the real display. During diagnosis, Chrome's video dimensions were 1446×672 while X reported 1280×800; an additional diagnostic stream had also been opened, so that mismatch alone does not establish the original cause. Reloading the desktop page restored a visible panel and Chromium without restarting the container or applications. This is a confirmed manual recovery, not a permanent resizing fix. Keep the initial display/stream synchronization problem open for reproduction; do not attribute it to VPN authentication or hide it behind the successful reload.

## Container-only fixture run — 27 October 2026

After the supervisor became container-only, `prepare fixture`, `start`, `traffic`, `check` and `stream` passed on OrbStack with `CC_TEST_IMAGE=deskyard-debian-xfce:test`: both gateways mounted host paths under the private directory's `data/` (and the peer keys at their host paths), and both desktops' homes were `data/home/<name>` on the host. `lifecycle` then failed in the Go test at "replacement identity/namespace": since environments became durable definitions (26 October), a stopped gateway is started again in place rather than replaced, so the test's expectation of a new namespace after a gateway stop is out of date. That assertion and the matching lifecycle phase need updating; it is not a container-only regression. Cleanup removed every owned resource.
