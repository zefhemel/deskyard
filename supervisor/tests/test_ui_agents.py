"""What the desktop page shows of agents: port mappings, who's there and what
they did, and their marks on the picture."""

import subprocess
import time

from playwright.sync_api import expect, sync_playwright

from conftest import docker, free_port
from helpers import PICTURE, SCREEN, SHOWING, open_desktop, signed_in, to_page


def test_forwards_shown(cli, remote, forwards, supervisor, name):
    p = free_port()
    f = forwards(cli, remote, str(p))
    f.wait_for(f"in {p} {p}")
    with sync_playwright() as pw:
        browser, page = signed_in(pw, supervisor.base)
        page.goto(f"{supervisor.base}/{name}/")
        chip = page.get_by_role("button", name="Viewers and port mappings")
        expect(chip).to_be_visible(timeout=30000)
        chip.click()
        expect(page.get_by_text(f"cli test: {p} in")).to_be_visible()

        page.goto(f"{supervisor.base}/environments/{name}")
        expect(page.get_by_text(f"cli test: {p} in")).to_be_visible(timeout=15000)

        page.goto(f"{supervisor.base}/{name}/")
        expect(chip).to_be_visible(timeout=30000)
        assert f.stop() == 0
        # The button stays for the viewers (this page is one); the mapping count goes.
        expect(chip.get_by_text("⇄")).to_have_count(0, timeout=3000)
        browser.close()


def test_agent_presence_and_summary(cli, remote, supervisor, name):
    remote("-e", name, "click", "100", "100")
    remote("-e", name, "exec", "--", "true")
    # An agent stays present for 12 s after its last action here
    # (DESKYARD_ACTIVITY_PRESENCE), which a slow run can outlast before the
    # hover: the row then leaves the popover under the pointer. A running
    # command keeps the agent present for the whole test.
    busy = subprocess.Popen([cli, "-e", name, "exec", "--", "sleep", "600"], env=remote.env,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        check_agent_presence_and_summary(supervisor.base, name)
    finally:
        busy.terminate()
        busy.wait(10)


def check_agent_presence_and_summary(base, name):
    title = "cli test · admin's CLI"
    with sync_playwright() as pw:
        browser, page = signed_in(pw, base)
        page.goto(f"{base}/{name}/")
        chip = page.get_by_role("button", name="Viewers and port mappings")
        expect(chip).to_be_visible(timeout=30000)
        expect(chip.locator("[data-agent]")).to_have_attribute("title", title)
        chip.click()
        expect(page.get_by_role("heading", name="CLI activity")).to_be_visible()
        row = page.get_by_role("button", name=title)
        expect(row).to_be_visible()
        row.hover()
        expect(page.get_by_text("Recently")).to_be_visible()
        expect(page.get_by_text("clicked", exact=True)).to_be_visible()
        expect(page.get_by_text("true · exit 0")).to_be_visible()

        page.goto(f"{base}/environments/{name}")
        expect(page.locator("[data-agent]").first).to_be_visible(timeout=15000)

        page.goto(f"{base}/")
        expect(page.locator("[data-agent]").first).to_be_visible(timeout=15000)
        browser.close()


def test_open_popover_stays_open_when_the_stream_connects(cli, remote, supervisor, name):
    # When the stream connects, Selkies' client focuses its text input and the
    # page gives the screen focus. Either blurred the window, and the bar's
    # popovers close on blur: one opened before the stream connected closed by
    # itself (as test_agent_presence_and_summary's hover found, now and then).
    remote("-e", name, "exec", "--", "true")  # an agent, so the popover has a row before anyone views
    busy = subprocess.Popen([cli, "-e", name, "exec", "--", "sleep", "600"], env=remote.env,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    docker("exec", f"deskyard-{name}", "supervisorctl", "stop", "selkies")
    try:
        with sync_playwright() as pw:
            browser, page = signed_in(pw, supervisor.base)
            page.goto(f"{supervisor.base}/{name}/")
            chip = page.get_by_role("button", name="Viewers and port mappings")
            expect(chip).to_be_visible(timeout=30000)
            chip.click()
            agents = page.get_by_role("heading", name="CLI activity")
            expect(agents).to_be_visible()
            expect(page.get_by_text("Connected", exact=True)).to_be_hidden()
            docker("exec", f"deskyard-{name}", "supervisorctl", "start", "selkies")
            expect(page.get_by_text("Connected", exact=True)).to_be_visible(timeout=30000)
            page.wait_for_function(SHOWING, timeout=30000)
            time.sleep(0.5)
            expect(agents).to_be_visible()
            browser.close()
    finally:
        docker("exec", f"deskyard-{name}", "supervisorctl", "start", "selkies", check=False)
        busy.terminate()
        busy.wait(10)


def test_overlay_marks(cli, remote, supervisor, name):
    with sync_playwright() as pw:
        browser, page = signed_in(pw, supervisor.base)
        open_desktop(page, supervisor.base, name)
        time.sleep(1)  # the screen-events stream is open

        remote("-e", name, "click", "200", "150")
        click = page.locator('[data-mark="click"]')
        expect(click).to_be_visible(timeout=5000)
        expect(click).to_contain_text("cli test")
        box = click.bounding_box()
        want = to_page(page.evaluate(PICTURE), 200, 150)
        assert abs(box["x"] + box["width"] / 2 - want[0]) <= 3, (box, want)
        assert abs(box["y"] + box["height"] / 2 - want[1]) <= 3, (box, want)

        # Locked and scaled down, the marks still land on the spot.
        page.get_by_role("button", name="Lock size", exact=True).click()
        page.set_viewport_size({"width": 800, "height": 700})
        page.wait_for_function(f"(() => {{ const f = document.querySelector('iframe'); "
                               f"return {SCREEN}.video().getBoundingClientRect().width <= f.clientWidth + 1; }})()")
        time.sleep(1)
        expect(page.locator("[data-mark]")).to_have_count(0, timeout=5000)
        remote("-e", name, "click", "600", "400")
        expect(click).to_be_visible(timeout=5000)
        box = click.bounding_box()
        pic = page.evaluate(PICTURE)
        assert pic["width"] < pic["remoteWidth"] - 50, pic
        want = to_page(pic, 600, 400)
        assert abs(box["x"] + box["width"] / 2 - want[0]) <= 3, (box, want)
        assert abs(box["y"] + box["height"] / 2 - want[1]) <= 3, (box, want)

        remote("-e", name, "type", "secret-text")
        typing = page.locator('[data-mark="typing"]')
        expect(typing).to_have_text("cli test is typing…", timeout=5000)
        expect(typing).to_have_count(0, timeout=3000)

        remote("-e", name, "key", "ctrl+l")
        expect(page.locator('[data-mark="key"]')).to_have_text("⌃L", timeout=5000)

        marks = page.locator("[data-mark]")
        expect(marks).to_have_count(0, timeout=5000)
        remote("-e", name, "move", "10", "10")
        time.sleep(1.5)
        expect(marks).to_have_count(0)

        assert "secret-text" not in page.content()
        browser.close()
