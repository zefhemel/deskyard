# Tailscale verification

The Tailscale runtime uses the official multi-architecture image pinned in
`internal/vpn/tailscale_runtime.go`. `TestTailscaleKernelEngine` characterizes
its kernel TUN, private daemon socket and firewall support without credentials:

```sh
go test -tags engine ./internal/vpn -run TestTailscaleKernelEngine -v
```

This does not verify enrollment, MagicDNS, peer routing, Serve or Funnel. Those
require a dedicated test tailnet and are exercised through a separate test
instance; never reuse another computer's Tailscale state or production secrets.

The agent keeps the exact test-instance address and walkthrough in its handover.

## Manual acceptance on the isolated trial

The current trial is reachable from the shared desktop at
`http://10.213.0.17:7344`; on the Docker host it is `http://localhost:7344`.
Its data is separate from production. The admin login is stored owner-only in
`/workspace/.local/share/deskyard-tailscale-trial/login.txt` (do not copy it into
issues, commits or test reports). The ready computer is `ts-trial`, initially
using No VPN, with sample content at `/workspace/demo`.

1. In Tailscale, create an OAuth client with `auth_keys` write and
   `devices:core` write permission for `tag:deskyard`. Ensure the tag is owned
   appropriately in the tailnet policy. Enable MagicDNS and HTTPS certificates.
2. In the trial's **Networking**, add **Tailscale**, enter the tailnet name/ID,
   client ID/secret and permitted tag. Test/save it. The test device is ephemeral;
   a connection-test success does not certify Funnel permissions. If Tailscale
   rejects the device tags, define the tag in `tagOwners` and permit that tag for
   the OAuth client before retrying.
3. Open **ts-trial → Settings → Networking**, choose the saved connection and
   save. Confirm `ts-trial` appears as its own device in Tailscale (approve it if
   required). In its terminal run:

   ```sh
   cd /workspace/demo
   python3 -m http.server 3000 --bind 127.0.0.1
   ```

4. Add HTTP service local port **3000**, listener **443**, **Private · Serve**.
   Save HTTP services and open the observed URL from a tailnet device. A browser
   outside the tailnet should not reach it. Test another allowed peer by IP and
   MagicDNS from the desktop; check normal public browsing still works.
5. Allow the `funnel` node attribute for `tag:deskyard` in Tailscale policy. Change
   the rule to **Public internet · Funnel**, save, then open the observed URL from
   a device with Tailscale disconnected. Confirm the demo page is publicly
   reachable. Funnel does not add a Deskyard login.
6. Change back to private and check outside access is withdrawn. Stop the
   computer, clear/change rules while stopped, then start it: old public exposure
   must not return. Restart the demo process after computer stop or provider
   replacement. Verify stop/start, sidecar replacement and desktop image upgrade
   preserve the Tailscale device ID; provider disconnection retires it.
7. Add a second uniquely named computer with the same saved connection and check
   it has a distinct device ID. Verify peer access rules, denied LAN/control
   access, WebSockets and streaming using a suitable app. Test actual engine-host
   reboot separately, never as an inference from restarting only the supervisor.

A provider selection recreates the desktop and ends running app processes; its
workspace and demo content persist. HTTP-rule-only edits do not recreate it.
Record observed results and environments in the project space's Tailscale
verification page before claiming live acceptance.

## Private node-state ownership check

When tests run inside a desktop but Docker is on its host, set
`DESKYARD_TEST_HOST_WORKSPACE` to the engine-host path mounted at `/workspace`:

```sh
DESKYARD_TEST_HOST_WORKSPACE=/host/path/to/workspace \
  go test -tags engine ./internal/vpn \
  -run 'TestTailscale(KernelEngine|OfflineStateOwnerEngine)' -v
```

This fixture creates only uniquely named, labelled containers and a temporary
folder under `/workspace/.cache`. It verifies root-owned 0600 node/certificate
state can be safely prepared offline without losing node keys or restoring a
saved public listener. It does not need Tailscale enrollment credentials.
