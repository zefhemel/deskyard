"""Owned terminal UI against real tmux. Viewport emulation is not native mobile QA."""
import time
import shlex
import uuid

from playwright.sync_api import expect, sync_playwright

from helpers import signed_in
from test_terminal_transport import shell, eventually

SOCKET = '/run/workspace/deskyard-terminal/tmux.sock'


def pane(name, field, session='main'):
    return shell(name, f'tmux -S {SOCKET} display-message -p -t ={session}: "#{{{field}}}"')


def command(page, text):
    textarea = page.get_by_role('textbox', name='Terminal input')
    textarea.focus()
    page.keyboard.insert_text(text)
    page.keyboard.press('Enter')


def connected(page):
    expect(page.locator('[data-terminal-state=connected]')).to_be_attached(timeout=15000)


def test_owned_terminal_shared_shell_keys_history_and_reconnect(supervisor, desktop, name, tmp_path):
    with sync_playwright() as p:
        browser, first = signed_in(p, supervisor.base)
        errors = []
        first.on('pageerror', lambda error: errors.append(str(error)))
        first.add_init_script('''window.__terminalSockets=[]; const NativeSocket=window.WebSocket;
          window.WebSocket=class extends NativeSocket {constructor(...args){super(...args);window.__terminalSockets.push(this)}};''')
        first.goto(f'{supervisor.base}/{name}/terminal/')
        connected(first)
        notices = first.request.get(supervisor.base + '/assets/terminal-notices.txt')
        assert notices.status == 200 and 'Permission is hereby granted' in notices.text()
        assert first.evaluate('document.querySelector("[data-terminal-page]").getBoundingClientRect().width === innerWidth')
        assert first.get_by_role('textbox', name='Terminal input').evaluate('(e)=>document.activeElement!==e')
        nonce = uuid.uuid4().hex
        command(first, f'export UI_NONCE={nonce}; printf "%s" "$$" > /workspace/ui-pid')
        eventually(lambda: shell(name, 'cat /workspace/ui-pid 2>/dev/null || true').isdigit(), True)
        pid = shell(name, 'cat /workspace/ui-pid')
        second = first.context.new_page()
        second.set_viewport_size({'width': 1300, 'height': 850})
        second.goto(f'{supervisor.base}/{name}/terminal/')
        connected(second)
        command(second, 'printf "%s" "$UI_NONCE" > /workspace/ui-shared')
        eventually(lambda: shell(name, 'cat /workspace/ui-shared 2>/dev/null || true'), nonce)
        assert pane(name, 'pane_pid') == pid
        first.reload()
        connected(first)
        assert pane(name, 'pane_pid') == pid
        first.get_by_role('textbox', name='Terminal input').focus()
        first.evaluate('window.__terminalSockets.at(-1).close(4000)')
        expect(first.locator('[data-terminal-state=retrying]')).to_be_attached()
        first.keyboard.insert_text('printf bad > /workspace/ui-replayed\r')
        connected(first)
        assert shell(name, 'test -e /workspace/ui-replayed && echo bad || echo good') == 'good'
        command(first, 'printf reconnected > /workspace/ui-reconnected')
        eventually(lambda: shell(name, 'cat /workspace/ui-reconnected 2>/dev/null || true'), 'reconnected')

        # Tapping a helper keeps native xterm focus, sends actual control input,
        # and interrupts a foreground process independently of rendered echo.
        command(first, 'sleep 60; printf interrupted > /workspace/ui-interrupt')
        first.get_by_role('button', name='Ctrl+C', exact=True).click()
        expect(first.get_by_role('textbox', name='Terminal input')).to_be_focused()
        command(first, 'printf ready > /workspace/ui-after-interrupt')
        eventually(lambda: shell(name, 'cat /workspace/ui-after-interrupt 2>/dev/null || true'), 'ready')
        assert shell(name, 'test -e /workspace/ui-interrupt && echo bad || echo good') == 'good'
        shell(name, 'printf tab-proof > /workspace/ui-completion-proof')
        first.get_by_role('textbox', name='Terminal input').focus()
        first.keyboard.insert_text('cat /workspace/ui-completion-pro')
        first.get_by_role('button', name='Tab', exact=True).click()
        first.keyboard.insert_text(' > /workspace/ui-tab-result')
        first.keyboard.press('Enter')
        eventually(lambda: shell(name, 'cat /workspace/ui-tab-result 2>/dev/null || true'), 'tab-proof')
        first.get_by_role('button', name='Control', exact=True).click()
        first.get_by_role('textbox', name='Terminal input').dispatch_event('compositionstart')
        expect(first.get_by_role('button', name='Control', exact=True)).to_have_attribute('aria-pressed', 'false')
        first.get_by_role('textbox', name='Terminal input').dispatch_event('compositionend')
        first.get_by_role('button', name='Control', exact=True).click()
        first.get_by_role('textbox', name='Terminal input').evaluate('''e => {
          const data=new DataTransfer();data.setData('text/plain','');
          e.dispatchEvent(new ClipboardEvent('paste',{clipboardData:data,bubbles:true}));
        }''')
        expect(first.get_by_role('button', name='Control', exact=True)).to_have_attribute('aria-pressed', 'false')
        command(first, "bind 'set enable-bracketed-paste off'")
        first.get_by_role('textbox', name='Terminal input').focus()
        first.keyboard.insert_text('printf %s ')
        first.get_by_role('button', name='Control', exact=True).click()
        first.get_by_role('textbox', name='Terminal input').evaluate('''e => {
          const data=new DataTransfer();data.setData('text/plain','c');
          e.dispatchEvent(new ClipboardEvent('paste',{clipboardData:data,bubbles:true}));
        }''')
        expect(first.get_by_role('button', name='Control', exact=True)).to_have_attribute('aria-pressed', 'false')
        first.keyboard.insert_text(' > /workspace/ui-paste')
        first.keyboard.press('Enter')
        eventually(lambda: shell(name, 'cat /workspace/ui-paste 2>/dev/null || true'), 'c')
        first.get_by_role('button', name='Control', exact=True).click()
        first.keyboard.insert_text('l')
        expect(first.get_by_role('button', name='Control', exact=True)).to_have_attribute('aria-pressed', 'false')
        first.get_by_role('button', name='Alt', exact=True).click()
        first.keyboard.insert_text('日')
        expect(first.get_by_role('button', name='Alt', exact=True)).to_have_attribute('aria-pressed', 'false')
        first.get_by_role('button', name='Ctrl+C', exact=True).click()

        # History is tmux copy-mode, not xterm's alternate-screen scrollback.
        command(first, "printf 'history-line-%04d\\n' {1..200}")
        eventually(lambda: int(pane(name, 'history_size')) > 100, True)
        first.get_by_role('button', name='Terminal history').click()
        eventually(lambda: pane(name, 'pane_in_mode'), '1')
        first.get_by_role('button', name='Page up').click()
        eventually(lambda: int(pane(name, 'scroll_position')) > 0, True)
        up_position = int(pane(name, 'scroll_position'))
        first.get_by_role('button', name='Page down').click()
        eventually(lambda: int(pane(name, 'scroll_position')) < up_position, True)
        first.get_by_role('button', name='Escape', exact=True).click()
        eventually(lambda: pane(name, 'pane_in_mode'), '0')
        command(first, 'printf after-history > /workspace/ui-history')
        eventually(lambda: shell(name, 'cat /workspace/ui-history 2>/dev/null || true'), 'after-history')

        # Fullscreen app: restore termios in finally and observe both readiness
        # and completion before sending another shell command.
        raw_program = """import os, termios, tty
from pathlib import Path
old = termios.tcgetattr(0)
try:
    tty.setraw(0)
    os.write(1, b'\\x1b[?1h\\x1b[?1049h')
    Path('/workspace/ui-raw-ready').write_text('ready')
    data = os.read(0, 3)
    Path('/workspace/ui-arrow').write_text(data.hex())
finally:
    os.write(1, b'\\x1b[?1l\\x1b[?1049l')
    termios.tcsetattr(0, termios.TCSADRAIN, old)
    Path('/workspace/ui-raw-done').write_text('done')
"""
        shell(name, 'printf %s '+shlex.quote(raw_program)+' > /workspace/ui-raw.py')
        command(first, 'python3 /workspace/ui-raw.py')
        eventually(lambda: shell(name, 'cat /workspace/ui-raw-ready 2>/dev/null || true'), 'ready')
        time.sleep(.2)
        first.get_by_role('button', name='Arrow up').click()
        eventually(lambda: shell(name, 'cat /workspace/ui-arrow 2>/dev/null || true'), '1b4f41')
        eventually(lambda: shell(name, 'cat /workspace/ui-raw-done 2>/dev/null || true'), 'done')
        eventually(lambda: pane(name, 'pane_current_command'), 'bash')

        # Output credit must continue through more than one 256KiB window.
        command(first, "python3 -c 'print(\"x\"*800000)' ; printf drained > /workspace/ui-output")
        eventually(lambda: shell(name, 'cat /workspace/ui-output 2>/dev/null || true'), 'drained', seconds=20)
        command(first, 'printf unicode-日 > /workspace/ui-unicode')
        eventually(lambda: shell(name, 'cat /workspace/ui-unicode 2>/dev/null || true'), 'unicode-日')
        command(first, "clear; printf 'Shared terminal ready\\nBrowser and CLI share this shell.\\n'")
        time.sleep(.2)
        first.screenshot(path=str(tmp_path / 'terminal-desktop.png'))
        # Passive narrow clients keep readable type; their next interaction
        # joins latest sizing without a separate manual window-size policy.
        first.set_viewport_size({'width':390,'height':600})
        second.set_viewport_size({'width':1400,'height':850})
        time.sleep(.5)
        first.screenshot(path=str(tmp_path / 'terminal-passive-narrow-pane.png'))
        command(first, 'printf narrow > /workspace/ui-narrow')
        eventually(lambda: shell(name, 'cat /workspace/ui-narrow 2>/dev/null || true'), 'narrow')
        assert first.locator('.xterm-helper-textarea').evaluate('(e)=>getComputedStyle(e).fontSize') == '16px'
        first.set_viewport_size({'width':1280,'height':720})

        # A distinct session is explicitly selected through the owned selector.
        first.get_by_role('button', name='Sessions: main', exact=True).click()
        first.get_by_role('textbox', name='New session name').fill('other')
        first.get_by_role('button', name='Create session', exact=True).click()
        connected(first)
        command(first, 'printf "%s" "$$" > /workspace/ui-other-pid')
        eventually(lambda: shell(name, 'cat /workspace/ui-other-pid 2>/dev/null || true').isdigit(), True)
        assert shell(name, 'cat /workspace/ui-other-pid') != pid
        command(first, 'exit 7')
        expect(first.get_by_role('button', name='Reopen terminal')).to_be_visible(timeout=10000)
        time.sleep(1)
        expect(first.locator('[data-terminal-state=exited]')).to_be_attached()
        first.get_by_role('button', name='Reopen terminal').click()
        connected(first)
        command(first, 'printf reopened > /workspace/ui-reopened')
        eventually(lambda: shell(name, 'cat /workspace/ui-reopened 2>/dev/null || true'), 'reopened')
        assert errors == []
        browser.close()


