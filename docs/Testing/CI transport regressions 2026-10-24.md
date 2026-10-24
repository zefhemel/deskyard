---
displayName: CI transport regressions, 24 October 2026
date: "2026-10-24"
---
The Tests workflow run `40104810507`, at commit `4a89e30`, failed the settings recreation assertion in both supervisor modes. Its native job also timed out starting the first microphone in `test_two_mics`.

# Causes and changes

`test_settings_change_recreates_and_keeps_folder` compared the entire settings object against the older schema. The API now also returns `vpnConnectionId: null`, `lanExceptions: []` and `dockerAccess: false` by default. The assertion includes those fields and still checks exact settings, stable identity, completed recreation and preserved workspace content. The API contract did not change for this fix.

The native job logged `net::ERR_NETWORK_CHANGED` for the microphone badge POST. Linux Docker bridge creation/removal is visible to Chromium while other workers create desktops. The Audio panel deliberately waits for the supervisor to acknowledge its badge report before starting capture; a failed report keeps the mic off. Increasing the wait cannot complete that rejected operation. Production capture and badge behavior remains unchanged.

`supervisor/tests/netchange.py` now forwards loopback mic/camera badge POSTs through Playwright's request transport, as it already does for bootstrap requests. It sends each report once with transport retries and redirect following disabled. It aborts on an uncertain response without falling back to another send. Other mutations, event streams, screen traffic and documents retain their native transport; mapped insecure test hostnames still forward only bootstrap API calls. The narrow route pattern avoids intercepting media streams while Python records audio. See [[Architecture/Web UI]] and [[superpowers/specs/2026-10-22-audio-and-ports-design|Audio and ports design]] for the capture responsibilities and privacy requirements.

# Verification and limits

Run `supervisor/tests/.venv/bin/python -m unittest discover -s supervisor/tests -p test_netchange.py` from the repository root. This bypasses the integration suite's build/setup hook and needs no supervisor or container engine. Five policy tests cover mic/camera selection, a failed report sent once, unrelated mutations/streams staying native, and remote/mapped hosts staying native. A real Chromium test against a loopback HTTP stub checks the JSON report body, session cookie and error response are preserved and received once. All six tests passed on macOS. Loopback binding and Chromium require execution outside the filesystem sandbox.

The real Linux Docker network-change event and the complete native/container CI suites were not reproduced locally. The settings test requires an isolated integration engine; no production supervisor or ordinary integration fixture was started against the shared development engine. A CI rerun remains the end-to-end verification for the original failures.

# Event WebSocket coverage follow-up

`supervisor/internal/server/ui_events_test.go` also exercises `socketEventWriter` against a real `httptest` WebSocket pair. An acknowledged heartbeat keeps the feed active; closed and non-reading peers cancel it. Invalid event JSON and a closed writer transport cancel the feed as well. Short parent contexts bound stalled-peer tests without waiting for the production heartbeat interval. Closing the local transport makes the write-failure case deterministic rather than racing the peer close against the operating system's write buffer.

`go test -race ./internal/server -run 'Test(SocketEventWriter|UIEventsWebSocket|ScreenEventsWebSocket)' -count=10 -coverprofile=/private/tmp/deskyard-ui-events-cover.out` passed. In that focused run, `socketEventWriter.send` and `socketEventWriter.comment` each reached 100% statement coverage. This characterizes existing behavior; no production transport change was needed.
