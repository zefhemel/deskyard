"""Fixtures for the desktop image's tests.

AC_IMAGE names the image to test; without it the image is built from this
checkout once per run (before any xdist worker starts). The `sandbox`
fixture takes a desktop from a small pool that boots ahead of the tests, so
each test still gets a fresh desktop on its own home volume without waiting
for it to boot. Tests marked `serial` are left out under xdist and run in a
second pass with --only-serial (see `make test` in the README).
"""

import os
import re
import subprocess
import uuid
from pathlib import Path

import pytest

from sandbox import REPO, docker, start_sandbox

ARTIFACTS = REPO / "artifacts"

# Desktops booting ahead in each test process; AC_POOL=0 turns the pool off.
POOL_SIZE = int(os.environ.get("AC_POOL", "1"))


def pytest_addoption(parser):
    parser.addoption("--only-serial", action="store_true",
                     help="run only the tests marked serial (they're left out under xdist)")


def pytest_configure(config):
    # Once, in the main process: xdist's workers inherit AC_IMAGE.
    if os.environ.get("PYTEST_XDIST_WORKER") or os.environ.get("AC_IMAGE") or config.option.collectonly:
        return
    tag = "deskyard-debian-xfce:test"
    subprocess.run(["docker", "build", "-q", "-t", tag, "-f", str(REPO.parent / "Dockerfile"), "--target", "debian-xfce", str(REPO.parent.parent)], check=True,
                   stdout=subprocess.DEVNULL)
    os.environ["AC_IMAGE"] = tag


def pytest_collection_modifyitems(config, items):
    only = config.getoption("--only-serial")
    parallel = bool(os.environ.get("PYTEST_XDIST_WORKER"))
    keep, drop = [], []
    for item in items:
        serial = item.get_closest_marker("serial") is not None
        (drop if (only and not serial) or (parallel and serial) else keep).append(item)
    if drop:
        config.hook.pytest_deselected(items=drop)
        items[:] = keep


@pytest.fixture(scope="session")
def image() -> str:
    return os.environ.get("AC_IMAGE", "deskyard-debian-xfce:test")


@pytest.fixture(scope="session")
def next_image(image) -> str:
    """A different image tag derived from the test image, standing in for an upgrade."""
    tag = image + "-next"
    docker("build", "-q", "-t", tag, "-", input=f"FROM {image}\nRUN echo next > /etc/workspace/build-id\n")
    return tag


class Volumes:
    def __init__(self):
        suffix = uuid.uuid4().hex[:8]
        self.home = f"ac-test-home-{suffix}"

    def remove(self):
        docker("volume", "rm", "-f", self.home, check=False)


class Pool:
    """Desktops started ahead, each on its own new home volume: docker run
    returns at once, and they boot while other tests run."""

    def __init__(self, image: str, size: int):
        self.image, self.size = image, size
        self.waiting: list = []

    def _start(self):
        v = Volumes()
        self.waiting.append((start_sandbox(self.image, home=v.home, wait=False), v))

    def take(self):
        if not self.waiting:
            self._start()
        sb, v = self.waiting.pop(0)
        while len(self.waiting) < self.size:
            self._start()
        return sb, v

    def close(self):
        for sb, v in self.waiting:
            sb.remove()
            v.remove()


@pytest.fixture(scope="session")
def pool(image, request):
    # Not for the serial pass: a desktop booting beside a timing test is load.
    p = Pool(image, 0 if request.config.getoption("--only-serial") else POOL_SIZE)
    yield p
    p.close()


@pytest.fixture
def volumes():
    v = Volumes()
    yield v
    v.remove()


@pytest.fixture
def make_sandbox(image, volumes):
    """Factory: start sandboxes on the test's volumes; all removed at teardown."""
    started = []

    def make(**kw):
        kw.setdefault("home", volumes.home)
        img = kw.pop("image", image)
        sb = start_sandbox(img, wait=False, **kw)
        started.append(sb)  # registered before waiting, so a failed start is still cleaned up
        sb.wait_ready()
        return sb

    yield make
    for sb in started:
        sb.remove()


@pytest.fixture
def sandbox(pool):
    """A fresh, ready desktop on its own new home volume; removed with it afterwards."""
    sb, v = pool.take()
    try:
        sb.wait_ready()
        yield sb
    finally:
        sb.remove()
        v.remove()


@pytest.fixture
def artifacts_dir(request) -> Path:
    d = ARTIFACTS / re.sub(r"[^\w.-]", "_", request.node.name)
    d.mkdir(parents=True, exist_ok=True)
    return d
