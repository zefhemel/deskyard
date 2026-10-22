# Build, test and release the supervisor and the deskyard command.
#
#   make build         web UI, image scanner + both binaries into bin/
#   make scanner       the image scanner, embedded by the supervisor
#   make test          Go unit tests
#   make test-engine   also the tests that need a running Docker engine
#   make release VERSION=v1.2.3   cross-compiled binaries into dist/
#   make image         the supervisor's container image

VERSION ?= dev
IMAGE   ?= deskyard:$(VERSION)

GO       ?= go
LDFLAGS  := -s -w -X main.version=$(VERSION)
GOFLAGS_RELEASE := -trimpath -ldflags "$(LDFLAGS)"

# supervisor: Linux and macOS. desktop: also Windows, for agents there.
DESKYARD_PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
DESKYARD_PLATFORMS   := $(DESKYARD_PLATFORMS) windows/amd64

.PHONY: all web scanner build test test-engine typecheck vet release image clean

all: build

# The UI is built into internal/web/dist, which the supervisor embeds.
web:
	cd web && npm ci && npm run build

# The image scanner runs inside containers of the images the supervisor
# builds: static Linux binaries in internal/scanbin/bin, embedded by the Go build.
SCANNER_ARCHES := amd64 arm64
scanner:
	@set -e; for arch in $(SCANNER_ARCHES); do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch $(GO) build -trimpath -ldflags "-s -w" \
			-o internal/scanbin/bin/deskyard-image-scan-linux-$$arch ./cmd/deskyard-image-scan; \
	done

build: web scanner
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o bin/deskyard-supervisor ./cmd/deskyard-supervisor
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o bin/deskyard ./cmd/deskyard

test:
	$(GO) test ./...

test-engine:
	$(GO) test -tags engine ./...

vet:
	$(GO) vet ./...

typecheck:
	cd web && npm run typecheck

release: web scanner
	rm -rf dist
	mkdir -p dist
	@set -e; for p in $(DESKYARD_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "supervisor $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build $(GOFLAGS_RELEASE) \
			-o dist/deskyard-supervisor-$$os-$$arch ./cmd/deskyard-supervisor; \
	done
	@set -e; for p in $(DESKYARD_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "desktop $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build $(GOFLAGS_RELEASE) \
			-o dist/deskyard-$$os-$$arch$$ext ./cmd/deskyard; \
	done
	cd dist && shasum -a 256 * > SHA256SUMS

image:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

clean:
	rm -rf bin dist internal/scanbin/bin/deskyard-image-scan-*
