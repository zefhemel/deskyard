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


def test_template_dangling_dotfile_links_start(make_sandbox, derive):
    # Stow-style dotfiles whose targets weren't copied: the desktop still
    # starts, the links are kept, and nothing is written through them (the
    # .profile link points where only root could write).
    img = derive("COPY --chown=1000:1000 tpl/ /opt/deskyard/home-template/\n"
                 "RUN ln -s dotfiles/bash/.bashrc /opt/deskyard/home-template/.bashrc"
                 " && ln -s /etc/deskyard-probe /opt/deskyard/home-template/.profile"
                 " && chown -h 1000:1000 /opt/deskyard/home-template/.bashrc /opt/deskyard/home-template/.profile",
                 {"tpl/other": "other\n"})
    sb = make_sandbox(image=img)
    assert sb.exec("readlink ~/.bashrc").stdout.strip() == "dotfiles/bash/.bashrc"
    assert sb.exec("readlink ~/.profile").stdout.strip() == "/etc/deskyard-probe"
    assert sb.exec("test -e /etc/deskyard-probe", check=False).returncode != 0
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


def test_base_node_resolves_to_runtimes_not_mise(sandbox):
    for login in (True, False):
        assert sandbox.exec("which node", login=login).stdout.strip() == "/opt/runtimes/node/bin/node"
    assert sandbox.exec("stat -c %U:%a /opt/deskyard/bin").stdout.strip() == "root:755"


@pytest.mark.network
def test_image_tool_on_path_after_user(make_sandbox, derive):
    # Install a real tool into /opt/mise and link it the way the recipe build does.
    img = derive("RUN MISE_YES=1 MISE_DATA_DIR=/opt/mise MISE_CACHE_DIR=/tmp/mc mise install npm:cowsay@1.6.0"
                 " && rm -rf /tmp/mc"
                 " && for d in $(MISE_DATA_DIR=/opt/mise mise bin-paths npm:cowsay@1.6.0); do"
                 " for f in $d/*; do ln -s \"$f\" /opt/deskyard/bin/; done; done")
    sb = make_sandbox(image=img)
    for login in (True, False):
        assert sb.exec("which cowsay", login=login).stdout.strip() == "/opt/deskyard/bin/cowsay"
        assert "hi" in sb.exec("cowsay hi", login=login).stdout
    # The user's own shim of the same name wins.
    sb.exec("mkdir -p ~/.local/share/mise/shims && printf '#!/bin/sh\\necho user\\n' > ~/.local/share/mise/shims/cowsay"
            " && chmod +x ~/.local/share/mise/shims/cowsay")
    for login in (True, False):
        assert sb.exec("cowsay", login=login).stdout.strip() == "user"
