# Running Deskyard

The README's *Run it* section starts the supervisor in a container for
production and for development. This page covers reaching it from other
machines, and what to know about its security.

## With a domain

Set `DESKYARD_DOMAIN=desktops.example.com`, point its DNS at the host and let
ports 80 and 443 through: the supervisor gets a certificate itself and serves
HTTPS. Publish `80:80` and `443:443` instead of `127.0.0.1:7343:7343`. Without
a domain, keep the default localhost binding and put your own HTTPS proxy or
Tailscale in front (see [On a LAN](#on-a-lan)).

## The binary without a container

The edge release also has `deskyard-supervisor-<os>-<arch>` binaries
(`linux-amd64`, `linux-arm64`, `darwin-amd64`, `darwin-arm64`). Run directly,
the supervisor finds the engine at `DESKYARD_ENGINE_HOST`, else `DOCKER_HOST`,
else the platform's default socket, keeps everything in `~/deskyard` and
listens on `127.0.0.1:7343`; its user must be able to use the Docker socket.
To serve a domain on Linux, let it bind low ports with
`sudo setcap cap_net_bind_service=+ep ./deskyard-supervisor`. This mode is
being retired: the supervisor will run only in a container.

## On a LAN

To reach Deskyard from other machines at home or in an office, either
serve it over https or accept what plain http costs.

**Plain http** (`DESKYARD_LISTEN=0.0.0.0:7343`, then
`http://192.168.1.20:7343`): browsers treat any address but `localhost`
over http as insecure and turn features off. The desktop page says so in
its bar ("Not secure: some features off"). What still works: the picture,
keyboard and mouse, sound, the Clipboard button's text box (both ways),
Lock size, Quality, upload and the CLI activity overlay. Gamepads
should too in Chromium and Safari, which keep the Gamepad API over http. What doesn't:

- **Mic**: the browser has no `getUserMedia`. The Audio panel shows it off,
  with "Needs https".
- **Automatic clipboard sync**: copy and paste go through the Clipboard
  button instead.
- **H.264 in Chrome and Firefox**: without WebCodecs the picture is JPEG.
  At 1920x1080 and the Medium preset that is about 40 Mbit/s while a
  terminal scrolls (H.264: about 6), and next to nothing on a still
  screen. Fine on wired gigabit, workable on good Wi-Fi with Low (about
  18 Mbit/s); not for the internet. When one viewer is on JPEG the whole
  desktop streams JPEG, also to viewers on https.
- **Passwords travel in clear text.** Only on a network you trust.

**https** keeps everything. Without a public domain, the simplest is
Tailscale: run the supervisor on its default `127.0.0.1:7343`, then
`tailscale serve --bg 7343` on the same machine, and open
`https://<machine>.<tailnet>.ts.net` from any device on your tailnet. Or
put any reverse proxy with a certificate in front (Caddy:
`reverse_proxy 127.0.0.1:7343`); it must pass WebSockets through and keep
the `Host` header. With a public domain, `DESKYARD_DOMAIN` does it
itself ([With a domain](#with-a-domain)).

## Security

- **The engine socket is root on the host.** Whoever can use it owns the
  machine, so the supervisor is the most privileged thing there. Run
  directly on Linux, its user needs the `docker` group, which is
  root-equivalent; in a container, mounting the socket is the same.
- Every route but the login, link and CLI-login pages needs a signed-in
  account: a browser session, or a CLI's client credential sent as a bearer
  token (which never sets cookies and needs no `Origin`). Login links carry
  their code in the URL fragment, which browsers don't send, and are exchanged
  once for a session or credential. Secrets are stored as SHA-256 hashes.
  Failed logins, link codes and CLI-login polls are throttled per address.
- Observers' desktop streams pass through a filter that forwards only the
  messages needed to receive the picture; their input never reaches the
  desktop.
- Environments have no password and nothing else of their own facing the
  network: only Caddy on 8080, which wants a per-environment token that only
  the supervisor has. With the supervisor in a container, environments publish
  no ports; run directly, each publishes only 8080 on host `127.0.0.1`.
- The supervisor never serves pages from an environment: it serves the desktop
  client itself (taken from the environment's image) and proxies only the
  desktop's WebSocket and, for CLI clients, the browser's CDP.
- No `sudo` in environments, and every command the supervisor runs in one runs
  as `user`.
- Over plain HTTP, passwords travel in clear text: keep the default
  `127.0.0.1` binding unless the network is trusted, or set
  `DESKYARD_DOMAIN` for HTTPS.
