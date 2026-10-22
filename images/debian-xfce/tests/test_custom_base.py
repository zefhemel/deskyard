"""The base image's hooks for custom images: a home template, extra Flatpak
remotes and image-level mise tools. Custom images are built from the base by the
supervisor; these tests build tiny stand-ins with the same layout."""

import tempfile
import uuid
from pathlib import Path

import pytest

from sandbox import docker

REPO_FILE = "[Flatpak Repo]\nTitle=Extra\nUrl=https://example.invalid/repo/\n"


@pytest.fixture
def derive(image):
    """Factory: build `FROM image` plus extra Dockerfile lines and files; removed at teardown."""
    tags = []

    def build(lines: str, files: dict[str, str] | None = None) -> str:
        tag = f"deskyard-test-custom:t{uuid.uuid4().hex[:8]}"
        with tempfile.TemporaryDirectory() as ctx:
            for rel, content in (files or {}).items():
                p = Path(ctx) / rel
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_text(content)
            (Path(ctx) / "Dockerfile").write_text(f"FROM {image}\n{lines}\n")
            tags.append(tag)
            docker("build", "-q", "-t", tag, ctx)
        return tag

    yield build
    for t in tags:
        docker("rmi", "-f", t, check=False)


def test_template_seeds_new_home(make_sandbox, derive):
    img = derive("COPY --chown=1000:1000 tpl/ /opt/deskyard/home-template/", {"tpl/.config/x/y": "from-template\n"})
    sb = make_sandbox(image=img)
    assert sb.exec("cat /workspace/.config/x/y").stdout == "from-template\n"
    assert sb.exec("stat -c %u:%g /workspace/.config/x/y").stdout.strip() == "1000:1000"


def test_template_keeps_existing_files(make_sandbox, derive, image, volumes):
    img = derive("COPY --chown=1000:1000 tpl/ /opt/deskyard/home-template/",
                 {"tpl/.bashrc": "B\n", "tpl/other": "other\n"})
    docker("run", "--rm", "--entrypoint", "sh", "-v", f"{volumes.home}:/workspace", image,
           "-c", "echo A > /workspace/.bashrc")
    sb = make_sandbox(image=img)
    assert sb.exec("cat ~/.bashrc").stdout == "A\n"
    assert sb.exec("cat ~/other").stdout == "other\n"


def test_extra_remote_added_for_user(make_sandbox, derive):
    img = derive("COPY extra.flatpakrepo /etc/deskyard/flatpak-remotes/extra.flatpakrepo",
                 {"extra.flatpakrepo": REPO_FILE})
    sb = make_sandbox(image=img)
    remotes = sb.exec("flatpak remotes --user").stdout
    assert "extra" in remotes and "flathub" in remotes


def test_remote_with_bad_name_is_skipped(make_sandbox, derive):
    img = derive("COPY [\"extra.flatpakrepo\", \"/etc/deskyard/flatpak-remotes/bad name.flatpakrepo\"]\n"
                 "COPY extra.flatpakrepo /etc/deskyard/flatpak-remotes/good.flatpakrepo",
                 {"extra.flatpakrepo": REPO_FILE})
    sb = make_sandbox(image=img)
    remotes = sb.exec("flatpak remotes --user").stdout
    assert "good" in remotes and "bad" not in remotes
    assert "bad name" in sb.logs()


def test_image_mise_on_path_after_user(make_sandbox, derive):
    img = derive("RUN mkdir -p /opt/mise/shims && printf '#!/bin/sh\\necho image\\n' > /opt/mise/shims/imgtool"
                 " && printf '#!/bin/sh\\necho image\\n' > /opt/mise/shims/both && chmod +x /opt/mise/shims/*")
    sb = make_sandbox(image=img)
    sb.exec("mkdir -p ~/.local/share/mise/shims && printf '#!/bin/sh\\necho user\\n' > ~/.local/share/mise/shims/both"
            " && chmod +x ~/.local/share/mise/shims/both")
    for login in (True, False):
        assert sb.exec("imgtool", login=login).stdout.strip() == "image"
        assert sb.exec("both", login=login).stdout.strip() == "user"
