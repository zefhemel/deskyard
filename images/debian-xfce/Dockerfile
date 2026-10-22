# syntax=docker/dockerfile:1
FROM debian:trixie-slim

ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl procps supervisor dbus dbus-x11 \
        x11-utils xfce4 xfce4-terminal fonts-dejavu-core \
        chromium socat xdotool maim flatpak \
    && rm -rf /var/lib/apt/lists/* \
    # rootfs/usr/bin/flatpak wraps the real binary.
    && dpkg-divert --local --rename --divert /usr/bin/flatpak.real --add /usr/bin/flatpak

# KasmVNC: X server, VNC server and web client in one (Kasm's own packages).
ARG KASMVNC_VERSION=1.5.0
ARG TARGETARCH
RUN curl -fsSL -o /tmp/kasmvnc.deb \
        "https://github.com/kasmtech/KasmVNC/releases/download/v${KASMVNC_VERSION}/kasmvncserver_trixie_${KASMVNC_VERSION}_${TARGETARCH}.deb" \
    && apt-get update && apt-get install -y --no-install-recommends /tmp/kasmvnc.deb \
    && rm -rf /var/lib/apt/lists/* /tmp/kasmvnc.deb \
    # English UI for everyone: the client picks a translation by skipping any
    # English but en-US, so en-GB + Polish browsers got Polish.
    && rm -r /usr/share/kasmvnc/www/app/locale

COPY --from=caddy:2.11.7 /usr/bin/caddy /usr/local/bin/caddy
COPY --from=ghcr.io/astral-sh/uv:0.13.4 /uv /uvx /usr/local/bin/

RUN mkdir -p /etc/workspace \
    && curl -fsSL -o /etc/workspace/flathub.flatpakrepo https://dl.flathub.org/repo/flathub.flatpakrepo

ARG MISE_VERSION=v2026.10.17
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

VOLUME ["/workspace"]
# The only port: Caddy, which needs the supervisor's per-computer token
# (WORKSPACE_PROXY_TOKEN). Computers only run behind the supervisor.
EXPOSE 8080

# The image contract the supervisor checks before using an image.
ARG VERSION=dev
LABEL container-computer.contract="1" \
      org.opencontainers.image.title="Debian XFCE" \
      org.opencontainers.image.description="Debian trixie with XFCE, Chromium, Flatpak, mise and uv" \
      org.opencontainers.image.version="${VERSION}"

# Desktop + web client. Not Chromium: probing CDP would reopen a browser
# the human closed.
HEALTHCHECK --interval=10s --timeout=5s --start-period=30s --retries=3 \
    CMD /usr/bin/xdpyinfo -display :1 >/dev/null && /usr/bin/curl -fs -o /dev/null http://127.0.0.1:6080/

WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/workspace-init"]
