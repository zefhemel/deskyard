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

The supervisor binds 80 and 443 as UID 1000. That works because Docker lets
any user in a container's own network namespace bind low ports
(`net.ipv4.ip_unprivileged_port_start=0`); with `--network host` it would not,
and the supervisor can't run there anyway, since it finds its own container
by hostname.

## The container

The supervisor runs only in a container, with the Docker socket and a host
folder mounted at `/data`:

```bash
docker run -d --name deskyard --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /srv/deskyard:/data \
  -p 127.0.0.1:7343:7343 \
  ghcr.io/zefhemel/deskyard:latest
```

Any host folder will do on the left of `:/data`, but it has to be a bind mount:
a named volume is refused at start, because the supervisor must know the
folder's path on the host to hand it to desktops. It looks that path up by
inspecting its own container (the container ID is the hostname, so don't set
`--hostname`).

The image starts the supervisor as root for one reason: to take ownership of
the supervisor's files in a data folder from an older version (once, recorded
in `state/owner`), and to read which group owns the Docker socket. It then
drops to UID 1000 with that group before it opens the engine, the stores or
the listener, and never serves as root. So you don't set `user:` or
`group_add`; the socket may be owned by any group (or be world-accessible, or
a TCP address in `DESKYARD_ENGINE_HOST`). The desktops' home and volume
folders are owned by UID 1000 too.

**Upgrading an old Compose file or `docker run` command.** Earlier versions
mounted the data folder at its host path and named it in `DESKYARD_DATA_DIR`
(`-v /srv/deskyard:/srv/deskyard -e DESKYARD_DATA_DIR=/srv/deskyard`). The
supervisor now exits at start, with the instructions, if `DESKYARD_DATA_DIR`
is set to anything but `/data`. Replace the mount with
`-v /srv/deskyard:/data` and delete the variable; existing environments, homes
and volume grants carry over. Environments with a VPN connection are replaced
once on the first start: their desktops restart; homes and volumes are kept.
The `deskyard-supervisor` binaries that used to
run without a container are no longer released (the `deskyard` command still
is).

## On a LAN

To reach Deskyard from other machines at home or in an office, either
serve it over https or accept what plain http costs.

**Plain http** (publish `-p 7343:7343` instead of `-p 127.0.0.1:7343:7343`,
then open `http://192.168.1.20:7343`): browsers treat any address but
`localhost` over http as insecure and turn features off. The desktop page says so in
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
Tailscale: keep the default `-p 127.0.0.1:7343:7343`, then
`tailscale serve --bg 7343` on the same machine, and open
`https://<machine>.<tailnet>.ts.net` from any device on your tailnet. Or
put any reverse proxy with a certificate in front (Caddy:
`reverse_proxy 127.0.0.1:7343`); it must pass WebSockets through and keep
the `Host` header. With a public domain, `DESKYARD_DOMAIN` does it
itself ([With a domain](#with-a-domain)).

## Security

- **The engine socket is root on the host.** Whoever can use it owns the
  machine, so the supervisor is the most privileged thing there. Mounting
  the socket into its container is the same.
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
  the supervisor has. Environments publish no ports to the host.
- The supervisor never serves pages from an environment: it serves the desktop
  client itself (taken from the environment's image) and proxies only the
  desktop's WebSocket and, for CLI clients, the browser's CDP.
- No `sudo` in environments, and every command the supervisor runs in one runs
  as `user`.
- Over plain HTTP, passwords travel in clear text: keep the default
  `127.0.0.1` publish unless the network is trusted, or set
  `DESKYARD_DOMAIN` for HTTPS.
