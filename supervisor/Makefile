# Build, test and release the supervisor and the deskyard command.
#
#   make build         web UI + both binaries into bin/
#   make test          Go unit tests, with the race detector
#   make test-engine   the tests that need a running Docker engine
#                      (builds the desktop image unless CC_TEST_IMAGE names one)
# (All of Deskyard's tests: the Makefile one folder up.)
#   make release VERSION=v1.2.3   cross-compiled binaries into dist/
#   make image         the supervisor's container image

VERSION ?= dev
IMAGE   ?= deskyard:$(VERSION)

GO       ?= go
LDFLAGS  := -s -w -X main.version=$(VERSION)
GOFLAGS_RELEASE := -trimpath -ldflags "$(LDFLAGS)"

# supervisor: Linux and macOS. deskyard: also Windows, for agents there.
SUPERVISOR_PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
DESKYARD_PLATFORMS   := $(SUPERVISOR_PLATFORMS) windows/amd64 windows/arm64

.PHONY: all web build test test-engine typecheck vet release image clean

all: build

# The UI is built into internal/web/dist, which the supervisor embeds.
web:
	cd web && npm ci && npm run build

build: web
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o bin/deskyard-supervisor ./cmd/deskyard-supervisor
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o bin/deskyard ./cmd/deskyard

test:
	$(GO) test -race ./...

# The engine tests run desktops of CC_TEST_IMAGE. Without it, the desktop
# image is built from this checkout first (as TEST_IMAGE), so it has the
# contract this supervisor checks.
TEST_IMAGE ?= deskyard-debian-xfce:test
ENGINE_PACKAGES := ./internal/desktops/ ./internal/engine/...
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
	@set -e; for p in $(SUPERVISOR_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "supervisor $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build $(GOFLAGS_RELEASE) \
			-o dist/deskyard-supervisor-$$os-$$arch ./cmd/deskyard-supervisor; \
	done
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
