"""Retina density, runtime overrides and shared display ownership."""
import json

from playwright.sync_api import expect, sync_playwright
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

        # Returning from a terminal recreates the viewer. Its handshake must
        # not briefly resize the desktop to the iframe's unscaled CSS size.
        messages = []
        resolutions = []

        def watch_socket(socket):
            if '/screen/' not in socket.url:
                return
            socket.on('framesent', lambda data: messages.append(data))

            def received(data):
                if not isinstance(data, str) or not data.startswith('{'):
                    return
                value = json.loads(data)
                if value.get('type') == 'stream_resolution' and value.get('displayId') == 'primary':
                    resolutions.append([value['width'], value['height']])

            socket.on('framereceived', received)

        page.on('websocket', watch_socket)
        page.get_by_role('button', name='New terminal', exact=True).click()
        expect(page.locator('[data-terminal-pane]:not([hidden]) [data-terminal-state=connected]')).to_be_attached(timeout=15000)
        for _ in range(3):
            messages.clear()
            resolutions.clear()
            page.get_by_role('tab', name='Desktop', exact=True).click()
            page.wait_for_function(f"{SCREEN}?.display().owner === true")
            assert wait_until(lambda: list(screen_size(name)) == expected_size(2))
            page.wait_for_timeout(500)  # Include delayed upstream auto-resize.
            desired = expected_size(2)
            assert resolutions and all(size == desired for size in resolutions), resolutions
            for message in messages:
                if not isinstance(message, str):
                    continue
                if message.startswith('r,'):
                    assert message.split(',')[1] == f'{desired[0]}x{desired[1]}', message
                elif message.startswith('s,'):
                    assert message.split(',')[1] == '192', message
                elif message.startswith('SETTINGS,'):
                    settings = json.loads(message[9:])
                    assert 'initialClientWidth' not in settings and 'initialClientHeight' not in settings, settings
                    assert settings.get('scaling_dpi', 192) == 192, settings
            assert setting('/Gdk/WindowScalingFactor') == '2'
            page.get_by_role('tab').nth(1).click()
        page.get_by_role('tab', name='Desktop', exact=True).click()
        page.wait_for_function(f"{SCREEN}?.display().owner === true")

        page.wait_for_function(f"{SCREEN}?.video() !== null")
        pic = page.evaluate(PICTURE)
        page.mouse.move(*to_page(pic, 600, 400))
        assert wait_until(lambda: all(abs(a-b) <= 4 for a,b in zip(mouse_location(name), (600, 400))))

        page.get_by_role('button', name='Display', exact=True).click()
        page.get_by_role('menuitemcheckbox', name='Resolution: 1280 × 800', exact=True).click()
        assert wait_until(lambda: screen_size(name) == (2560, 1600))
        def panel_height():
            # Measure GTK's actual dock window. XFWM's cached _NET_WORKAREA
            # can lag a simultaneous RandR/GTK density change even when the
            # panel and its _NET_WM_STRUT_PARTIAL have the correct size.
            windows = in_desktop(name, "xdotool", "search", "--onlyvisible", "--class", "^Xfce4-panel$", check=False).split()
            for window in windows:
                if "_NET_WM_WINDOW_TYPE_DOCK" in in_desktop(name, "xprop", "-id", window, "_NET_WM_WINDOW_TYPE", check=False):
                    geometry = in_desktop(name, "xdotool", "getwindowgeometry", "--shell", window, check=False)
                    bounds = {k: int(v) for k, v in (line.split("=", 1) for line in geometry.splitlines())}
                    if not {"X", "Y", "WIDTH", "HEIGHT"} <= bounds.keys():
                        continue  # The dock may be remapping during the resize.
                    width, height = screen_size(name)
                    if (bounds["X"] >= 0 and bounds["Y"] >= 0
                            and bounds["X"] + bounds["WIDTH"] <= width
                            and bounds["Y"] + bounds["HEIGHT"] <= height):
                        return bounds["HEIGHT"]
            return 0
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
        # Finish the old owner's native resize before testing ownership handover.
        assert wait_until(lambda: list(screen_size(name)) == expected_size(2))
        assert wait_until(lambda: 80 <= panel_height() <= 84)
        successor = page.context.new_page()
        successor.add_init_script("Object.defineProperty(window, 'devicePixelRatio', {get: () => 1})")
        successor.goto(f"{supervisor.base}/{name}/")
        successor.wait_for_function(f"{SCREEN}?.display().owner === false")
        page.close()
        successor.wait_for_function(f"{SCREEN}?.display().owner === true")
        assert wait_until(lambda: setting('/Gdk/WindowScalingFactor') == '1')
        assert wait_until(lambda: 40 <= panel_height() <= 42), {
            'screen': screen_size(name),
            'workarea': in_desktop(name, "xprop", "-root", "_NET_WORKAREA"),
            'panels': in_desktop(name, "bash", "-lc", """
                for window in $(xdotool search --onlyvisible --class '^Xfce4-panel$'); do
                    xdotool getwindowgeometry --shell "$window"
                    xprop -id "$window" _NET_WM_WINDOW_TYPE _NET_WM_STRUT_PARTIAL
                done
            """),
        }
        browser.close()
