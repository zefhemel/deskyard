# Environments

Admins create environments with a name, an image from the catalog, and
optionally a memory and CPU limit (by default an environment may use all of the
host's). Names are 2 to 32 lowercase letters, digits and dashes, start with a letter
and can't be changed later; the name is the environment's identity. Each
environment runs as a container `deskyard-<name>` on its own network
`deskyard-<name>`, so environments can't reach each other.

The supervisor keeps each environment's definition (image, limits, network,
volume grants, Docker access, memory preference and whether it should
run) in `computers.json`, not on its container. The container is
disposable: if it is lost, the supervisor recreates it from the definition
with the same name, home folder, access grants, shares and settings. A stopped
environment may have no container; it gets one when started. Stopping keeps the
container, so changes outside `/workspace` survive until the container is next
recreated.

Every settings change that affects the container is saved as a new revision
and then applied by recreating the container. The old container is stopped and
set aside until the new one is ready; if the new one fails, the old one comes
back (running if it was) and the environment shows the error, with the saved
change still pending. A pending change is tried again only when you start or
restart the environment or edit its settings. Every 30 seconds, and when a
container exits or disappears, the supervisor starts environments that should
be running. Containers it can't match to a definition, or several claiming one
environment, are shown to administrators as conflicts on the Environments page;
the supervisor leaves them alone, and a duplicate blocks that environment's
start, stop and settings until an administrator removes the extra container. A
name claimed by such a container can't be used for a new environment until the
container is removed. If a new environment's desktop isn't ready within two
minutes of its first boot, the creation is undone. Removing an environment
stops it first, so an interrupted removal never brings it back.

## Docker daemon access

**Docker daemon access** is an administrator checkbox when creating an environment
or editing its Settings. It defaults off. Enable it on a desktop image with
the Docker CLI, such as the standalone [Docker example](../examples/docker/README.md).
The supervisor checks and mounts the engine host's `/var/run/docker.sock`;
an updated desktop image gives `user` its socket group. Saving a changed
setting recreates the environment's container, preserving its workspace.

Everyone using that environment can then control the shared Docker daemon and
its containers, with host administrator privileges. Only enable this for
trusted users. This is separate from volumes;
custom/rootless daemon socket locations are not currently supported.

## Outbound VPN