def test_terminal_short_viewports(supervisor, desktop, name, tmp_path):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport={'width': 390, 'height': 420}, is_mobile=True, has_touch=True)
        page.goto(f'{supervisor.base}/{name}/terminal/')
        connected(page)
        for width, height, label in [(390, 420, 'portrait-keyboard-emulated'), (852, 190, 'landscape-keyboard-emulated'), (750, 100, 'ultra-short'), (750, 68, 'minimum-height')]:
            page.set_viewport_size({'width': width, 'height': height})
            page.wait_for_timeout(300)
            measurements = page.evaluate('''() => {
              const box = s => document.querySelector(s).getBoundingClientRect().toJSON();
              return { host: box('.terminal-host'), bar: box('.terminal-keybar'), header: box('.terminal-header'), font: getComputedStyle(document.querySelector('.xterm-helper-textarea')).fontSize,
                controls: [...document.querySelectorAll('.terminal-keybar button')].map(e=>({label:e.getAttribute('aria-label'),height:e.getBoundingClientRect().height,width:e.getBoundingClientRect().width})) };
            }''')
            assert measurements['host']['height'] >= 24, measurements
            assert measurements['bar']['bottom'] <= height + 1, measurements
            assert measurements['font'] == '16px', measurements
            assert all(c['height'] >= 44 and c['width'] >= 44 for c in measurements['controls']), measurements
            for label_key in ['Ctrl+C', 'Escape', 'Tab', 'Control', 'Alt', 'Arrow up']:
                button = page.get_by_role('button', name=label_key, exact=True)
                button.scroll_into_view_if_needed()
                assert button.bounding_box()['y'] + 44 <= height + 1
            page.get_by_role('button', name='Show keyboard').click()
            page.get_by_role('button', name='Ctrl+C', exact=True).click()
            expect(page.get_by_role('textbox', name='Terminal input')).to_be_focused()
            page.screenshot(path=str(tmp_path / f'terminal-{label}.png'))
        browser.close()


