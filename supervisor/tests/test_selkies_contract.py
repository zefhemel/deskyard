"""Explicit client boundary, exercised only for images advertising v1 hooks."""
import pytest
from playwright.sync_api import sync_playwright
from helpers import SCREEN, open_desktop, signed_in


def test_v1_client_keeps_browser_primitives(supervisor, desktop, name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        page.context.add_init_script("""window.__browserPrimitives = {
          eventListener: EventTarget.prototype.addEventListener,
          focus: HTMLElement.prototype.focus,
          connect: AudioNode.prototype.connect,
          socket: window.WebSocket,
          workerPost: Worker.prototype.postMessage,
          audioContext: window.AudioContext,
          capture: navigator.mediaDevices?.getUserMedia,
          secure: window.isSecureContext,
        };""")
        open_desktop(page, supervisor.base, name)
        screen = page.frames[1]
        if not screen.evaluate("!!document.querySelector('meta[name=deskyard-client-contract]')"):
            pytest.skip('legacy image: no explicit client hook contract')
        assert screen.evaluate("window.__deskyardHooks.version") == 1
        assert screen.evaluate("""() => {
          const old = window.__browserPrimitives;
          return old.eventListener === EventTarget.prototype.addEventListener &&
            old.focus === HTMLElement.prototype.focus &&
            old.connect === AudioNode.prototype.connect &&
            old.socket === window.WebSocket && old.workerPost === Worker.prototype.postMessage &&
            old.audioContext === window.AudioContext && old.capture === navigator.mediaDevices?.getUserMedia &&
            old.secure === window.isSecureContext;
        }""")
        # The supervisor's per-tab stream identity is supplied at URL creation.
        assert screen.evaluate("new URL(window.__deskyardHooks.url('ws://example/api/websockets')).searchParams.get('tab') === window.deskyardScreen.tab")
        page.wait_for_function(f"{SCREEN}.state() === 'connected'")
        browser.close()
