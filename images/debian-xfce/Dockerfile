# syntax=docker/dockerfile:1

# The `deskyard` command (supervisor/cmd/deskyard), built for the target
# platform on the build machine's.
FROM --platform=$BUILDPLATFORM golang:1.27-trixie AS desktop
WORKDIR /src
COPY supervisor/go.mod supervisor/go.sum ./
RUN go mod download
COPY supervisor/ ./
ARG TARGETOS TARGETARCH VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /desktop ./cmd/deskyard

# Gamepads for apps: Selkies' Input Interposer (an LD_PRELOAD library that
# serves /dev/input/js0-3 and event1000-1003 from Selkies' sockets) and
# fake-udev (lists those pads to libudev users such as SDL). Neither is in the
# PyPI wheel, so both are built from Selkies' source at tag 2.1.0, for the
# target platform.
FROM debian:trixie-slim AS gamepads
ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl gcc libc6-dev make \
    && rm -rf /var/lib/apt/lists/*
ARG SELKIES_COMMIT=6ee68a7a815956e6745e916741f13335e0b3ff53
ARG SELKIES_SOURCE_SHA256=b28d7ab88b537c5949cce15444e729ba11147cdf8091861abbe4385f8a0c64eb
WORKDIR /src
RUN curl -fsSL -o selkies.tar.gz "https://github.com/selkies-project/selkies/archive/${SELKIES_COMMIT}.tar.gz" \
    && echo "${SELKIES_SOURCE_SHA256}  selkies.tar.gz" | sha256sum -c - \
    && tar -xzf selkies.tar.gz --strip-components=1 \
        "selkies-${SELKIES_COMMIT}/addons/input-interposer" "selkies-${SELKIES_COMMIT}/addons/fake-udev" \
    # /usr/$LIB/ in LD_PRELOAD resolves to the multiarch directory.
    && lib=/out/usr/lib/$(gcc -print-multiarch) && mkdir -p "$lib" /out/opt/selkies/lib/fake-udev \
    && gcc -O2 -shared -fPIC -o "$lib/selkies_input_interposer.so" \
        addons/input-interposer/input_interposer.c -ldl -pthread \
    && make -C addons/fake-udev CFLAGS="-O2" \
    && cp addons/fake-udev/libudev.so.1.0.0-fake /out/opt/selkies/lib/fake-udev/libudev.so.1

FROM debian:trixie-slim

ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl procps supervisor dbus dbus-x11 \
        x11-utils xfce4 xfce4-terminal fonts-dejavu-core \
        chromium socat xdotool xclip maim flatpak \
        pipewire pipewire-pulse wireplumber pulseaudio-utils \
    && rm -rf /var/lib/apt/lists/* \
    # rootfs/usr/bin/flatpak wraps the real binary.
    && dpkg-divert --local --rename --divert /usr/bin/flatpak.real --add /usr/bin/flatpak

# The screen: Xvfb as the X server, and Selkies streaming it (H.264 and Opus
# over one WebSocket) with its web client, from PyPI wheels into a venv.
# Pinned: Selkies 2.1.0 (the latest 2.1.x on 2026-10-22) and the pixelflux and
# pcmflux versions it requires. Its web client lands in
# /opt/selkies/lib/python3.13/site-packages/selkies/selkies_web, which the
# supervisor extracts from the image.
ARG SELKIES_VERSION=2.1.0 PIXELFLUX_VERSION=2.2.1 PCMFLUX_VERSION=2.2.0
RUN apt-get update && apt-get install -y --no-install-recommends \
        xvfb x11-xserver-utils xcvt python3-venv \
    && rm -rf /var/lib/apt/lists/* \
    && python3 -m venv /opt/selkies \
    && /opt/selkies/bin/pip install --no-cache-dir \
        "selkies==${SELKIES_VERSION}" "pixelflux==${PIXELFLUX_VERSION}" "pcmflux==${PCMFLUX_VERSION}" \
    && /opt/selkies/bin/selkies --help >/dev/null \
    && test -f /opt/selkies/lib/python3.13/site-packages/selkies/selkies_web/index.html

COPY --from=caddy:2.11.7 /usr/bin/caddy /usr/local/bin/caddy
COPY --from=ghcr.io/astral-sh/uv:0.13.4 /uv /uvx /usr/local/bin/

RUN mkdir -p /etc/workspace \
    && curl -fsSL -o /etc/workspace/flathub.flatpakrepo https://dl.flathub.org/repo/flathub.flatpakrepo

ARG MISE_VERSION=v2026.10.17
ARG TARGETARCH
RUN case "$TARGETARCH" in arm64) arch=arm64 ;; amd64) arch=x64 ;; *) echo "unsupported $TARGETARCH" >&2; exit 1 ;; esac \
    && curl -fsSL -o /usr/local/bin/mise \
        "https://github.com/jdx/mise/releases/download/${MISE_VERSION}/mise-${MISE_VERSION}-linux-${arch}" \
    && chmod +x /usr/local/bin/mise \
    && mise --version

# Preinstalled runtimes, installed with mise at build time into /opt/runtimes
# (outside mise's own bookkeeping, so users' `mise use -g node@22` simply
# wins on PATH). They update with the image.
ARG NODE_VERSION=24.22.1
ARG PYTHON_VERSION=3.13.16
RUN MISE_YES=1 MISE_CACHE_DIR=/tmp/mise-cache MISE_DATA_DIR=/opt/mise MISE_CONFIG_DIR=/tmp/mise-config \
        mise install node@${NODE_VERSION} python@${PYTHON_VERSION} \
    && mkdir -p /opt/runtimes \
    && ln -s /opt/mise/installs/node/${NODE_VERSION} /opt/runtimes/node \
    && ln -s /opt/mise/installs/python/${PYTHON_VERSION} /opt/runtimes/python \
    && rm -rf /tmp/mise-cache /tmp/mise-config \
    && /opt/runtimes/node/bin/node --version \
    && /opt/runtimes/python/bin/python --version

RUN useradd --system --home-dir /var/lib/caddy --create-home --shell /usr/sbin/nologin caddy \
    && groupadd -g 1000 user \
    && useradd -u 1000 -g user -d /workspace -M -s /bin/bash user \
    && mkdir -p /workspace \
    && chown user:user /workspace

ENV HOME=/workspace \
    USER=user \
    DISPLAY=:1 \
    XDG_RUNTIME_DIR=/run/workspace \
    DBUS_SESSION_BUS_ADDRESS=unix:path=/run/workspace/bus \
    WORKSPACE_RESOLUTION=1280x800 \
    NO_AT_BRIDGE=1 \
    MISE_DATA_DIR=/workspace/.local/share/mise \
    NPM_CONFIG_PREFIX=/workspace/.npm-global \
    UV_PYTHON_PREFERENCE=only-managed \
    XDG_DATA_DIRS=/workspace/.local/share/flatpak/exports/share:/var/lib/flatpak/exports/share:/usr/local/share:/usr/share \
    PATH=/workspace/.local/bin:/workspace/.npm-global/bin:/workspace/.local/share/mise/shims:/opt/runtimes/node/bin:/opt/runtimes/python/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

COPY images/debian-xfce/rootfs/ /
COPY --from=desktop /desktop /usr/local/bin/deskyard
COPY --from=gamepads /out/ /

VOLUME ["/workspace"]
# The only port: Caddy, which needs the supervisor's per-desktop token
# (WORKSPACE_PROXY_TOKEN). Desktops only run behind the supervisor.
EXPOSE 8080

# The image contract the supervisor checks before using an image.
ARG VERSION=dev
LABEL deskyard.contract="3" \
      org.opencontainers.image.title="Debian XFCE" \
      org.opencontainers.image.description="Debian trixie with XFCE, Chromium, Flatpak, mise and uv" \
      org.opencontainers.image.version="${VERSION}"

# The X server and Selkies. Not Chromium: probing CDP would reopen a browser
# the human closed.
HEALTHCHECK --interval=10s --timeout=5s --start-period=30s --retries=3 \
    CMD /usr/bin/xdpyinfo -display :1 >/dev/null && /usr/bin/curl -fs -o /dev/null http://127.0.0.1:6080/api/health

WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/workspace-init"]
