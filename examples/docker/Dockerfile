# Build from this directory; use a Deskyard base containing Docker access support.
ARG BASE_IMAGE=ghcr.io/zefhemel/deskyard-debian-xfce:latest
FROM ${BASE_IMAGE}
USER root
LABEL org.opencontainers.image.title="Deskyard Docker tools example" \
      org.opencontainers.image.description="Docker CLI and Buildx; daemon access is an explicit desktop setting"

# Client tools only. The supervisor supplies the host socket when an admin
# enables Docker daemon access; no daemon is started inside this desktop.
RUN apt-get update \
    && apt-get install -y --no-install-recommends docker-cli docker-buildx \
    && rm -rf /var/lib/apt/lists/*
COPY --chown=1000:1000 demo/ /opt/deskyard/home-template/docker-demo/
COPY Dockerfile README.md /usr/share/doc/deskyard-docker/
# Preserve the base entrypoint, services and Deskyard contract labels.
