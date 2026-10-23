"""The Audio panel: the desktop's sound per viewer, viewers' mics into the
desktop with everyone seeing whose is on, and camera badges."""

import array
import math
import re
import struct
import subprocess
import time
import uuid
import wave

import pytest
from playwright.sync_api import expect, sync_playwright

from conftest import docker
from helpers import FAKE_MEDIA, MEDIA, SCREEN, audio_switch, in_desktop, open_desktop, sign_in, signed_in, wait_until


def tone_file(path, freq, seconds=60, rate=48000):
    """A WAV of beeps at freq: a quarter second on, a quarter off, so the
    browser's noise suppression keeps it."""
    frames = bytearray()
    for i in range(seconds * rate):
        on = (i // (rate // 4)) % 2 == 0
        v = int(12000 * math.sin(2 * math.pi * freq * i / rate)) if on else 0
        frames += struct.pack("<h", v)
    with wave.open(str(path), "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(bytes(frames))
    return str(path)


def mic_browser(p, tone):
    """Chromium whose mic hears the tone file, and grants the mic without asking."""
    return p.chromium.launch(args=[*FAKE_MEDIA, f"--use-file-for-fake-audio-capture={tone}"])


def record_mic(name, seconds=3, rate=24000) -> list[float]:
    """What an app on the desktop hears from its default microphone."""
    r = subprocess.run(["docker", "exec", "-u", "user", f"deskyard-{name}", "bash", "-lc",
                        f"timeout {seconds} parecord --raw --format=s16le --rate={rate} --channels=1 -d @DEFAULT_SOURCE@; true"],
                       capture_output=True, check=True)
    samples = array.array("h", r.stdout[: len(r.stdout) // 2 * 2])
    return [v / 32768 for v in samples]


def tone_level(samples, freq, rate=24000) -> float:
    """The amplitude of freq in samples (Goertzel)."""
    n = len(samples)
    if n == 0:
        return 0.0
    c = 2 * math.cos(2 * math.pi * round(n * freq / rate) / n)
    s1 = s2 = 0.0
    for x in samples:
        s1, s2 = x + c * s1 - s2, s1
    return 2 * math.sqrt(max(0.0, s1 * s1 + s2 * s2 - c * s1 * s2)) / n


def hears(name, *freqs, absent=()) -> bool:
    samples = record_mic(name)
    levels = {f: tone_level(samples, f) for f in (*freqs, *absent)}
    ok = all(levels[f] > 0.005 for f in freqs) and all(levels[f] < 0.001 for f in absent)
    if not ok:
        print("mic levels", levels)
    return ok


def mic_badge(page, user):
    return page.locator(f'[data-avatar="{user}"] [data-mic-badge]')


@pytest.mark.serial  # seconds of quiet, timed
def test_sound_toggle(supervisor, desktop, name):
    # A tone plays on the desktop. Sound is off on a first visit; turned on,
    # the page plays the desktop's sound; it's remembered, with the volume.
    play_tone(name)
    with sync_playwright() as p:
        browser = p.chromium.launch(args=["--autoplay-policy=no-user-gesture-required"])
        page = sign_in(browser, supervisor.base)
        open_desktop(page, supervisor.base, name)
        sound = audio_switch(page, "Desktop sound")
        expect(sound).to_have_attribute("aria-checked", "false")
        time.sleep(3)
        assert quiet(page) > 2.5  # nothing comes while it's off

        sound.click()
        expect(sound).to_have_attribute("aria-checked", "true")
        page.wait_for_function(f"{MEDIA}.sound && {MEDIA}.playing", timeout=15000)
        assert wait_until(lambda: quiet(page) < 1.5, 10), quiet(page)
        page.get_by_role("slider", name="Volume").fill("0.3")
        assert page.evaluate("JSON.parse(localStorage.getItem('deskyard.sound')).volume") == 0.3

        # Remembered: on again after a reload.
        page.reload()
        open_desktop(page, supervisor.base, name)
        page.wait_for_function(f"{MEDIA}.sound && {MEDIA}.playing", timeout=15000)
        sound = audio_switch(page, "Desktop sound")
        expect(sound).to_have_attribute("aria-checked", "true")
        expect(page.get_by_role("slider", name="Volume")).to_have_value("0.3")

        # Off: the desktop stops sending this page its sound.
        sound.click()
        page.wait_for_function(f"!{MEDIA}.sound", timeout=10000)
        time.sleep(4)
        assert quiet(page) > 2.5, quiet(page)
        browser.close()
    in_desktop(name, "pkill", "paplay", check=False)


def play_tone(name):
    """A tone on the desktop's speaker for two minutes."""
    in_desktop(name, "bash", "-lc", "python3 -c \"import math,struct,sys; sys.stdout.buffer.write(b''.join("
               "struct.pack('<h', int(8000*math.sin(2*math.pi*440*i/48000))) for i in range(48000*120)))\" > /tmp/tone.raw")
    subprocess.Popen(["docker", "exec", "-u", "user", f"deskyard-{name}", "bash", "-lc",
                      "paplay --raw --format=s16le --rate=48000 --channels=1 /tmp/tone.raw"])


def quiet(page) -> float:
    """How long ago the stream last brought the page sound, in seconds."""
    return page.evaluate(f"(performance.now() - {MEDIA}.soundAt) / 1000")


@pytest.mark.serial  # seconds of quiet, timed
def test_sound_per_viewer(supervisor, desktop, name):
    # Sound is each viewer's own: the display's owner and a page beside it
    # turn theirs on and off without touching the other's.
    play_tone(name)
    with sync_playwright() as p:
        browser = p.chromium.launch(args=["--autoplay-policy=no-user-gesture-required"])
        a = sign_in(browser, supervisor.base)
        open_desktop(a, supervisor.base, name)
        a.wait_for_function(f"{SCREEN}.display().owner === true", timeout=15000)
        b = sign_in(browser, supervisor.base)
        open_desktop(b, supervisor.base, name)
        b.wait_for_function(f"{SCREEN}.display().owner === false", timeout=15000)

        audio_switch(a, "Desktop sound").click()
        assert wait_until(lambda: quiet(a) < 1.5, 10), quiet(a)
        time.sleep(3)
        assert quiet(b) > 2.5, quiet(b)

        audio_switch(b, "Desktop sound").click()
        assert wait_until(lambda: quiet(b) < 1.5, 10), quiet(b)
        audio_switch(a, "Desktop sound").click()
        a.wait_for_function(f"!{MEDIA}.sound", timeout=10000)
        time.sleep(4)
        assert quiet(a) > 2.5, quiet(a)
        assert quiet(b) < 1.5, quiet(b)
        browser.close()
    in_desktop(name, "pkill", "paplay", check=False)


@pytest.mark.serial  # seconds of quiet, timed
def test_joiner_keeps_owner_sound(supervisor, desktop, name):
    # Pages joining beside the owner with their sound off (first visits)
    # don't turn the owner's sound off.
    play_tone(name)
    with sync_playwright() as p:
        browser = p.chromium.launch(args=["--autoplay-policy=no-user-gesture-required"])
        a = sign_in(browser, supervisor.base)
        open_desktop(a, supervisor.base, name)
        a.wait_for_function(f"{SCREEN}.display().owner === true", timeout=15000)
        audio_switch(a, "Desktop sound").click()
        assert wait_until(lambda: quiet(a) < 1.5, 10), quiet(a)
        for _ in range(2):
            b = sign_in(browser, supervisor.base)
            open_desktop(b, supervisor.base, name)
            time.sleep(4)
            assert quiet(a) < 1.5 and a.evaluate(f"{MEDIA}.sound"), quiet(a)
            assert quiet(b) > 2.5, quiet(b)
            b.context.close()
            time.sleep(2)
        browser.close()
    in_desktop(name, "pkill", "paplay", check=False)


def test_mic_badge_and_reload(supervisor, admin, desktop, name, tmp_path):
    # The mic goes into the desktop, where apps hear it, and everyone sees
    # the badge. After a reload it's off, and the badge is gone.
    tone = tone_file(tmp_path / "tone.wav", 700)
    with sync_playwright() as p:
        browser = mic_browser(p, tone)
        page = sign_in(browser, supervisor.base)
        open_desktop(page, supervisor.base, name)
        wall = sign_in(browser, supervisor.base)
        expect(wall.get_by_text(name, exact=True).first).to_be_visible(timeout=15000)

        mic = audio_switch(page, "My mic")
        expect(mic).to_have_attribute("aria-checked", "false")
        expect(page.get_by_text(f"Everyone on {name} sees when your mic is on")).to_be_visible()
        expect(mic_badge(page, "admin")).to_have_count(0)
        mic.click()
        expect(mic).to_have_attribute("aria-checked", "true")
        page.wait_for_function(f"{MEDIA}.mic", timeout=15000)
        expect(mic_badge(page, "admin")).to_be_visible(timeout=10000)
        expect(mic_badge(wall, "admin")).to_be_visible(timeout=10000)
        assert admin.desktop(name)["mics"] == ["admin"]
        assert wait_until(lambda: hears(name, 700), 20, 1)
        # The meter moves, and the bar's Audio button says the mic is live.
        assert wait_until(lambda: page.evaluate(f"{SCREEN}.micLevel()") > 0.01, 10, 0.1)
        expect(page.get_by_role("button", name="Audio", exact=True)).to_have_attribute("title", re.compile("mic is on"))

        page.reload()
        open_desktop(page, supervisor.base, name)
        expect(mic_badge(page, "admin")).to_have_count(0, timeout=10000)
        expect(mic_badge(wall, "admin")).to_have_count(0, timeout=10000)
        assert admin.desktop(name)["mics"] == []
        expect(audio_switch(page, "My mic")).to_have_attribute("aria-checked", "false")
        time.sleep(2)
        assert page.evaluate(f"{MEDIA}.mic") is False
        assert admin.desktop(name)["mics"] == []
        browser.close()


def test_mic_quick_off_on_keeps_badge(supervisor, admin, desktop, name, tmp_path):
    # Off and straight back on (a double click): the mic ends up live, and
    # so does its badge. The badge always matches the live mic.
    tone = tone_file(tmp_path / "t.wav", 700)
    with sync_playwright() as p:
        browser = mic_browser(p, tone)
        page = sign_in(browser, supervisor.base)
        open_desktop(page, supervisor.base, name)
        mic = audio_switch(page, "My mic")
        mic.click()
        page.wait_for_function(f"{MEDIA}.mic", timeout=15000)
        expect(mic_badge(page, "admin")).to_be_visible(timeout=10000)
        outcomes = []
        for delay in (0, 50, 150):
            mic.click()
            page.wait_for_timeout(delay)
            mic.click()
            time.sleep(4)
            outcomes.append((delay, page.evaluate(f"{MEDIA}.mic"), admin.desktop(name)["mics"], mic.get_attribute("aria-checked")))
        assert all(live and mics == ["admin"] and on == "true" for _, live, mics, on in outcomes), outcomes
        # And on and straight back off: off, with no badge.
        mic.click()
        page.wait_for_function(f"!{MEDIA}.mic", timeout=10000)
        expect(mic_badge(page, "admin")).to_have_count(0, timeout=10000)
        mic.click()
        mic.click()
        time.sleep(4)
        assert page.evaluate(f"{MEDIA}.mic") is False
        assert admin.desktop(name)["mics"] == []
        browser.close()


# Watches, in the page every 30 ms, that a mic live before and after a look
# at the supervisor has its badge there.
BADGE_WATCH = """(name) => {
  window.__viol = []; window.__n = 0;
  const live = () => document.querySelector('iframe')?.contentWindow?.deskyardScreen?.media?.().mic === true;
  window.__w = setInterval(async () => {
    const before = live();
    let d;
    try { d = await (await fetch('/api/desktops/' + name)).json(); } catch { return; }
    window.__n++;
    if (before && live() && !d.mics.includes('admin')) window.__viol.push(d.mics);
  }, 30);
}"""


def test_mic_badge_on_a_slow_network(supervisor, admin, desktop, name, tmp_path):
    # Badge reports take 400 ms. Off, then on-off-on within one round trip:
    # the mic never goes live while the supervisor says it's off.
    with sync_playwright() as p:
        browser = mic_browser(p, tone_file(tmp_path / "t.wav", 700))
        page = sign_in(browser, supervisor.base)
        open_desktop(page, supervisor.base, name)
        mic = audio_switch(page, "My mic")
        mic.click()
        page.wait_for_function(f"{MEDIA}.mic", timeout=15000)
        time.sleep(1)
        page.evaluate("""() => { const f = window.fetch; window.fetch = async (u, o) => {
            if (/\\/(mic|cam)$/.test(String(u))) await new Promise((r) => setTimeout(r, 400)); return f(u, o); }; }""")
        page.evaluate(BADGE_WATCH, name)
        mic.click()
        page.wait_for_function(f"!{MEDIA}.mic", timeout=5000)
        page.wait_for_timeout(50)
        mic.click()
        mic.click()
        mic.click()
        time.sleep(3)
        violations, checks = page.evaluate("[window.__viol, window.__n]")
        assert checks > 30 and violations == [], (violations, checks)
        assert page.evaluate(f"{MEDIA}.mic") is True and admin.desktop(name)["mics"] == ["admin"]
        browser.close()


def test_cam_badge(supervisor, admin, desktop, name):
    # Whose camera is on shows like the mic, from the page's report on its
    # stream. (Numbered past the page's own reports, which come first.)
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        open_desktop(page, supervisor.base, name)
        tab = page.evaluate(f"{SCREEN}.tab")
        r = page.evaluate("""async (tab) => (await fetch(location.pathname.replace(/\\/$/, '').replace(/^\\//, '/api/desktops/') + '/cam',
            {method: 'POST', body: JSON.stringify({on: true, tab, seq: 1e9})})).status""", tab)
        assert r == 204
        expect(page.locator('[data-avatar="admin"] [data-cam-badge]')).to_be_visible(timeout=10000)
        assert admin.desktop(name)["cams"] == ["admin"]
        page.reload()
        open_desktop(page, supervisor.base, name)
        expect(page.locator('[data-avatar="admin"] [data-cam-badge]')).to_have_count(0, timeout=10000)
        assert admin.desktop(name)["cams"] == []
        browser.close()


def test_two_mics(supervisor, admin, desktop, name, tmp_path):
    # Two viewers' mics are mixed into the desktop's one. One turning theirs
    # off, or leaving, never takes the other's away.
    user = "u" + uuid.uuid4().hex[:5]
    r = admin.post("/api/accounts", json={"username": user, "password": "pw-" + user, "admin": False, "desktops": [name]})
    assert r.status_code == 201, r.text
    try:
        with sync_playwright() as p:
            ba = mic_browser(p, tone_file(tmp_path / "a.wav", 500))
            bb = mic_browser(p, tone_file(tmp_path / "b.wav", 1300))
            a = sign_in(ba, supervisor.base)
            open_desktop(a, supervisor.base, name)
            b = sign_in(bb, supervisor.base, user, "pw-" + user)
            open_desktop(b, supervisor.base, name)

            audio_switch(a, "My mic").click()
            a.wait_for_function(f"{MEDIA}.mic", timeout=15000)
            audio_switch(b, "My mic").click()
            b.wait_for_function(f"{MEDIA}.mic", timeout=15000)
            for page in (a, b):
                expect(mic_badge(page, "admin")).to_be_visible(timeout=10000)
                expect(mic_badge(page, user)).to_be_visible(timeout=10000)
            assert admin.desktop(name)["mics"] == sorted(["admin", user])
            assert wait_until(lambda: hears(name, 500, 1300), 20, 1)

            # One turns theirs off: the other's badge and sound stay.
            audio_switch(a, "My mic").click()
            expect(audio_switch(a, "My mic")).to_have_attribute("aria-checked", "false")
            for page in (a, b):
                expect(mic_badge(page, "admin")).to_have_count(0, timeout=10000)
                expect(mic_badge(page, user)).to_be_visible()
            assert admin.desktop(name)["mics"] == [user]
            assert b.evaluate(f"{MEDIA}.mic") is True
            assert wait_until(lambda: hears(name, 1300, absent=[500]), 20, 1)

            # The first viewer leaves: the other's mic still reaches the desktop.
            ba.close()
            assert wait_until(lambda: admin.desktop(name)["viewers"] == [user])
            time.sleep(2)
            assert admin.desktop(name)["mics"] == [user]
            assert b.evaluate(f"{MEDIA}.mic") is True
            if not wait_until(lambda: hears(name, 1300), 20, 1):
                # (A desktop whose sound server came up before PipeWire did this: fixed in the image.)
                state = docker("exec", "-u", "user", f"deskyard-{name}", "bash", "-lc",
                               "pactl info | grep Default; pactl list short sinks; pactl list short sources; pactl list short sink-inputs").stdout
                raise AssertionError(f"B's mic no longer reaches the desktop; B: {b.evaluate(f'JSON.stringify({MEDIA})')}\n{state}")
            bb.close()
    finally:
        admin.delete(f"/api/accounts/{user}")
