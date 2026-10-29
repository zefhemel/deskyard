# Development

The supervisor needs Go, Node 24 and Docker, and runs only in a container:

```bash
cd supervisor
make dev            # cross-compile, then (re)start a container with the binary
                    # mounted; about 4 s after an edit
make test           # Go unit tests, with the race detector
make test-engine    # the tests that drive a real Docker engine (builds the
                    # desktop image first, unless CC_TEST_IMAGE names one)
make typecheck      # the UI's TypeScript and tests
make vet            # Go static checks
make build          # the UI, both Linux desktop CLIs and bin/deskyard
make linux          # the engine-platform supervisor and both Linux desktop CLIs
make cli-linux      # just the amd64 and arm64 desktop CLIs, bin/desktop-cli/
make base           # the supervisor's base image, deskyard:base (no binary)
make run            # (re)start a supervisor container from deskyard:base
make release VERSION=v1.2.3   # cross-compiled deskyard binaries in dist/
make image          # the supervisor's container image
```

`make dev` is `make linux base run`. `make linux` builds
`bin/deskyard-supervisor-linux` for the Docker engine's architecture (`OUT=`
another file, `GOARCH=` another architecture; `make engine-goarch` prints the
engine's). `make base` builds the supervisor's base image (Alpine, without the
binary), a no-op unless its Dockerfile changed. `make run` starts the container
`NAME` (default `DEV_NAME`, `deskyard-dev`) from it with the Docker socket,
`DATA` (default `DEV_DATA`, `test-instance/` in the top folder) at `/data`,
`BIN` (default `bin/deskyard-supervisor-linux`) bind-mounted over the image's
binary and `bin/desktop-cli/` mounted read-only at `/opt/deskyard/cli`, on
`127.0.0.1:PORT` (default `DEV_PORT`, 7343), replacing a container of that
name. `RUN_ARGS` adds `docker run` options and `ARGS` the binary's arguments;
the supervisor suite and the VPN fixture start their supervisors with it. The
same `make dev` after every edit rebuilds the UI only when its sources changed.
Run a second instance beside it with
`make dev DEV_DATA=/tmp/scratch DEV_PORT=7344 DEV_NAME=deskyard-dev2`. With
Compose, run `make linux` and `docker compose up -d --force-recreate` using
`supervisor/compose.yaml`, which mounts the same. The supervisor refuses to run
outside a container (it must find its own container on the engine); the
`deskyard` command released from `make release` is the only binary
distributed.

The supervisor publishes the shipped Linux amd64 and arm64 CLIs into
`/data/state/cli/linux-<arch>/deskyard` on startup. Runtime-capable images
(`deskyard.cli=runtime-v1`) receive the matching directory as a strict read-only
mount at `/opt/deskyard/cli`; `/usr/local/bin/deskyard` links to it. Updating the
supervisor updates new CLI invocations in running desktops; existing processes
keep their original executable. The mounted directories stay in place while
individual binaries are replaced atomically.

For CLI-only edits, run `make cli-linux` then `docker restart deskyard-dev`
(or `docker compose restart supervisor`). For supervisor changes, use `make dev`
or the Compose force-recreate command above to refresh its binary mount too.
Neither path rebuilds desktop images. Existing images with a baked-in CLI need
a one-time image rebuild and environment **Upgrade** to acquire the symlink
and mount. Until upgraded, they retain their original CLI.

For a standalone desktop started with `docker run`, first run `make cli-linux`
in `supervisor/`, then mount `bin/desktop-cli/linux-<arch>/` read-only at
`/opt/deskyard/cli`. Use the desktop image's architecture, reported by
`docker image inspect --format '{{.Architecture}}' IMAGE`, rather than the
architecture of your workstation. For example, add
`--mount type=bind,src="$PWD/bin/desktop-cli/linux-amd64",dst=/opt/deskyard/cli,readonly`
when starting an amd64 desktop from that folder. The image and engine test
fixtures supply the matching mount automatically.

## Repository layout

