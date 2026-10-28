"""The touch bridge against the pinned xterm renderer, without a live shell."""
from playwright.sync_api import sync_playwright

from test_ui_images import mock_ui, mock_page  # noqa: F401 (shared Vite fixture)


def test_touch_scrolls_mouse_protocol_and_local_history(mock_ui):
    with sync_playwright() as p:
        browser, page = mock_page(p, mock_ui, "/")
        try:
            cdp = page.context.new_cdp_session(page)
            cdp.send('Emulation.setDeviceMetricsOverride', {
                'width': 390, 'height': 844, 'deviceScaleFactor': 1, 'mobile': True})
            cdp.send('Emulation.setTouchEmulationEnabled', {'enabled': True, 'maxTouchPoints': 5})
            page.evaluate(r'''async () => {
              document.body.innerHTML = '<div class="terminal-host" style="width:380px;height:600px"></div>';
              for (const href of ['/node_modules/@xterm/xterm/css/xterm.css', '/src/terminal/terminal.css']) {
                const link = document.createElement('link'); link.rel = 'stylesheet'; link.href = href;
                document.head.append(link);
              }
              const {Terminal} = await import('/node_modules/@xterm/xterm/lib/xterm.mjs');
              const {installTouchScroll} = await import('/src/terminal/touch-scroll.ts');
              window.touchTerm = new Terminal({cols: 35, rows: 28});
              touchTerm.open(document.querySelector('.terminal-host'));
              window.disposeTouch = installTouchScroll(touchTerm.element);
              window.touchReports = [];
              touchTerm.onData(data => touchReports.push(data));
              await new Promise(done => touchTerm.write('\x1b[?1049h\x1b[?1000h\x1b[?1006h', done));
            }''')
            page.wait_for_function("getComputedStyle(touchTerm.element).touchAction.includes('pan-x')")

            def finger(kind, y=200):
                cdp.send('Input.dispatchTouchEvent', {'type': kind, 'touchPoints':
                         [] if kind == 'touchEnd' else [{'x': 150, 'y': y}]})

            def swipe(start, end):
                finger('touchStart', start)
                for step in range(1, 13):
                    finger('touchMove', start + (end - start) * step / 12)
                    page.wait_for_timeout(20)
                finger('touchEnd')

            swipe(180, 420)
            page.wait_for_function("touchReports.length > 0")
            reports = page.evaluate('touchReports.splice(0)')
            assert all(report.startswith('\x1b[<64;') for report in reports), reports
            swipe(420, 180)
            page.wait_for_function("touchReports.length > 0")
            reports = page.evaluate('touchReports.splice(0)')
            assert all(report.startswith('\x1b[<65;') for report in reports), reports

            page.evaluate(r'''async () => {
              await new Promise(done => touchTerm.write('\x1b[?1049l\x1b[?1000l\x1b[?1006l' +
                Array.from({length:150}, (_,i) => 'local row ' + i).join('\r\n'), done));
              touchTerm.scrollToBottom();
            }''')
            page.wait_for_function('touchTerm.buffer.active.viewportY > 100')
            before = page.evaluate('touchTerm.buffer.active.viewportY')
            swipe(180, 420)
            page.wait_for_function('(before) => touchTerm.buffer.active.viewportY < before', arg=before)
            # Local scrolling must not manufacture shell-history arrow keys.
            assert page.evaluate('touchReports') == []
            page.evaluate('disposeTouch(); touchTerm.dispose()')
        finally:
            page.close()
            browser.close()
