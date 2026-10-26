# Deskyard's tests. Which to run when: README.md, "Tests".
#
#   make test            the fast default, about 7 minutes: Go (race) and
#                        engine tests, the web UI's, and both pytest suites
#                        without what's slow, needs the internet, builds
#                        images, measures, or is WebKit
#   make test-all        everything, in both supervisor modes (about 25 min)
#   make test-ci         what CI runs on a pull request: test, plus WebKit
#
#   make test-go test-web test-engine test-image test-supervisor test-container
#                        one part each; PYTEST_ARGS="-k drop" narrows the
#                        pytest suites, MARKS="-m ''" takes every marker
#
# The desktop image the suites test is built from this checkout as TEST_IMAGE
# (BUILD_IMAGE=0: it's already there, as in CI).
# Desktops get /28s from DESKYARD_TEST_SUBNET, split between the supervisor
# suite's workers: give each checkout testing at once its own.

TEST_IMAGE ?= deskyard-debian-xfce:test
DESKYARD_TEST_SUBNET ?= 10.215.0.0/20
WORKERS ?= 4
BUILD_IMAGE ?= 1
DEFAULT_MARKS := not slow and not network and not build and not perf and not webkit
MARKS ?= -m '$(DEFAULT_MARKS)'
PYTEST_ARGS ?=

export AC_IMAGE := $(TEST_IMAGE)
export CC_TEST_IMAGE := $(TEST_IMAGE)
export DESKYARD_TEST_SUBNET

IMAGE_TESTS := images/tests
SUPERVISOR_TESTS := supervisor/tests
# A parallel pass, then the timing-sensitive tests (marked serial) alone.
# (pytest exits 5 when a pass selects nothing.)
define two_passes
	cd $(1) && uv run pytest -n $(WORKERS) $(2) $(MARKS) $(PYTEST_ARGS) || [ $$? -eq 5 ]
	cd $(1) && uv run pytest --only-serial $(MARKS) $(PYTEST_ARGS) || [ $$? -eq 5 ]
endef

.PHONY: test test-all test-ci test-go test-web test-engine test-image test-supervisor test-container test-image-build

test: test-go test-web test-image-build test-engine test-image test-supervisor

test-all:
	$(MAKE) test MARKS="-m ''"
	$(MAKE) test-container MARKS="-m ''"

test-ci:
	$(MAKE) test MARKS="-m 'not slow and not network and not build and not perf'"

test-go:
	cd supervisor && go vet ./... && go test -race ./...

test-web:
	cd supervisor/web && { [ -d node_modules ] || npm ci; } && npm run typecheck && npm test

test-image-build:
	[ "$(BUILD_IMAGE)" = 0 ] || docker build -q -t $(TEST_IMAGE) --target debian-xfce -f images/Dockerfile .

test-engine: test-image-build
	cd supervisor && go test -tags engine ./internal/environments/ ./internal/engine/...

test-image: test-image-build
	$(call two_passes,$(IMAGE_TESTS),)

# Each worker runs its own supervisor, so tests spread one by one.
test-supervisor: test-image-build
	$(call two_passes,$(SUPERVISOR_TESTS),)

test-container: test-image-build
	DESKYARD_TEST_MODE=container $(MAKE) test-supervisor

