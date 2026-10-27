# Build, test and release the supervisor and the deskyard command.
#
#   make build         web UI + both binaries into bin/
#   make test          Go unit tests, with the race detector
#   make test-engine   the tests that need a running Docker engine
#                      (builds the desktop image unless CC_TEST_IMAGE names one)
# (All of Deskyard's tests: the Makefile one folder up.)
#   make linux         the supervisor for the engine's Linux architecture,
#                      into bin/deskyard-supervisor-linux
#   make dev           make linux, then (re)start a container from the base
#                      image with that binary mounted over the image's:
#                      /data is DEV_DATA, on 127.0.0.1:DEV_PORT. The same
#                      command after every edit; the image is rebuilt only
#                      when the Dockerfile changes. See also compose.yaml.
#   make release VERSION=v1.2.3   cross-compiled deskyard binaries into dist/
#   make image         the supervisor's container image
#
# The supervisor runs only in a container, with any host folder mounted at
# /data. The deskyard command is the only binary that is released.

VERSION ?= dev
IMAGE   ?= deskyard:$(VERSION)

GO       ?= go
LDFLAGS  := -s -w -X main.version=$(VERSION)
GOFLAGS_RELEASE := -trimpath -ldflags "$(LDFLAGS)"

# The deskyard command, for agents on Linux, macOS and Windows.
DESKYARD_PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

# Development: the container's data folder, port and name. Override them to
# run a second instance (a scratch folder, another port and name).
DEV_DATA ?= $(abspath ../test-instance)
DEV_PORT ?= 7343
DEV_NAME ?= deskyard-dev

# The Docker engine's architecture, which is the desktop of the container.
ENGINE_ARCH := $(shell docker info -f '{{.Architecture}}' 2>/dev/null | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')

.PHONY: all web build linux dev test test-engine typecheck vet release image clean

all: build

# The UI is built into internal/web/dist, which the supervisor embeds.
web:
	cd web && npm ci && npm run build

build: web
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o bin/deskyard-supervisor ./cmd/deskyard-supervisor
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o bin/deskyard ./cmd/deskyard

# The supervisor for the Docker engine's Linux, to run in a container. The
# UI is rebuilt only when its sources are newer than the last build.
WEB_SOURCES := $(shell find web/src web/index.html web/package.json web/package-lock.json web/vite.config.* -type f 2>/dev/null)
internal/web/dist/index.html: $(WEB_SOURCES)
	$(MAKE) web

linux: internal/web/dist/index.html
	@test -n "$(ENGINE_ARCH)" || { echo "cannot read the engine's architecture: is Docker running?"; exit 1; }
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ENGINE_ARCH) $(GO) build -o bin/deskyard-supervisor-linux ./cmd/deskyard-supervisor

dev: linux
	docker build -q --target base -t deskyard:base .
	-docker rm -f $(DEV_NAME)
	docker run -d --name $(DEV_NAME) \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v $(DEV_DATA):/data \
		-v $(CURDIR)/bin/deskyard-supervisor-linux:/usr/local/bin/deskyard-supervisor:ro \
		-p 127.0.0.1:$(DEV_PORT):7343 deskyard:base

test:
	$(GO) test -race ./...

# The engine tests run desktops of CC_TEST_IMAGE. Without it, the desktop
# image is built from this checkout first (as TEST_IMAGE), so it has the
# contract this supervisor checks.
TEST_IMAGE ?= deskyard-debian-xfce:test
ENGINE_PACKAGES := ./internal/environments/ ./internal/engine/... ./internal/storage/
test-engine:
ifdef CC_TEST_IMAGE
	$(GO) test -tags engine $(ENGINE_PACKAGES)
else
	docker build -t $(TEST_IMAGE) -f ../images/Dockerfile ..
	CC_TEST_IMAGE=$(TEST_IMAGE) $(GO) test -tags engine $(ENGINE_PACKAGES)
endif

vet:
	$(GO) vet ./...

typecheck:
	cd web && npm run typecheck && npm test

release: web
	rm -rf dist
	mkdir -p dist
	@set -e; for p in $(DESKYARD_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "deskyard $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build $(GOFLAGS_RELEASE) \
			-o dist/deskyard-$$os-$$arch$$ext ./cmd/deskyard; \
	done
	cd dist && shasum -a 256 * > SHA256SUMS

image:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

clean:
	rm -rf bin dist