Administrators can add named connections under **Networking** in the top bar (`/_vpn`), choose a country and enter [NordVPN service credentials](https://support.nordvpn.com/hc/en-us/articles/19685514639633-How-to-find-your-service-credentials-for-manual-NordVPN-setup), which differ from the account login. **Test connection** checks a candidate without changing environments; blank credentials on an edit retain their saved values. Choose a saved connection or **No VPN** when creating an environment or on its Settings tab. The same operations are available through the [VPN API](../supervisor/API.md#vpn-connections-admin). Each selected environment gets its own managed Gluetun gateway. Internet access stays blocked while the tunnel is unavailable; only an explicit selection of no VPN restores direct networking. Enabling, disabling or changing VPN settings recreates the environment's container while preserving its name and workspace. Saving settings warns about session disconnection and uses **Save and restart** for running environments; files remain intact.

VPN environment containers do not restart independently after an engine reboot: run the supervisor to recover their namespace and firewall before starting them. While the supervisor runs, background recovery restores the saved power state after an unexpected container exit or a failed stop, retaining a healthy unchanged gateway and starting a stopped one again in place; a container that keeps crashing is restarted with growing delays, up to five minutes. A stopped environment's gateway is not repaired until the environment is started. Interrupted network changes, including an explicit No VPN selection, stay visible with a safe diagnostic and are finished when the supervisor starts; a change that failed is retried on start, restart or another edit. Direct access returns only after that selected transition completes. Stopping an environment keeps its gateway idle and may continue consuming a provider connection. LAN access is denied except for administrator-configured destination, transport and port rules. Existing agent port forwarding remains available; this feature does not prevent deliberate proxies or tunnels through permitted applications.

Shared connection edits list the affected environments and require confirmation before validating and applying a new revision. Validation can be cancelled before commit; a failed validation keeps the saved revision. Each environment's rollout reports its own result and retains its selected policy on failure. Connections still used by an environment cannot be deleted. Users with access to an environment see separate VPN health, current exit IP only while connected, historical observations and idle status; only administrators change the configuration.

The OrbStack verification uses real Selkies desktops, the supervisor proxy, agent exec/files/activity and shared Chromium with two independent controlled WireGuard tunnels. It covers the supervisor in a container (earlier checks also ran one outside a container, which is no longer supported), blocked requests during tunnel loss, local access and recovery. Controlled WireGuard does not validate NordVPN service authentication. Live NordVPN service authentication, verified desktop egress and visible MATE desktop access were confirmed on OrbStack on 24 October 2026. Country/credential changes, provider limits, Linux hosts and a full engine/host reboot remain release checks; see the [verification guide](../supervisor/tests/VPN.md) for the exact scope and outstanding observations.

Each enabled environment adds one long-lived gateway container with its own CPU/memory use and provider connection; stopping the environment does not release that cost. Environment resource limits apply to its container, not its gateway. Version one uses public DNS through the tunnel, blocks external IPv6 and Docker's embedded resolver, and has no custom DNS selector. LAN exceptions intentionally send only their configured private IPv4 destination, TCP/UDP and ports outside the tunnel; other environment/control networks and link-local destinations cannot be excepted.

## What persists

An environment's private persistent folder is `/workspace`: the folder
`<data folder>/home/<name>` on the host, which you can browse and back up with host tools. Creating an environment
with the name of a folder that already exists reuses that folder, so to copy
an environment, copy its folder and create an environment with the new name.
Removing an environment keeps its folder unless you tick "also delete files".
If an environment's folder goes missing, the supervisor reports an error and
does not start it; it never creates the folder again by itself.

| Need | Command |
|---|---|
| Runtimes and CLI tools | `mise use -g node@22 python@3.12 go jq ripgrep` |
| npm CLIs | `npm i -g <pkg>` |
| Python CLIs | `uv tool install <pkg>` |
| Desktop apps | `flatpak install --user flathub <app-id>` |
| Anything else | put binaries in `~/.local/bin` |

These all land in `/workspace` and are on `PATH`. Node and Python come
preinstalled with the image (outside `/workspace`, so they update with it);
`mise use` wins over them. There's no `sudo`, and anything installed with
`apt` or changed outside `/workspace` is lost when the image is updated.

Stopping, starting and restarting keep the environment's container, so changes
outside `/workspace` (including `/tmp`) last until the container is rebuilt:
by an upgrade or a settings change that needs it. Stopped environments keep
their container too. Nothing in `/workspace` or a shared folder is ever
cleaned up; only removing an environment with "also delete files" deletes its
folder.

uv defaults to its own Python installations in `/workspace` so installed
tools do not depend on the image's Python. This default comes from
`/etc/uv/uv.toml`; explicit uv flags and user configuration can override it.
Choosing the image's Python explicitly makes that environment dependent on
the image version.

## Desktop state across restarts

When an environment stops (stop, restart, upgrades), its native session manager
logs out and saves the session; Chromium saves its tabs independently.
Native session restoration applies to applications that support that
desktop's session protocol (XFCE: Settings > Session and Startup).
Most Electron and
Flatpak apps do not restore their windows through this protocol.

Switching desktop images keeps your files, installed Flatpak apps, tools
and Chromium profile. Each environment keeps its own settings; open windows
are not migrated between different desktop environments.

## Reclaiming memory

Admins can choose **Reclaim memory** from the **⋯** menu on a running environment's details page to
ask the host to release idle memory while its apps keep running. The result
shows resident memory before and after; this includes file cache, and swapped
pages may still use compressed RAM. Savings vary and apps may briefly respond
more slowly when accessed again.

On supported hosts, each environment also gets one automatic 128 MiB reclaim
attempt after 15 minutes with no viewers. A returning viewer resets the timer,
even if they leave again between checks. Active CLI clients, environment operations,
and missing or stale activity reports delay reclamation without consuming the
attempt. Failed attempts are not retried during that absence. Checks run every
30 seconds and requests run one at a time. Supervisor/container restarts start
a fresh timer. Admins can turn off **Automatically reclaim idle memory** in an
environment's Settings; this saves immediately in its definition without a restart and survives
container recreation. The manual button remains available.

Automatic reclamation requires the environment's activity service to send current
command-status heartbeats. Older desktop binaries with only blank heartbeats
keep running normally but skip automatic reclamation; update the desktop image
to use it. Activity detection covers operations recorded by Deskyard, not plain
shell commands or other unreported background work; use the opt-out for those.

This requires a rootful Linux Docker engine with cgroup v2 and swap available
to the environment. It works through the Docker socket, including with the
supervisor running on macOS against OrbStack. The supervisor pulls a small
Alpine helper image if needed and removes its temporary helper containers
after each request. At boot it checks the engine's memory/swap controls and
tests the full helper path, including `memory.reclaim` with swappiness, on a
tiny disposable container. The log reports whether reclamation is enabled
and why it is disabled if a check fails. A failed check leaves the rest of
the supervisor running and disables the button with a reason. Restart the
supervisor after fixing host support to run the check again. Each request
still checks the selected environment's swap settings; a failure there does not
disable reclamation for other environments. No memory limits or host swap
configuration change.
