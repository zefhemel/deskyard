# Build, test and release the supervisor and the deskyard command.
#
#   make build         web UI, desktop CLIs and the deskyard command into bin/
#   make test          Go unit tests, with the race detector
#   make test-engine   the tests that need a running Docker engine
#                      (builds the desktop image unless CC_TEST_IMAGE names one)
# (All of Deskyard's tests: the Makefile one folder up.)
#   make linux         the supervisor for the engine's Linux architecture,
#                      into bin/deskyard-supervisor-linux, plus both desktop CLIs
#                      (OUT= another file, GOARCH= another architecture)
#   make cli-linux     just the desktop CLIs in bin/desktop-cli/linux-{amd64,arm64}/
#   make base          the supervisor's base image, deskyard:base (no binary)
#   make run           (re)start a supervisor container from deskyard:base with
#                      BIN mounted over the image's binary: NAME, /data is
#                      DATA, on 127.0.0.1:PORT (RUN_ARGS: more docker run
#                      options; ARGS: the binary's arguments)
#   make dev           make linux base run, by default as DEV_NAME with
#                      DEV_DATA on DEV_PORT. The same command after every
#                      edit; the image is rebuilt only when the Dockerfile
#                      changes. See also compose.yaml.
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

.PHONY: all web build linux engine-goarch cli-linux base run dev test test-engine typecheck vet release image clean

all: build

# The UI is built into internal/web/dist, which the supervisor embeds.
web:
	cd web && npm ci && npm run build

# The supervisor runs only in a container: make linux builds it for one.
build: web cli-linux
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o bin/deskyard ./cmd/deskyard

# The supervisor for the Docker engine's Linux, to run in a container. The
# UI is rebuilt only when its sources are newer than the last build.
WEB_SOURCES := $(shell find web/src web/index.html web/package.json web/package-lock.json web/vite.config.* -type f 2>/dev/null)
internal/web/dist/index.html: $(WEB_SOURCES)
	$(MAKE) web

# Both desktop architectures ship with the supervisor. Write to a temporary
# file and rename so mounted build directories never expose a partial build.
cli-linux:
	@set -e; for arch in amd64 arm64; do \
		dir=bin/desktop-cli/linux-$$arch; mkdir -p $$dir; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch $(GO) build $(GOFLAGS_RELEASE) \
			-o $$dir/deskyard.next ./cmd/deskyard; \
		mv $$dir/deskyard.next $$dir/deskyard; \
	done

# The Docker engine's architecture as a GOARCH, on stdout, asked only when a
# target needs it.
ENGINE_GOARCH = arch=$$(docker info -f '{{.Architecture}}' 2>/dev/null) && test -n "$$arch" || \
		{ echo "cannot read the engine's architecture: is Docker running?" >&2; exit 1; }; \
	case $$arch in \
		(x86_64|amd64) echo amd64 ;; \
		(aarch64|arm64) echo arm64 ;; \
		(*) echo "the engine's architecture $$arch is not supported: Deskyard runs on amd64 and arm64" >&2; exit 1 ;; \
	esac

engine-goarch:
	@$(ENGINE_GOARCH)

# The supervisor for Linux: the engine's architecture unless GOARCH names one.
OUT = bin/deskyard-supervisor-linux
linux: internal/web/dist/index.html cli-linux
	@set -e; goarch="$(GOARCH)"; [ -n "$$goarch" ] || goarch=$$($(ENGINE_GOARCH)); \
	echo "CGO_ENABLED=0 GOOS=linux GOARCH=$$goarch $(GO) build -o $(OUT) ./cmd/deskyard-supervisor"; \
	CGO_ENABLED=0 GOOS=linux GOARCH=$$goarch $(GO) build -o $(OUT) ./cmd/deskyard-supervisor

base:
	docker build -q --target base -t deskyard:base .

# One way to run a supervisor from this checkout, for make dev and both
# Python suites: strict mounts, so a missing binary is an error rather than
# a folder made in its place. compose.yaml mounts the same.
NAME = $(DEV_NAME)
DATA = $(DEV_DATA)
PORT = $(DEV_PORT)
BIN  = bin/deskyard-supervisor-linux
RUN_ARGS =
ARGS =
run:
	mkdir -p $(DATA)
	-@docker rm -f $(NAME) >/dev/null 2>&1
	docker run -d --name $(NAME) \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v $(abspath $(DATA)):/data \
		--mount type=bind,src=$(abspath $(BIN)),dst=/usr/local/bin/deskyard-supervisor,readonly \
		--mount type=bind,src=$(CURDIR)/bin/desktop-cli,dst=/opt/deskyard/cli,readonly \
		-p 127.0.0.1:$(PORT):7343 $(RUN_ARGS) deskyard:base $(ARGS)

dev: linux base run

test:
	$(GO) test -race ./...

# The engine tests run desktops of CC_TEST_IMAGE. Without it, the desktop
# image is built from this checkout first (as TEST_IMAGE), so it has the
# contract this supervisor checks.
TEST_IMAGE ?= deskyard-debian-xfce:test
# Every package with engine-tagged tests (grep -rl '//go:build engine' internal).
ENGINE_PACKAGES := ./internal/environments/ ./internal/engine/... ./internal/storage/
test-engine: cli-linux
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
