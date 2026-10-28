"""Retina density, runtime overrides and shared display ownership."""
import pytest
from playwright.sync_api import sync_playwright
from helpers import SCREEN, PICTURE, signed_in, in_desktop, wait_until, screen_size, mouse_location, to_page


def emulate_dpr(page, ratio):
    """Emulates a display density without depending on the test runner's display.
    Setting window.testDpr changes it; a resize event then tells the page."""
    page.add_init_script(f"Object.defineProperty(window, 'devicePixelRatio', {{get: () => window.testDpr ?? {ratio}}})")


def open_page(context_or_page, base, name, ratio, owner):
    """A desktop page at ratio, once it is (owner) or isn't the display's owner."""
    page = context_or_page.new_page() if hasattr(context_or_page, "new_page") else context_or_page
    emulate_dpr(page, ratio)
    page.goto(f"{base}/{name}/")
    page.wait_for_function(f"{SCREEN}?.display().owner === {'true' if owner else 'false'}")
    return page


def setting(name, prop):
    return in_desktop(name, "bash", "-lc", f"xfconf-query -c xsettings -p {prop}").strip()


def scale_factor(name):
    return setting(name, "/Gdk/WindowScalingFactor")


def requested_size(page):
    """The size the page asks the display for, in remote pixels (its frame's data-display-size)."""
    value = page.locator("iframe").first.get_attribute("data-display-size")
    return tuple(int(n) for n in value.split("x")) if value else None


def follows_request(name, page):
    """The desktop's screen reaches the size the page asks for."""
    return wait_until(lambda: requested_size(page) is not None and screen_size(name) == requested_size(page))


def panel_height(name):
    area = in_desktop(name, "xprop", "-root", "_NET_WORKAREA").split("=", 1)[1]
    return screen_size(name)[1] - int(area.split(",")[3])


def choose(page, item):
    page.get_by_role("button", name="Display", exact=True).click()
    page.get_by_role("menuitemcheckbox", name=item, exact=True).click()


def pointer_lands(page, name, x=600, y=400):
    """A pointer over the picture at remote (x, y) lands there on the desktop."""
    page.wait_for_function(f"{SCREEN}?.video() !== null")
    page.mouse.move(*to_page(page.evaluate(PICTURE), x, y))
    return wait_until(lambda: all(abs(a - b) <= 4 for a, b in zip(mouse_location(name), (x, y))))


@pytest.fixture
def owner(supervisor, desktop, name):
    """A Retina (2×) browser's page owning the desktop's display."""
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        yield open_page(page, supervisor.base, name, 2, owner=True)
        browser.close()


def test_retina_owner_streams_at_twice_the_frame(owner, name):
    assert wait_until(lambda: scale_factor(name) == "2")
    assert follows_request(name, owner), (screen_size(name), requested_size(owner))
    frame_width = owner.locator("iframe").first.evaluate("f => f.clientWidth")
    assert abs(requested_size(owner)[0] - 2 * frame_width) <= 2, (requested_size(owner), frame_width)
    assert setting(name, "/Xft/DPI") == "192"
    assert setting(name, "/Gdk/UnscaledDPI") == "98304"
    assert pointer_lands(owner, name)


def test_locked_resolution_is_in_logical_pixels(owner, name):
    choose(owner, "Resolution: 1280 × 800")
    assert wait_until(lambda: screen_size(name) == (2560, 1600))
    assert wait_until(lambda: 80 <= panel_height(name) <= 84)
    # Locked: the window's size no longer matters.
    owner.set_viewport_size({"width": 1000, "height": 700})
    owner.wait_for_timeout(400)
    assert screen_size(name) == (2560, 1600)
    choose(owner, "Resolution: Auto fit")
    assert follows_request(name, owner)
    assert requested_size(owner) != (2560, 1600)


def test_viewer_at_another_density_follows_the_owner(owner, name, supervisor):
    assert wait_until(lambda: scale_factor(name) == "2")
    second = open_page(owner.context, supervisor.base, name, 1, owner=False)
    assert scale_factor(name) == "2"
    assert pointer_lands(second, name)
    second.close()


def test_scaling_override_applies_and_is_remembered(owner, name):
    choose(owner, "Scaling: 1×")
    assert wait_until(lambda: scale_factor(name) == "1")
    assert follows_request(name, owner)
    assert setting(name, "/Xft/DPI") == "96"
    owner.reload()
    owner.wait_for_function(f"{SCREEN}?.display().owner === true")
    assert follows_request(name, owner)
    assert scale_factor(name) == "1"


def test_auto_scaling_follows_the_pixel_ratio(owner, name):
    choose(owner, "Scaling: Auto (2×)")
    assert wait_until(lambda: scale_factor(name) == "2")
    owner.evaluate("window.testDpr = 1; window.dispatchEvent(new Event('resize'))")
    assert wait_until(lambda: scale_factor(name) == "1")
    assert follows_request(name, owner)
    owner.evaluate("window.testDpr = 2; window.dispatchEvent(new Event('resize'))")
    assert wait_until(lambda: scale_factor(name) == "2")
    assert follows_request(name, owner)


def test_successor_owner_applies_its_own_density(owner, name, supervisor):
    assert wait_until(lambda: scale_factor(name) == "2")
    successor = open_page(owner.context, supervisor.base, name, 1, owner=False)
    owner.close()
    successor.wait_for_function(f"{SCREEN}?.display().owner === true")
    assert wait_until(lambda: scale_factor(name) == "1")
    assert wait_until(lambda: 40 <= panel_height(name) <= 42)
    assert follows_request(name, successor)
