import re

import pytest
import yaml

from sandbox import REPO

SHIPPED = REPO.parent / "common" / "rootfs" / "opt" / "workspace" / "skills"
SKILLS = ["workspace-computer", "workspace-browser", "workspace-computer-use"]
AGENT_DIRS = ["~/.claude/skills", "~/.codex/skills", "~/.agents/skills", "~/.gemini/skills"]


def frontmatter(path):
    m = re.match(r"^---\n(.*?)\n---\n", path.read_text(), re.S)
    assert m, f"{path} has no frontmatter"
    return yaml.safe_load(m.group(1))


def python_block(path) -> str:
    m = re.search(r"```python\n(.*?)```", path.read_text(), re.S)
    assert m, f"{path} has no python example"
    return m.group(1)


def test_shipped_skill_set():
    assert sorted(p.name for p in SHIPPED.iterdir()) == sorted(SKILLS)


@pytest.mark.parametrize("path", sorted(SHIPPED.glob("*/SKILL.md")),
                         ids=lambda p: p.parent.name)
def test_frontmatter_valid(path):
    fm = frontmatter(path)
    assert fm["name"] == path.parent.name
    assert fm["description"].startswith("Use when")


def test_skill_links_present(sandbox):
    for d in AGENT_DIRS:
        for s in SKILLS:
            target = sandbox.exec(f"readlink -f {d}/{s}").stdout.strip()
            assert target == f"/opt/workspace/skills/{s}", (d, s, target)
            sandbox.exec(f"test -r {d}/{s}/SKILL.md")


def test_user_changes_kept_across_restart(sandbox):
    """A skill the user replaced, and a Codex config of their own, stay theirs."""
    sandbox.exec("rm ~/.claude/skills/workspace-browser && mkdir ~/.claude/skills/workspace-browser"
                 " && echo custom > ~/.claude/skills/workspace-browser/SKILL.md")
    sandbox.exec("echo 'model = \"mine\"' > ~/.codex/config.toml")
    sandbox.restart()
    sandbox.exec("test ! -L ~/.claude/skills/workspace-browser")
    assert sandbox.exec("cat ~/.claude/skills/workspace-browser/SKILL.md").stdout.strip() == "custom"
    assert sandbox.exec("cat ~/.codex/config.toml").stdout.strip() == 'model = "mine"'


@pytest.mark.network
def test_browser_skill_example_runs(sandbox):
    code = python_block(SHIPPED / "workspace-browser" / "SKILL.md")
    sandbox.exec(f"cat > /tmp/example.py <<'PYEOF'\n{code}PYEOF")
    out = sandbox.exec("uv run /tmp/example.py", timeout=600).stdout
    assert "Example Domain" in out


def test_codex_config_seeded_with_network_access(sandbox):
    cfg = sandbox.exec("cat ~/.codex/config.toml").stdout
    assert 'sandbox_mode = "danger-full-access"' in cfg