def test_terminal_picker_live_join_create_and_errors(supervisor, desktop, name):
    """Choosing discovery results joins that pane; creating a name owns another shell."""
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        page.goto(f'{supervisor.base}/{name}/terminal/')
        connected(page)
        original = pane(name, 'pane_pid')
        shell(name, f'tmux -S {SOCKET} new-session -d -s picker-existing')
        existing = pane(name, 'pane_pid', 'picker-existing')
        page.get_by_role('button', name='Sessions: main', exact=True).click()
        page.get_by_role('button', name='Join picker-existing', exact=True).click()
        connected(page)
        assert 'session=picker-existing' in page.url
        command(page, 'printf "%s" "$$" > /workspace/picker-pid')
        eventually(lambda: shell(name, 'cat /workspace/picker-pid 2>/dev/null || true'), existing)
        assert original != existing
        page.get_by_role('button', name='Sessions: picker-existing', exact=True).click()
        page.get_by_role('textbox', name='New session name').fill('picker-existing')
        expect(page.get_by_role('button', name='Create session', exact=True)).to_be_disabled()
        page.get_by_role('textbox', name='New session name').fill('picker-created')
        page.get_by_role('button', name='Create session', exact=True).click()
        connected(page)
        assert pane(name, 'pane_pid', 'picker-created') not in (original, existing)
        page.get_by_role('button', name='Sessions: picker-created', exact=True).click()
        shell(name, f'tmux -S {SOCKET} new-session -d -s picker-later')
        expect(page.get_by_role('button', name='Join picker-later', exact=True)).to_be_visible(timeout=10000)
        shell(name, f'tmux -S {SOCKET} kill-session -t =picker-later')
        expect(page.get_by_role('button', name='Join picker-later', exact=True)).to_have_count(0, timeout=10000)
        page.route('**/terminal/sessions', lambda route: route.fulfill(status=502, json={'error': 'Discovery unavailable'}))
        page.get_by_role('button', name='Refresh sessions', exact=True).click()
        expect(page.get_by_role('alert')).to_contain_text('Discovery unavailable')
        expect(page.get_by_role('button', name='Join picker-existing', exact=True)).to_be_visible()
        page.unroute('**/terminal/sessions')
        page.get_by_role('button', name='Retry sessions', exact=True).click()
        expect(page.get_by_role('alert')).to_have_count(0)
        page.route('**/terminal/sessions', lambda route: route.fulfill(json={'sessions': []}))
        page.get_by_role('button', name='Refresh sessions', exact=True).click()
        expect(page.get_by_text('No running sessions.')).to_be_visible()
        page.get_by_role('button', name='Close sessions', exact=True).click()
        browser.close()


