import io

from PIL import Image
from playwright.sync_api import sync_playwright

from sandbox import TOKEN_HEADER, wait_until
# (Caddy's token: test_selkies.test_everything_behind_token.)


def open_desktop(p, web_url: str, width=1280, height=800, errors=None):
    """Selkies' own page, straight from Caddy (the supervisor serves its own copy)."""
    browser = p.chromium.launch(args=["--mute-audio"])
    ctx = browser.new_context(extra_http_headers=TOKEN_HEADER, viewport={"width": width, "height": height})
    page = ctx.new_page()
    page.add_init_script("""window.__nativePrimitives = {
        socket: window.WebSocket, audio: window.AudioContext,
        events: EventTarget.prototype.addEventListener,
        focus: HTMLElement.prototype.focus, connect: AudioNode.prototype.connect,
        capture: navigator.mediaDevices?.getUserMedia, secure: window.isSecureContext,
    };""")
    if errors is not None:
        page.on("pageerror", lambda error: errors.append(str(error)))
    page.goto(web_url)
    page.wait_for_function(
        "() => [...document.querySelectorAll('video')].some(v => v.videoWidth > 100)"
        " || [...document.querySelectorAll('canvas')].some(c => c.width > 100"
        " && getComputedStyle(c).display !== 'none')", timeout=30_000)
    return browser, page


def distinct_colours(png: bytes) -> int:
    img = Image.open(io.BytesIO(png)).convert("RGB").resize((320, 200))
    return len(img.getcolors(maxcolors=320 * 200))


def test_desktop_renders(sandbox, artifacts_dir):
    with sync_playwright() as p:
        browser, page = open_desktop(p, sandbox.web_url)
        # The picture fills in as the stream's first frames arrive.
        shots = []

        def colourful():
            shots.append(page.screenshot())
            return distinct_colours(shots[-1]) > 50

        assert wait_until(colourful, 15, 1), distinct_colours(shots[-1])
        (artifacts_dir / "desktop.png").write_bytes(shots[-1])
        browser.close()


def test_desktop_follows_browser_window(sandbox):
    size = lambda: sandbox.exec("deskyard size").stdout.split()  # noqa: E731
    with sync_playwright() as p:
        errors = []
        browser, page = open_desktop(p, sandbox.web_url, width=1000, height=640, errors=errors)
        assert not errors, errors
        assert wait_until(lambda: size() == ["1000", "640"], 10), size()
        page.set_viewport_size({"width": 1400, "height": 900})
        assert wait_until(lambda: size() == ["1400", "900"], 10), size()
        browser.close()


def test_bare_client_contract_preserves_browser_primitives(sandbox):
    with sync_playwright() as p:
        errors = []
        browser, page = open_desktop(p, sandbox.web_url, errors=errors)
        assert not errors, errors
        assert page.evaluate("""() => {
            const h = window.__deskyardHooks, n = window.__nativePrimitives;
            return h?.version === 1 && h.allowInsecure === false && h.pointerMoveOnly === false &&
                n.socket === window.WebSocket && n.audio === window.AudioContext &&
                n.events === EventTarget.prototype.addEventListener && n.focus === HTMLElement.prototype.focus &&
                n.connect === AudioNode.prototype.connect && n.capture === navigator.mediaDevices?.getUserMedia &&
                n.secure === window.isSecureContext;
        }""")
        browser.close()