```
supervisor/
  cmd/deskyard-supervisor/     the supervisor
  cmd/deskyard/     the deskyard command (inside environments and for remote access)
  internal/         engine interface and Docker implementation, accounts, auth,
                    environments, storage remotes and backups, the HTTP server
                    and proxy, the embedded UI
  web/              the UI: Preact + TypeScript + Tailwind, built with Vite
  tests/            integration tests against a real engine
  Dockerfile        node build -> go build -> alpine
  compose.yaml      the supervisor's base image with the checkout's binary mounted, for development
deploy/
  compose.yaml      the supervisor's published image, for production
images/
  Dockerfile       build stages and the XFCE desktop target
  common/          shared rootfs and streaming patches
  debian-xfce/     XFCE rootfs overlay
  tests/           shared desktop image tests
  ci/              CI helpers (build-needed: whether images need building)
docs/               local copy of the SilverBullet project space (ignored by git)
```

CI: `.github/workflows/images.yml` builds all `docker-bake.hcl` targets together for
amd64 and arm64 on native runners, weekly and when desktop image inputs change
(`images/`, `docker-bake.hcl` or the workflow; `images/ci/build-needed BASE`
decides, and runs locally too, e.g. `images/ci/build-needed HEAD~1`).
CLI-only changes no longer trigger desktop publication;
`.github/workflows/supervisor.yml` tests the supervisor, publishes its image,
builds the `deskyard` command binaries (the supervisor is released only as
the image) and puts them in the `edge` pre-release (main) or the GitHub
release (tags `v*`);
`.github/workflows/tests.yml` runs the integration suites (see [Tests](#tests)).
`install.sh` installs `deskyard` from a release.

## Project records

Project documentation lives in the SilverBullet space "docs", which is synced and versioned outside this repository. A local copy sits in `docs/`, which git ignores; edit pages there or in SilverBullet and let sync carry them, rather than committing them. Links from the README and these guides name pages in that space instead of linking files.

The space's *Operations manual* describes how the owner and agents plan, work, review feedback, verify outcomes and maintain the records. Its *index* page is the owner dashboard: requests, current capabilities and unscheduled loose ends through live SilverBullet views. No baked sections are used. The space tracks features, outcome-based milestones, design specs, implementation plans, components, spikes and verification evidence. Its *AGENTS* page (the documentation manual) defines their fields and lifecycle; its *CONFIG* page registers native SilverBullet schemas. Use SilverBullet comments addressed to `@agent` for contextual feedback; the *Agent inbox* page collects open requests for agents to check during work. Invoke `$sb-inbox` to process that queue explicitly; its repository skill lives in `.agents/skills/sb-inbox/`. Milestones have no target dates. Completed implementation plans are purged after durable decisions and evidence are retained; design specs stay while they describe the current system or agreed future work. The space's remote versioning preserves the history. From the repository root, with SilverBullet Desktop available, check metadata and relationships using `sb --space "docs" script --file scripts/check-docs.lua --json`; run `sb` outside an agent sandbox so it can reach Desktop.

## Tests

From the top folder; the pytest suites need Docker, [uv](https://docs.astral.sh/uv/)
and, once, `uv run playwright install chromium` in each of
`images/tests` and `supervisor/tests`:

| Command | Runs | When |
|---|---|---|
| `make test` | Go (vet, race) and engine tests, the UI's, and both pytest suites without what's slow, needs the internet, builds images, measures timing, or runs WebKit. About 7 minutes on a quiet Mac, 10 under load. | Before every commit. |
| `make test-ci` | `make test` plus WebKit and Firefox: what CI runs on a pull request. | To check a browser difference. |
| `make test-all` | Everything, including what is slow or needs the internet (`MARKS="-m ''"`). About 25 minutes. | Before a release, or after changing desktop Dockerfiles, image runtime behavior or audio timing. CI runs it on main and nightly. |
| `make test-image`, `test-supervisor`, `test-go`, `test-web`, `test-engine` | One part. | While working on it. |

While working on one area, narrow a suite: `make test-supervisor PYTEST_ARGS="-k drop"`,
or run pytest in the suite's folder (`uv run pytest test_ui_audio.py`).
`MARKS="-m ''"` lifts the default selection; markers: `slow`, `network`,
`build`, `perf`, `webkit` (left out by default) and `serial`.

The supervisor suite always runs the supervisor in a container: each worker
starts one with `make run`, as `make dev` does, with its data folder at `/data`
and the cross-compiled binary mounted, so a code change needs no image build. Go engine tests that reach desktops by IP skip on macOS when
OrbStack can't route to the custom bridge networks; Linux CI runs them.

Both suites run under pytest-xdist (`WORKERS`, default 4), then run the
timing-sensitive tests, marked `serial`, on their own (`pytest --only-serial`).
Each test gets a new environment, taken from a few started ahead of it. The
supervisor suite runs a supervisor per worker, each on its own slice of
`DESKYARD_TEST_SUBNET` (default 10.215.0.0/20): give checkouts that test at the
same time different ones. Teardown removes only the containers and networks labelled with that run's supervisor instance, plus managed containers mounting its data folder. The supervisor suite rebuilds the web UI first when
its sources are newer than the build. `TEST_IMAGE` names the desktop image the
suites build and test (default `deskyard-debian-xfce:test`); outside make,
`AC_IMAGE` (image suite) and `CC_TEST_IMAGE` (supervisor suite) name it.
Firefox is skipped where Playwright's Firefox can't start (on some Macs).
On Linux, tests write workspace fixtures through the desktop as UID 1000,
since the runner may not own the workspace. In Chromium, loopback bootstrap API GETs, UI assets, login and mic badge POSTs use
Playwright's request transport to avoid Chromium's `ERR_NETWORK_CHANGED` when
Docker changes network interfaces. Each request is sent once, including login
POSTs; other mutations, downloads, live streams and Selkies' client use the browser. Documents stay on the native browser transport to preserve their network address space for event WebSockets. Navigation
on the browser transport retries that specific error; other browser errors still
fail. Engine tests pull their own auxiliary images,
including `python:3.13-alpine` for memory reclamation.
The plain-HTTP test hostnames forward only bootstrap API calls, preserving their
insecure document origin and native screen connections. Other browsers use their
native transport throughout.

```bash
docker buildx bake --load     # the desktop image
cd images/tests
uv run playwright install chromium   # once
uv run pytest -m "not slow"
uv run pytest                        # also: memory budget, Flatpak app, CLIs
```

Desktop image tests for `deskyard.fuse="userns-v1"` need `/dev/fuse` on the Docker
host. Their user exec helper joins the shared desktop namespace, as the supervisor
does. `test_flatpak.py` checks document export/read/write, namespace sharing and
read-only mount protection with and without `no-new-privileges`.

Screenshots and stats land in `images/tests/artifacts/`. `AC_IMAGE=<tag>`
skips the build and tests that image instead, for example:

```bash
AC_IMAGE=deskyard-debian-xfce:dev uv run pytest -m "not slow and not network"
```

For shared-engine VPN verification, use the [scoped VPN test guide](../supervisor/tests/VPN.md). Its `vpnintegration` test binary protects existing resources with a unique ownership label. Do not start an ordinary production supervisor or the full-main Python fixture against a Docker engine holding unrelated environments.

For UI development against an existing test supervisor, run `DESKYARD_API=http://<supervisor>:7343 npm run dev` in `supervisor/web`. The Vite proxy rewrites both Host and Origin to the supervisor for API and desktop stream requests, so login and other same-origin checks work through the development URL. Without `DESKYARD_API`, Vite uses mock data.

In current desktop images, control a user service as the desktop user with `/usr/local/bin/workspace-exec supervisorctl -c /etc/workspace/user-services.conf stop selkies` (or `start selkies`). The root supervisor controls system services; the user control socket is private. Allow at least 45 seconds when stopping disposable desktop containers to observe the complete ordered shutdown.
