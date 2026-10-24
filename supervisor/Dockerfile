# syntax=docker/dockerfile:1
# The supervisor's container image. Build context: this folder (supervisor/).
#   docker build -t deskyard .
# Run it with compose.yaml next to this file.

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

# 3. The image: the binary, CA certificates (registry pulls, ACME) and tzdata.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/deskyard-supervisor /usr/local/bin/deskyard-supervisor
ARG VERSION=dev
LABEL deskyard.kind="supervisor" \
      org.opencontainers.image.title="Deskyard" \
      org.opencontainers.image.description="Creates, runs and shares desktops" \
      org.opencontainers.image.version="${VERSION}"
# DESKYARD_DATA_DIR has no default here: mount the data folder at the
# same path as on the host and set it to that path (see compose.yaml).
ENV DESKYARD_IN_CONTAINER=1 \
    DESKYARD_LISTEN=0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/deskyard-supervisor"]
