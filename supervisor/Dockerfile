# syntax=docker/dockerfile:1
# The supervisor's container image. Build context: this folder (supervisor/).
#   docker build -t deskyard .
# Run it with any host folder mounted at /data and the engine socket:
#   docker run -d -v /var/run/docker.sock:/var/run/docker.sock \
#     -v /srv/deskyard:/data -p 127.0.0.1:7343:7343 deskyard
# The binary starts as root only to fix the data folder's ownership once, then
# drops to UID 1000 with the socket's group, so there is no USER line. For
# development, compose.yaml and `make dev` build only the "base" stage and
# mount a cross-compiled binary over the one the final stage adds.

# 1. The web UI, built into internal/web/dist (embedded by the Go build).
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# 2. The supervisor binary (static, cross-compiled for the target platform).
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/web/dist ./internal/web/dist
ARG TARGETOS TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/deskyard-supervisor ./cmd/deskyard-supervisor

# 3. The base: everything but the binary, so development can mount its own.
# CA certificates serve registry pulls and ACME; tzdata, local times.
FROM alpine:3.22 AS base
RUN apk add --no-cache ca-certificates tzdata
ARG VERSION=dev
LABEL deskyard.kind="supervisor" \
      org.opencontainers.image.title="Deskyard" \
      org.opencontainers.image.description="Creates, runs and shares desktops" \
      org.opencontainers.image.version="${VERSION}"
# The data folder is always /data: mount any host folder there. The
# supervisor reads the host path from its own container.
ENV DESKYARD_IN_CONTAINER=1 \
    DESKYARD_LISTEN=0.0.0.0:7343
EXPOSE 7343
ENTRYPOINT ["/usr/local/bin/deskyard-supervisor"]

# 4. The image: the base plus the binary.
FROM base
COPY --from=build /out/deskyard-supervisor /usr/local/bin/deskyard-supervisor
