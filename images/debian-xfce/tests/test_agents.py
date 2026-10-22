import pytest


@pytest.mark.slow
@pytest.mark.network
def test_claude_code_and_codex_install(make_sandbox):
    a = make_sandbox()
    a.exec("curl -fsSL https://claude.ai/install.sh | bash", timeout=900)
    a.exec("mise use -g node@22 && npm i -g @openai/codex", timeout=900)
    a.exec("claude --version")
    a.exec("codex --version")
    a.remove()
    b = make_sandbox()
    assert b.exec("claude --version").stdout.strip()
    assert b.exec("codex --version").stdout.strip()
