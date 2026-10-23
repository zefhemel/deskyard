"""Sound and picture in sync through Selkies, measured in a real browser.

The A/V test page (/opt/workspace/avsync in the image) plays full-screen in
the desktop: once a second a white flash and a beep, together, on whole
seconds of the desktop's clock (?epoch=1). The desktop page's ?avsync=1
readout (web/src/desktop/avsync.ts) pairs each flash it sees in Selkies'
picture with the beep it hears, as each reaches the screen and the speakers.

The target (the audio design's): sound at most 45 ms early and 125 ms late,
as the median over 30 beeps, on a clean network and with 60 ± 20 ms of delay
each way. Lag assumes the desktop's clock is the browser's (the same machine).
"""

import statistics
import time

import pytest
from playwright.sync_api import sync_playwright

from delayproxy import DelayProxy
from helpers import MEDIA, SCREEN, audio_switch, in_desktop, screen_size, sign_in, wait_until

pytestmark = [pytest.mark.perf, pytest.mark.serial]

# Sound plays without a click and comes out of no speaker: Chromium's fake
# output, so the numbers don't depend on this machine's audio device.
ARGS = ["--autoplay-policy=no-user-gesture-required", "--disable-audio-output"]
EARLY, LATE = -45, 125
BEEPS = 30
# Beeps left out at the start: congestion control raises the bitrate
# gradually after connecting.
WARMUP = 5


def start_test_page(name):
    """The A/V test page full-screen in the desktop's Chromium, started with a click."""
    in_desktop(name, "bash", "-lc",
               "setsid /usr/bin/chromium --user-data-dir=/tmp/avsync-profile --no-first-run --no-default-browser-check "
               "--password-store=basic --no-sandbox --test-type --kiosk --autoplay-policy=no-user-gesture-required "
               "'file:///opt/workspace/avsync/index.html?epoch=1' >/dev/null 2>&1 &")
    assert wait_until(lambda: in_desktop(name, "xdotool", "search", "--name", "A/V sync test", check=False).strip(), 30), \
        "the test page didn't open"
    time.sleep(2)  # full-screen
    w, h = screen_size(name)
    in_desktop(name, "xdotool", "mousemove", str(w // 2), str(h // 2), "click", "1")


def measure(base, name) -> dict:
    with sync_playwright() as p:
        browser = p.chromium.launch(args=ARGS)
        page = sign_in(browser, base)
        page.goto(f"{base}/{name}/?avsync=1")
        page.wait_for_function(f"{SCREEN}?.state() === 'connected' && {SCREEN}.video() !== null", timeout=30000)
        audio_switch(page, "Desktop sound").click()
        page.wait_for_function(f"{MEDIA}.sound && {MEDIA}.playing", timeout=15000)
        page.keyboard.press("Escape")
        start_test_page(name)
        want = WARMUP + BEEPS
        page.wait_for_function(f"(window.__avsync?.offsets.length ?? 0) >= {want}", timeout=(want + 40) * 1000)
        d = page.evaluate("window.__avsync")
        browser.close()
    return {k: v[WARMUP:want] for k, v in d.items()}


def report(label, d) -> float:
    off, lag, vlag = d["offsets"], d["lags"], d["vlags"]
    med = statistics.median(off)
    print(f"\n{label}: {len(off)} beeps; ms as median / min / max"
          f"\n  sync offset (sound − picture) {med:+.0f} / {min(off):+.0f} / {max(off):+.0f}"
          f"\n  sound lag   {statistics.median(lag):.0f} / {min(lag):.0f} / {max(lag):.0f}"
          f"\n  picture lag {statistics.median(vlag):.0f} / {min(vlag):.0f} / {max(vlag):.0f}"
          f"\n  offsets {[round(x) for x in off]}")
    return med


def test_sync_clean(supervisor, desktop, name):
    med = report("clean network", measure(supervisor.base, name))
    assert EARLY <= med <= LATE, med


def test_sync_with_delay(supervisor, desktop, name):
    with DelayProxy("127.0.0.1", supervisor.port, delay_ms=60, jitter_ms=20) as proxy:
        med = report("60 ± 20 ms each way", measure(proxy.base, name))
    assert EARLY <= med <= LATE, med