def test_terminal_insufficient_viewport_preserves_native_input_and_connection(supervisor, desktop, name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport={'width': 750, 'height': 404})
        page.goto(f'{supervisor.base}/{name}/terminal/')
        connected(page)
        command(page, 'printf "%s" "$$" > /workspace/fallback-pid')
        eventually(lambda: shell(name, 'cat /workspace/fallback-pid 2>/dev/null || true').isdigit(), True)
        pid = pane(name, 'pane_pid')
        page.evaluate("window.__focusedTerminal = document.querySelector('.xterm-helper-textarea')")
        page.set_viewport_size({'width': 750, 'height': 36})
        expect(page.get_by_text('Rotate to portrait or dismiss the keyboard to use the terminal.')).to_be_visible()
        assert page.evaluate("document.activeElement === window.__focusedTerminal && window.__focusedTerminal.isConnected")
        connected(page)
        page.set_viewport_size({'width': 402, 'height': 404})
        expect(page.get_by_text('Rotate to portrait or dismiss the keyboard to use the terminal.')).to_be_hidden()
        assert page.evaluate("document.activeElement === window.__focusedTerminal")
        command(page, 'printf "%s" "$$" > /workspace/fallback-restored')
        eventually(lambda: shell(name, 'cat /workspace/fallback-restored 2>/dev/null || true'), pid)
        browser.close()


