# Build from this directory: docker build -t deskyard-retroarch:example .
# Override BASE_IMAGE with a locally built Deskyard image or a registry digest.
ARG BASE_IMAGE=ghcr.io/zefhemel/deskyard-debian-xfce:latest

FROM debian:trixie-slim AS cores
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential ca-certificates curl zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*
COPY cores.tsv build-cores.sh /build/
RUN sh /build/build-cores.sh

FROM ${BASE_IMAGE}
USER root
LABEL org.opencontainers.image.title="RetroArch retro consoles example" \
      org.opencontainers.image.description="RetroArch with five source-built cores; no games or BIOS files"

# Flatpak's real binary installs at system scope. The desktop wrapper defaults
# to per-user installs, which belong to a workspace rather than an image.
COPY install-retroarch.sh /tmp/install-retroarch.sh
RUN sh /tmp/install-retroarch.sh && rm /tmp/install-retroarch.sh
# Keep inventory separate so a reporting error cannot discard the install layer.
RUN mkdir -p /usr/share/doc/deskyard-retroarch \
    && /usr/bin/flatpak.real list --system --columns=ref \
        | while IFS= read -r ref; do \
            printf '%s\t' "$ref"; \
            /usr/bin/flatpak.real info --system --show-commit "$ref" || exit 1; \
        done > /usr/share/doc/deskyard-retroarch/flatpak-commits.txt

# The base entrypoint copies this template into an unseeded workspace as UID
# 1000. Nothing is installed into /workspace during the Docker build.
COPY --chown=1000:1000 home-template/ /opt/deskyard/home-template/
COPY --from=cores --chown=1000:1000 /out/cores/ /opt/deskyard/home-template/.var/app/org.libretro.RetroArch/config/retroarch/cores/
COPY --from=cores /out/sources/ /usr/share/doc/deskyard-retroarch/sources/
COPY Dockerfile build-cores.sh install-retroarch.sh cores.tsv README.md NOTICES.md /usr/share/doc/deskyard-retroarch/
RUN chmod 755 /opt/deskyard/home-template/.local/bin/retroarch-deskyard \
    /opt/deskyard/home-template/Desktop/RetroArch.desktop \
    && printf '\ncore_updater_buildbot_cores_url = "https://buildbot.libretro.com/nightly/linux/%s/latest/"\n' \
        "$(/usr/bin/flatpak.real --default-arch)" \
        >> /opt/deskyard/home-template/.var/app/org.libretro.RetroArch/config/retroarch/retroarch.cfg

# Keep the base's ENTRYPOINT, CMD, root startup and Deskyard contract labels.
