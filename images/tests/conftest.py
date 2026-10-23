import os
import re
import subprocess
import uuid
from pathlib import Path

import pytest

from sandbox import REPO, docker, start_sandbox

ARTIFACTS = REPO / "artifacts" / os.environ.get("AC_DESKTOP", "xfce")


@pytest.fixture(scope="session")
def image() -> str:
    if os.environ.get("AC_IMAGE"):
        return os.environ["AC_IMAGE"]
    desktop = os.environ.get("AC_DESKTOP", "xfce")
    if desktop not in {"xfce", "kde", "mate"}:
        raise ValueError("AC_DESKTOP must be xfce, kde or mate")
    tag = f"deskyard-debian-{desktop}:test"
    subprocess.run(["docker", "build", "-q", "-t", tag, "-f", str(REPO.parent / "Dockerfile"), "--target", f"debian-{desktop}", str(REPO.parent.parent)], check=True,
                   stdout=subprocess.DEVNULL)
    return tag


@pytest.fixture(scope="session")
def next_image(image) -> str:
    """A different image tag derived from the test image, standing in for an upgrade."""
    tag = f"deskyard-debian-{os.environ.get('AC_DESKTOP', 'xfce')}:test-next"
    docker("build", "-q", "-t", tag, "-", input=f"FROM {image}\nRUN echo next > /etc/workspace/build-id\n")
    return tag


class Volumes:
    def __init__(self):
        suffix = uuid.uuid4().hex[:8]
        self.home = f"ac-test-home-{suffix}"

    def remove(self):
        docker("volume", "rm", "-f", self.home, check=False)


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
def sandbox(make_sandbox):
    return make_sandbox()


@pytest.fixture
def artifacts_dir(request) -> Path:
    d = ARTIFACTS / re.sub(r"[^\w.-]", "_", request.node.name)
    d.mkdir(parents=True, exist_ok=True)
    return d