def test_terminal_picker_uses_visual_viewport_when_layout_is_taller(supervisor, desktop, name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport={'width': 750, 'height': 404})
        page.goto(f'{supervisor.base}/{name}/terminal/')
        connected(page)
        page.evaluate("""() => {
          Object.defineProperty(visualViewport, 'height', {configurable: true, value: 68});
          visualViewport.dispatchEvent(new Event('resize'));
        }""")
        page.get_by_role('button', name='Sessions: main', exact=True).click()
        for label in ['Refresh sessions', 'Close sessions', 'Create session']:
            button = page.get_by_role('button', name=label, exact=True)
            button.scroll_into_view_if_needed()
            box = button.bounding_box()
            assert box['height'] >= 44 and box['y'] >= 0 and box['y'] + box['height'] <= 68, (label, box)
        page.get_by_role('textbox', name='New session name').scroll_into_view_if_needed()
        box = page.get_by_role('textbox', name='New session name').bounding_box()
        assert box['y'] >= 0 and box['y'] + box['height'] <= 68, box
        browser.close()


def test_terminal_compact_budget_with_reopen_and_safe_bottom(supervisor, desktop, name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport={'width': 750, 'height': 404})
        page.goto(f'{supervisor.base}/{name}/terminal/')
        connected(page)
        # Substitute only the CSS environment value: retain selectors and the
        # actual cascade, since this Chromium has no safe-area CDP emulation.
        page.evaluate("""() => {
          const substitute = rules => { for (const rule of rules) {
            if (rule.style) rule.style.cssText = rule.style.cssText.replaceAll('env(safe-area-inset-bottom)', '20px');
            if (rule.cssRules) substitute(rule.cssRules);
          }};
          for (const sheet of document.styleSheets) substitute(sheet.cssRules);
          visualViewport.dispatchEvent(new Event('resize'));
        }""")
        assert page.locator('.terminal-keybar').evaluate("e => getComputedStyle(e).paddingBottom") == '20px'
        command(page, 'exit')
        expect(page.get_by_role('button', name='Reopen terminal')).to_be_visible()
        for height in [174, 180, 195, 196, 68]:
            page.set_viewport_size({'width': 750, 'height': height})
            page.wait_for_timeout(100)
            geometry = page.evaluate('''() => {
              const box = s => document.querySelector(s).getBoundingClientRect().toJSON();
              const host = box('.terminal-host'), scroll = box('.terminal-scroll');
              return {host, scroll, bar:box('.terminal-keybar'), reopen:box('.terminal-status button')};
            }''')
            assert geometry['host']['height'] >= 24 and geometry['host']['bottom'] <= geometry['scroll']['bottom'] + 1, geometry
            assert geometry['reopen']['height'] >= 44 and geometry['reopen']['bottom'] <= height + 1, geometry
            assert geometry['bar']['bottom'] <= height + 1, geometry
        page.set_viewport_size({'width': 750, 'height': 36})
        expect(page.get_by_text('Rotate to portrait or dismiss the keyboard to use the terminal.')).to_be_visible()
        browser.close()
