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

# pixelflux (Selkies' screen capture and encoder), built from its source at
# tag 2.2.1 with Deskyard's idle-grab patch (images/common/patches). The
# codec libraries are built from source at the commits pixelflux's own wheel
# build pins (the before-all script in its pyproject.toml), as the PyPI wheel's
# are, and land in /opt/pixelflux/lib, which the module finds by its rpath.
FROM debian:trixie-slim AS pixelflux
ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential clang libclang-dev cmake nasm meson ninja-build pkg-config git curl ca-certificates \
        python3-dev python3-venv libpixman-1-dev libx11-dev libxext-dev libxfixes-dev libdrm-dev \
        libinput-dev libxkbcommon-dev libgbm-dev libwayland-dev libudev-dev libxcb1-dev \
    && rm -rf /var/lib/apt/lists/*
ARG PIXELFLUX_VERSION=2.2.1
ARG PIXELFLUX_COMMIT=67b6d833fc8eba87f9b15fb4cb3382d923a2fe56
# GitHub archive checksums: GitHub has changed archive bytes before, so a
# sudden mismatch here more likely means that than a tampered tag. Check, then update.
ARG PIXELFLUX_SOURCE_SHA256=6984b587bf1384445fe3ef82aa37fe07daab224dcf06599eb7454433b48a4ad3
WORKDIR /src
RUN curl -fsSL -o /tmp/pixelflux.tar.gz "https://github.com/linuxserver/pixelflux/archive/${PIXELFLUX_COMMIT}.tar.gz" \
    && echo "${PIXELFLUX_SOURCE_SHA256}  /tmp/pixelflux.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/pixelflux.tar.gz --strip-components=1 && rm /tmp/pixelflux.tar.gz \
    && grep -qx "version = \"${PIXELFLUX_VERSION}\"" pixelflux/Cargo.toml
# The codec libraries: the part of the wheel build's before-all script that
# defines and builds them, without its manylinux/Alpine package installs.
RUN python3 -c 'import tomllib; s = tomllib.load(open("pyproject.toml", "rb"))["tool"]["cibuildwheel"]["linux"]["before-all"]; head, sep, _ = s.partition("\n    if command -v dnf"); assert sep and "build_codec_libraries()" in head, "pixelflux before-all changed shape"; print(head + "\nbuild_codec_libraries")' > /tmp/codecs.sh \
    && bash /tmp/codecs.sh && ldconfig && rm -rf /tmp/*
ARG RUST_VERSION=1.99.1
RUN url="https://static.rust-lang.org/rustup/dist/$(uname -m)-unknown-linux-gnu/rustup-init" \
    && curl -fsSL -o /tmp/rustup-init "$url" \
    && echo "$(curl -fsSL "$url.sha256" | cut -d' ' -f1)  /tmp/rustup-init" | sha256sum -c - \
    && chmod +x /tmp/rustup-init && /tmp/rustup-init -y --profile minimal --default-toolchain "${RUST_VERSION}" \
    && rm /tmp/rustup-init
ENV PATH=/root/.cargo/bin:$PATH
RUN cargo fetch --locked --manifest-path pixelflux/Cargo.toml \
    && python3 -m venv /build \
    && /build/bin/pip install --no-cache-dir setuptools==84.2.1 setuptools-rust==1.13.0 wheel==0.48.0
# Each patch must apply exactly (git apply allows no fuzz), so a version bump
# that moves the patched code fails here instead of dropping the patch.
COPY images/common/patches/pixelflux-*.diff /patches/
RUN for p in /patches/pixelflux-*.diff; do git apply --verbose "$p" || exit 1; done \
    && RUSTFLAGS="-C link-arg=-Wl,-rpath,/opt/pixelflux/lib" \
        /build/bin/pip wheel --no-deps --no-build-isolation -w /out/wheels . \
    && mkdir -p /out/opt/pixelflux/lib && cp -a /usr/local/lib/lib*.so* /out/opt/pixelflux/lib/

FROM debian:trixie-slim AS runtime

ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl procps supervisor dbus dbus-x11 \
        x11-utils xdg-utils fonts-dejavu-core \
        chromium socat xdotool xclip maim flatpak git openssh-client tmux \
        pipewire pipewire-pulse wireplumber pulseaudio-utils \
    && rm -rf /var/lib/apt/lists/* \
    # rootfs/usr/bin/flatpak wraps the real binary.
    && dpkg-divert --local --rename --divert /usr/bin/flatpak.real --add /usr/bin/flatpak

# The screen: Xvfb as the X server, and Selkies streaming it (H.264 and Opus
# over one WebSocket) with its web client, into a venv: Selkies and pcmflux
# from PyPI, pixelflux from the stage above. Pinned: Selkies 2.1.0 (the latest
# 2.1.x on 2026-10-22) and the pixelflux and pcmflux versions it requires.
# Selkies' idle-CPU patches (images/common/patches) are applied to the
# installed package, and must apply exactly. Its web client lands in
# /opt/selkies/lib/python3.13/site-packages/selkies/selkies_web, which the
# supervisor extracts from the image.
ARG SELKIES_VERSION=2.1.0 PIXELFLUX_VERSION=2.2.1 PCMFLUX_VERSION=2.2.0
COPY --from=pixelflux /out/opt/pixelflux/ /opt/pixelflux/
RUN --mount=type=bind,from=pixelflux,source=/out/wheels,target=/tmp/pixelflux-wheels \
    --mount=type=bind,source=images/common/patches,target=/tmp/patches \
    apt-get update && apt-get install -y --no-install-recommends \
        xvfb x11-xserver-utils xcvt python3-venv \
    && rm -rf /var/lib/apt/lists/* \
    && python3 -m venv /opt/selkies \
    && /opt/selkies/bin/pip install --no-cache-dir \
        "selkies==${SELKIES_VERSION}" /tmp/pixelflux-wheels/pixelflux-${PIXELFLUX_VERSION}-*.whl \
        "pcmflux==${PCMFLUX_VERSION}" \
    && cd /opt/selkies/lib/python3.13/site-packages \
    && for p in /tmp/patches/selkies-*.diff; do git apply --verbose "$p" || exit 1; done \
    && /opt/selkies/bin/python -m compileall -q selkies \
    && test ! -e pixelflux.libs \
    && grep -q PIXELFLUX_IDLE_KEEPALIVE_MS pixelflux.cpython-*.so \
    && ! ldd pixelflux.cpython-*.so | grep 'not found' \
    && cd / \
    && /opt/selkies/bin/python -c 'import pixelflux, pcmflux' \
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

# Where custom images put their starting home, tool links and extra Flatpak remotes (empty here).
# /opt/deskyard/bin (root-owned) holds links to the executables of tools a
# custom image installs with mise into /opt/mise; it is on PATH after the user's shims.
RUN mkdir -p /opt/deskyard/home-template /opt/deskyard/bin /etc/deskyard/flatpak-remotes \
    && chown 1000:1000 /opt/deskyard/home-template \
    && chmod 755 /opt/deskyard/bin

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
    PATH=/workspace/.local/bin:/workspace/.npm-global/bin:/workspace/.local/share/mise/shims:/opt/deskyard/bin:/opt/runtimes/node/bin:/opt/runtimes/python/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

COPY --from=desktop /desktop /usr/local/bin/deskyard
COPY --from=gamepads /out/ /

VOLUME ["/workspace"]
# The only port: Caddy, which needs the supervisor's per-desktop token
# (WORKSPACE_PROXY_TOKEN). Desktops only run behind the supervisor.
EXPOSE 8080

# The image contract the supervisor checks before using an image.
ARG VERSION=dev
LABEL deskyard.contract="3" \
      deskyard.terminal="tmux-v1" \
      deskyard.kind="desktop" \
      org.opencontainers.image.version="${VERSION}"

# The X server and Selkies. Not Chromium: probing CDP would reopen a browser
# the human closed.
HEALTHCHECK --interval=10s --timeout=5s --start-period=30s --retries=3 \
    CMD /usr/bin/xdpyinfo -display :1 >/dev/null && /usr/bin/curl -fs -o /dev/null http://127.0.0.1:6080/api/health

WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/workspace-init"]

# Desktop layers branch from the same runtime. Keep XFCE last so an ordinary
# docker build without --target retains the original default.
FROM runtime AS debian-kde
RUN apt-get update && apt-get install -y --no-install-recommends \
        plasma-desktop plasma-workspace kwin-x11 konsole dolphin \
        systemsettings kde-config-gtk-style breeze-icon-theme \
    && rm -rf /var/lib/apt/lists/*
ENV WORKSPACE_DESKTOP=kde XDG_CURRENT_DESKTOP=KDE DESKTOP_SESSION=plasma \
    KDE_SESSION_VERSION=6 KDE_FULL_SESSION=true QT_QUICK_BACKEND=software
RUN apt-get update && apt-get install -y --no-install-recommends \
        plasma-discover plasma-discover-backend-flatpak xdg-desktop-portal-kde \
    && rm -rf /var/lib/apt/lists/*
COPY images/common/rootfs/ /
COPY images/debian-kde/rootfs/ /
RUN --mount=type=bind,source=images/common/finalize.sh,target=/tmp/finalize.sh bash /tmp/finalize.sh
LABEL org.opencontainers.image.title="Debian KDE Plasma" \
      org.opencontainers.image.description="Debian trixie with KDE Plasma, Chromium, Flatpak, mise and uv"

FROM runtime AS debian-mate
RUN apt-get update && apt-get install -y --no-install-recommends \
        mate-desktop-environment-core \
    && rm -rf /var/lib/apt/lists/* \
    && ! command -v sudo
ENV WORKSPACE_DESKTOP=mate XDG_CURRENT_DESKTOP=MATE DESKTOP_SESSION=mate
RUN apt-get update && apt-get install -y --no-install-recommends \
        gnome-software-plugin-flatpak gnome-software iso-codes librsvg2-common xdg-desktop-portal-gtk \
        arc-theme papirus-icon-theme fonts-noto-core \
    && rm -rf /var/lib/apt/lists/*
COPY images/common/rootfs/ /
COPY images/debian-mate/rootfs/ /
RUN --mount=type=bind,source=images/common/finalize.sh,target=/tmp/finalize.sh bash /tmp/finalize.sh
RUN glib-compile-schemas /usr/share/glib-2.0/schemas
LABEL org.opencontainers.image.title="Debian MATE" \
      org.opencontainers.image.description="Debian trixie with MATE, Chromium, Flatpak, mise and uv"

FROM runtime AS debian-xfce
RUN apt-get update && apt-get install -y --no-install-recommends xfce4 xfce4-terminal \
    && rm -rf /var/lib/apt/lists/* \
    && ! command -v sudo
ENV WORKSPACE_DESKTOP=xfce XDG_CURRENT_DESKTOP=XFCE DESKTOP_SESSION=xfce
RUN apt-get update && apt-get install -y --no-install-recommends \
        gnome-software-plugin-flatpak gnome-software iso-codes librsvg2-common xdg-desktop-portal-gtk \
    && rm -rf /var/lib/apt/lists/*
COPY images/common/rootfs/ /
COPY images/debian-xfce/rootfs/ /
RUN --mount=type=bind,source=images/common/finalize.sh,target=/tmp/finalize.sh bash /tmp/finalize.sh
LABEL org.opencontainers.image.title="Debian XFCE" \
      org.opencontainers.image.description="Debian trixie with XFCE, Chromium, Flatpak, mise and uv"
