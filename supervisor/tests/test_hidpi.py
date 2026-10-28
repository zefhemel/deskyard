"""Retina density, runtime overrides and shared display ownership."""
from playwright.sync_api import sync_playwright
from helpers import SCREEN, PICTURE, signed_in, in_desktop, wait_until, screen_size, mouse_location, to_page


def test_hidpi_follows_owner(supervisor, desktop, name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        # Emulate Retina without depending on the physical test runner's display.
        page.add_init_script("Object.defineProperty(window, 'devicePixelRatio', {get: () => window.testDpr ?? 2})")
        page.goto(f"{supervisor.base}/{name}/")
        page.wait_for_function(f"{SCREEN}?.display().owner === true")

        def setting(prop):
            return in_desktop(name, "bash", "-lc", f"xfconf-query -c xsettings -p {prop}").strip()

        def expected_size(scale):
            return page.locator('iframe').first.evaluate("""(frame, scale) => {
              const fit = Math.min(scale, 3840/frame.clientWidth, 2160/frame.clientHeight);
              return [Math.floor(frame.clientWidth*fit/2)*2, Math.floor(frame.clientHeight*fit/2)*2];
            }""", scale)

        assert wait_until(lambda: setting('/Gdk/WindowScalingFactor') == '2')
        assert wait_until(lambda: list(screen_size(name)) == expected_size(2)), (screen_size(name), expected_size(2))
        assert setting('/Xft/DPI') == '192'
        assert setting('/Gdk/UnscaledDPI') == '98304'

        page.wait_for_function(f"{SCREEN}?.video() !== null")
        pic = page.evaluate(PICTURE)
        page.mouse.move(*to_page(pic, 600, 400))
        assert wait_until(lambda: all(abs(a-b) <= 4 for a,b in zip(mouse_location(name), (600, 400))))

        page.get_by_role('button', name='Display', exact=True).click()
        page.get_by_role('menuitemcheckbox', name='Resolution: 1280 × 800', exact=True).click()
        assert wait_until(lambda: screen_size(name) == (2560, 1600))
        def panel_height():
            area = in_desktop(name, "xprop", "-root", "_NET_WORKAREA").split("=", 1)[1]
            return screen_size(name)[1] - int(area.split(",")[3])
        assert wait_until(lambda: 80 <= panel_height() <= 84)
        page.set_viewport_size({"width": 1000, "height": 700})
        page.wait_for_timeout(400)
        assert screen_size(name) == (2560, 1600)
        page.get_by_role('button', name='Display', exact=True).click()
        page.get_by_role('menuitemcheckbox', name='Resolution: Auto fit', exact=True).click()
        assert wait_until(lambda: list(screen_size(name)) == expected_size(2))

        # A viewer on a different display follows the owner's stream.
        second = page.context.new_page()
        second.add_init_script("Object.defineProperty(window, 'devicePixelRatio', {get: () => 1})")
        second.goto(f"{supervisor.base}/{name}/")
        second.wait_for_function(f"{SCREEN}?.display().owner === false")
        assert setting('/Gdk/WindowScalingFactor') == '2'
        second.wait_for_function(f"{SCREEN}?.video() !== null")
        pic = second.evaluate(PICTURE)
        second.mouse.move(*to_page(pic, 600, 400))
        assert wait_until(lambda: all(abs(a-b) <= 4 for a,b in zip(mouse_location(name), (600, 400))))

        page.get_by_role('button', name='Display', exact=True).click()
        page.get_by_role('menuitemcheckbox', name='Scaling: 1×', exact=True).click()
        assert wait_until(lambda: setting('/Gdk/WindowScalingFactor') == '1')
        assert wait_until(lambda: list(screen_size(name)) == expected_size(1))
        assert setting('/Xft/DPI') == '96'

        second.close()
        page.reload()
        page.wait_for_function(f"{SCREEN}?.display().owner === true")
        assert wait_until(lambda: list(screen_size(name)) == expected_size(1))
        page.get_by_role('button', name='Display', exact=True).click()
        page.get_by_role('menuitemcheckbox', name='Scaling: Auto (2×)', exact=True).click()
        assert wait_until(lambda: setting('/Gdk/WindowScalingFactor') == '2')
        page.evaluate("window.testDpr = 1; window.dispatchEvent(new Event('resize'))")
        assert wait_until(lambda: setting('/Gdk/WindowScalingFactor') == '1')
        assert wait_until(lambda: list(screen_size(name)) == expected_size(1))
        page.evaluate("window.testDpr = 2; window.dispatchEvent(new Event('resize'))")
        assert wait_until(lambda: setting('/Gdk/WindowScalingFactor') == '2')
        successor = page.context.new_page()
        successor.add_init_script("Object.defineProperty(window, 'devicePixelRatio', {get: () => 1})")
        successor.goto(f"{supervisor.base}/{name}/")
        successor.wait_for_function(f"{SCREEN}?.display().owner === false")
        page.close()
        successor.wait_for_function(f"{SCREEN}?.display().owner === true")
        assert wait_until(lambda: setting('/Gdk/WindowScalingFactor') == '1')
        assert wait_until(lambda: 40 <= panel_height() <= 42)
        browser.close()
