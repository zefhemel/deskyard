"""The computer page's terminal tabs against real tmux. Viewport emulation is not native mobile QA."""
import os
import pty
import re
import subprocess
import threading
import time
import shlex
import uuid

from playwright.sync_api import expect, sync_playwright

from conftest import docker
from helpers import SHOWING, open_desktop, signed_in, wait_until
from test_terminal_transport import shell, eventually

SOCKET = '/run/workspace/deskyard-terminal/tmux.sock'
# The shown terminal pane: hidden panes stay mounted and connected.
ACTIVE = '[data-terminal-pane]:not([hidden])'


def pane(name, field, session='main'):
    return shell(name, f'tmux -S {SOCKET} display-message -p -t ={session}: "#{{{field}}}"')


def sessions(name):
    """The computer's public sessions, from the image's own helper."""
    return [line.split('\t')[0] for line in shell(name, 'deskyard terminal-list').splitlines() if line]


def create_session(name):
    """A session opened inside the computer, as the desktop's terminal does."""
    return docker('exec', '-u', 'user', 'deskyard-' + name, 'deskyard', 'terminal-create').stdout.strip()


def tab_name(session):
    """A terminal tab's accessible name: the session, perhaps with who else is attached."""
    return re.compile(rf'^{re.escape(session)}(\W|$)')


def command(page, text):
    textarea = page.get_by_role('textbox', name='Terminal input')
    textarea.focus()
    page.keyboard.insert_text(text)
    page.keyboard.press('Enter')


def after_interrupt(page, name):
    """Wait until the shell takes input again. The tty discards input that
    arrives while an interrupt lands, so a command typed straight after Ctrl+C
    can vanish; repeat a marker command until one runs."""
    for n in range(10):
        marker = f'/workspace/ui-after-interrupt-{n}'
        # Enter first: it runs, and so clears, whatever part of a lost command was kept.
        page.keyboard.press('Enter')
        command(page, f'printf ready > {marker}')
        if wait_until(lambda: shell(name, f'cat {marker} 2>/dev/null || true') == 'ready', 3):
            return
    raise AssertionError('the shell took no input after Ctrl+C')


def connected(page):
    expect(page.locator(f'{ACTIVE} [data-terminal-state=connected]')).to_be_attached(timeout=15000)


def open_terminal(page, base, name, session='main', create=True):
    """Opens a terminal tab: create as an explicit open (the card's +), else a join."""
    page.goto(f'{base}/{name}/?terminal={session}' + ('&create=1' if create else ''))
    connected(page)


def cli_session(cli, remote, name, session):
    """`deskyard terminal SESSION` from elsewhere, kept attached: its process."""
    process = subprocess.Popen([cli, '-e', name, 'terminal', session], env=remote.env,
                               stdin=subprocess.PIPE, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    eventually(lambda: session in sessions(name), True, seconds=20)
    return process


def stop(process):
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(5)


def test_owned_terminal_shared_shell_keys_history_and_reconnect(supervisor, desktop, name, tmp_path):
    with sync_playwright() as p:
        browser, first = signed_in(p, supervisor.base)
        errors = []
        first.on('pageerror', lambda error: errors.append(str(error)))
        first.add_init_script('''window.__terminalSockets=[]; const NativeSocket=window.WebSocket;
          window.WebSocket=class extends NativeSocket {constructor(...args){super(...args);window.__terminalSockets.push(this)}};''')
        open_terminal(first, supervisor.base, name)
        notices = first.request.get(supervisor.base + '/assets/terminal-notices.txt')
        assert notices.status == 200 and 'Permission is hereby granted' in notices.text()
        assert first.evaluate('document.querySelector("[data-terminal-pane]:not([hidden])").getBoundingClientRect().width === innerWidth')
        # With a mouse, showing a terminal tab puts the keyboard in it.
        expect(first.get_by_role('textbox', name='Terminal input')).to_be_focused()
        nonce = uuid.uuid4().hex
        command(first, f'export UI_NONCE={nonce}; printf "%s" "$$" > /workspace/ui-pid')
        eventually(lambda: shell(name, 'cat /workspace/ui-pid 2>/dev/null || true').isdigit(), True)
        pid = shell(name, 'cat /workspace/ui-pid')
        second = first.context.new_page()
        second.set_viewport_size({'width': 1300, 'height': 850})
        open_terminal(second, supervisor.base, name, create=False)
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
        after_interrupt(first, name)
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
        after_interrupt(first, name)

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

        # + opens another session with its own shell; its shell exiting ends the
        # session, so the tab closes and the left neighbour shows again.
        first.get_by_role('button', name='New terminal', exact=True).click()
        first.wait_for_url(re.compile(r'\?terminal=terminal-\d+$'))
        other = first.url.rsplit('=', 1)[1]
        connected(first)
        command(first, 'printf "%s" "$$" > /workspace/ui-other-pid')
        eventually(lambda: shell(name, 'cat /workspace/ui-other-pid 2>/dev/null || true').isdigit(), True)
        assert shell(name, 'cat /workspace/ui-other-pid') != pid
        command(first, 'exit 7')
        expect(first.get_by_text(f'Session {other} ended')).to_be_visible(timeout=10000)
        expect(first.get_by_role('tab', name=tab_name(other))).to_have_count(0)
        assert first.url.endswith(f'/{name}/?terminal=main')
        connected(first)
        command(first, 'printf reopened > /workspace/ui-reopened')
        eventually(lambda: shell(name, 'cat /workspace/ui-reopened 2>/dev/null || true'), 'reopened')
        assert errors == []
        browser.close()


def test_terminal_short_viewports(supervisor, desktop, name, tmp_path):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport={'width': 390, 'height': 420}, is_mobile=True, has_touch=True)
        open_terminal(page, supervisor.base, name)
        for width, height, label in [(390, 420, 'portrait-keyboard-emulated'), (852, 190, 'landscape-keyboard-emulated'), (750, 100, 'ultra-short'), (750, 68, 'minimum-height')]:
            page.set_viewport_size({'width': width, 'height': height})
            page.wait_for_timeout(300)
            measurements = page.evaluate('''() => {
              const box = s => document.querySelector('[data-terminal-pane]:not([hidden]) ' + s).getBoundingClientRect().toJSON();
              return { host: box('.terminal-host'), bar: box('.terminal-keybar'), font: getComputedStyle(document.querySelector('[data-terminal-pane]:not([hidden]) .xterm-helper-textarea')).fontSize,
                controls: [...document.querySelectorAll('[data-terminal-pane]:not([hidden]) .terminal-keybar button')].map(e=>({label:e.getAttribute('aria-label'),height:e.getBoundingClientRect().height,width:e.getBoundingClientRect().width})) };
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


def test_terminal_insufficient_viewport_preserves_native_input_and_connection(supervisor, desktop, name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport={'width': 750, 'height': 404})
        open_terminal(page, supervisor.base, name)
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


def test_terminal_compact_budget_with_reopen_and_safe_bottom(supervisor, desktop, name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport={'width': 750, 'height': 404})
        open_terminal(page, supervisor.base, name)
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
        # Detaching ends this attachment but not the session: the tab stays, to reopen.
        command(page, 'tmux detach-client')
        expect(page.get_by_role('button', name='Reopen terminal')).to_be_visible()
        for height in [174, 180, 195, 196, 68]:
            page.set_viewport_size({'width': 750, 'height': height})
            page.wait_for_timeout(100)
            geometry = page.evaluate('''() => {
              const box = s => document.querySelector('[data-terminal-pane]:not([hidden]) ' + s).getBoundingClientRect().toJSON();
              const host = box('.terminal-host'), scroll = box('.terminal-scroll');
              return {host, scroll, bar:box('.terminal-keybar'), reopen:box('.terminal-status button')};
            }''')
            assert geometry['host']['height'] >= 24 and geometry['host']['bottom'] <= geometry['scroll']['bottom'] + 1, geometry
            assert geometry['reopen']['height'] >= 44 and geometry['reopen']['bottom'] <= height + 1, geometry
            assert geometry['bar']['bottom'] <= height + 1, geometry
        page.set_viewport_size({'width': 750, 'height': 36})
        expect(page.get_by_text('Rotate to portrait or dismiss the keyboard to use the terminal.')).to_be_visible()
        browser.close()


def test_tabs_list_existing_sessions(supervisor, desktop, name, cli, remote):
    """Sessions opened inside the computer and through the CLI are tabs, and new ones appear within a poll."""
    native = create_session(name)
    process = cli_session(cli, remote, name, 'from-cli')
    try:
        with sync_playwright() as p:
            browser, page = signed_in(p, supervisor.base)
            page.goto(f'{supervisor.base}/{name}/')
            expect(page.get_by_role('tab', name='Desktop')).to_have_attribute('aria-selected', 'true')
            for session in (native, 'from-cli'):
                expect(page.get_by_role('tab', name=tab_name(session))).to_be_visible(timeout=15000)
            # The CLI is attached to from-cli: someone other than this page.
            expect(page.get_by_role('tab', name=tab_name('from-cli'))).to_contain_text('1')
            later = create_session(name)
            expect(page.get_by_role('tab', name=tab_name(later))).to_be_visible(timeout=15000)
            page.get_by_role('tab', name=tab_name(native)).click()
            connected(page)
            assert page.url.endswith(f'/{name}/?terminal={native}')
            command(page, 'printf "%s" "$$" > /workspace/tab-pid')
            eventually(lambda: shell(name, 'cat /workspace/tab-pid 2>/dev/null || true'), pane(name, 'pane_pid', native))
            browser.close()
    finally:
        stop(process)


def test_plus_opens_new_session(supervisor, desktop, name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        _, other = signed_in(p, supervisor.base)
        other.goto(f'{supervisor.base}/{name}/')
        page.goto(f'{supervisor.base}/{name}/')
        page.get_by_role('button', name='New terminal', exact=True).click()
        page.wait_for_url(re.compile(r'\?terminal=terminal-\d+$'))
        session = page.url.rsplit('=', 1)[1]
        connected(page)
        expect(page.get_by_role('tab', name=tab_name(session))).to_have_attribute('aria-selected', 'true')
        assert session in sessions(name)
        command(page, 'printf "%s" "$$" > /workspace/plus-pid')
        eventually(lambda: shell(name, 'cat /workspace/plus-pid 2>/dev/null || true'), pane(name, 'pane_pid', session))
        # Another browser sees the new tab, without having it open.
        expect(other.get_by_role('tab', name=tab_name(session))).to_be_visible(timeout=15000)
        expect(other.get_by_role('tab', name='Desktop')).to_have_attribute('aria-selected', 'true')
        # A second + makes another session rather than joining this one.
        page.get_by_role('button', name='New terminal', exact=True).click()
        page.wait_for_url(lambda url: 'terminal=terminal-' in url and not url.endswith(f'={session}'))
        connected(page)
        assert len(sessions(name)) == 2
        browser.close()


def test_switching_keeps_scrollback_and_reconnects_desktop(supervisor, desktop, name):
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        page.add_init_script('''window.__terminalSockets=[]; const NativeSocket=window.WebSocket;
          window.WebSocket=class extends NativeSocket {constructor(...args){super(...args);
            if (String(args[0]).includes('/terminal')) window.__terminalSockets.push(String(args[0]))}};''')
        open_desktop(page, supervisor.base, name)
        page.get_by_role('button', name='New terminal', exact=True).click()
        connected(page)
        session = page.url.rsplit('=', 1)[1]
        # The hidden Desktop tab doesn't stream: its frame is gone.
        expect(page.locator('iframe')).to_have_count(0)
        command(page, "printf 'scroll-mark-%d\\n' 1 2 3")
        rows = page.locator(f'{ACTIVE} .xterm-rows')
        expect(rows).to_contain_text('scroll-mark-3')
        page.get_by_role('tab', name='Desktop').click()
        assert page.url.endswith(f'/{name}/')
        expect(page.locator('iframe')).to_have_count(1)
        page.wait_for_function(SHOWING, timeout=30000)
        expect(page.locator('[data-terminal-pane]', has_text='scroll-mark-3')).to_be_hidden()
        page.get_by_role('tab', name=tab_name(session)).click()
        connected(page)
        expect(page.locator('iframe')).to_have_count(0)
        expect(rows).to_contain_text('scroll-mark-3')
        # The terminal stayed connected while hidden: one socket, never reopened.
        assert len(page.evaluate('window.__terminalSockets')) == 1
        browser.close()


def test_end_session_for_everyone(supervisor, admin, desktop, name):
    def details(session):
        listed = admin.get(f'/api/computers/{name}/terminal/sessions').json()['sessions']
        return next((s for s in listed if s['name'] == session), None)

    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        _, other = signed_in(p, supervisor.base)
        # An idle shell that only this page uses ends at once, for everyone.
        page.goto(f'{supervisor.base}/{name}/')
        page.get_by_role('button', name='New terminal', exact=True).click()
        connected(page)
        idle = page.url.rsplit('=', 1)[1]
        other.goto(f'{supervisor.base}/{name}/')
        expect(other.get_by_role('tab', name=tab_name(idle))).to_be_visible(timeout=15000)
        eventually(lambda: (details(idle) or {}).get('clients'), 1)
        page.get_by_role('button', name=f'End {idle}', exact=True).click()
        expect(page.get_by_text(f'Ended {idle}')).to_be_visible()
        expect(page.get_by_role('dialog')).to_have_count(0)
        expect(page.get_by_role('tab', name=tab_name(idle))).to_have_count(0)
        assert idle not in sessions(name)
        expect(other.get_by_role('tab', name=tab_name(idle))).to_have_count(0, timeout=15000)

        # Someone else attached: asked first, and Cancel keeps the session.
        page.get_by_role('button', name='New terminal', exact=True).click()
        connected(page)
        shared = page.url.rsplit('=', 1)[1]
        open_terminal(other, supervisor.base, name, shared, create=False)
        eventually(lambda: (details(shared) or {}).get('clients'), 2)
        page.get_by_role('button', name=f'End {shared}', exact=True).click()
        dialog = page.get_by_role('dialog')
        expect(dialog).to_contain_text('Someone else is attached')
        dialog.get_by_role('button', name='Cancel').click()
        expect(dialog).to_have_count(0)
        assert shared in sessions(name)

        # A program running: asked first too, naming it.
        other.goto(f'{supervisor.base}/{name}/')
        eventually(lambda: (details(shared) or {}).get('clients'), 1)
        command(page, 'sleep 100')
        eventually(lambda: (details(shared) or {}).get('command'), 'sleep')
        page.get_by_role('button', name=f'End {shared}', exact=True).click()
        expect(dialog).to_contain_text('sleep')
        expect(dialog).not_to_contain_text('attached')
        dialog.get_by_role('button', name='Cancel').click()
        assert shared in sessions(name)

        # Ending it anyway closes the other browser's open tab too.
        open_terminal(other, supervisor.base, name, shared, create=False)
        page.get_by_role('button', name=f'End {shared}', exact=True).click()
        dialog.get_by_role('button', name='End session').click()
        expect(page.get_by_role('tab', name=tab_name(shared))).to_have_count(0)
        expect(other.get_by_text(f'Session {shared} ended')).to_be_visible(timeout=10000)
        expect(other.get_by_role('tab', name='Desktop')).to_have_attribute('aria-selected', 'true')
        assert other.url.endswith(f'/{name}/')
        assert shared not in sessions(name)
        assert not [s for s in shell(name, f'tmux -S {SOCKET} list-sessions -F "#{{session_name}}" 2>/dev/null || true').splitlines()
                    if s.startswith('_deskyard_')], 'an attachment group outlived its session'
        browser.close()


def test_closed_session_stays_closed(supervisor, admin, desktop, name):
    from websockets.sync.client import connect
    gone = create_session(name)
    shell(name, f'deskyard terminal-kill {gone}')
    # A join to a session that doesn't exist says so, and creates nothing.
    cookie = '; '.join(f'{k}={v}' for k, v in admin.cookies.items())
    with connect(admin.base.replace('http:', 'ws:') + f'/api/computers/{name}/terminal?session={gone}',
                 origin=admin.base, additional_headers={'Cookie': cookie}) as ws:
        ws.send('{"cols":80,"rows":24}')
        assert ws.recv(timeout=10) == '{"ended":true}'
    assert gone not in sessions(name)
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        # A deep link to it opens Desktop with a note.
        page.goto(f'{supervisor.base}/{name}/?terminal={gone}')
        expect(page.get_by_text(f'Session {gone} ended')).to_be_visible(timeout=10000)
        expect(page.get_by_role('tab', name='Desktop')).to_have_attribute('aria-selected', 'true')
        assert page.url.endswith(f'/{name}/')
        time.sleep(1)
        assert gone not in sessions(name)

        # A tab whose connection drops while the session ends elsewhere closes
        # on reconnecting, and the reconnect doesn't bring the session back.
        page.add_init_script('''window.__terminalSockets=[]; window.__blocked=false; const NativeSocket=window.WebSocket;
          window.WebSocket=class extends NativeSocket {constructor(url, ...rest){
            const terminal = String(url).includes('/terminal');
            super(terminal && window.__blocked ? 'ws://127.0.0.1:9/' : url, ...rest);
            if (terminal) { window.__terminalSockets.push(String(url)); window.__lastTerminal = this; }}};''')
        session = create_session(name)
        open_terminal(page, supervisor.base, name, session, create=False)
        page.evaluate('window.__blocked = true; window.__lastTerminal.close(4000)')
        expect(page.locator(f'{ACTIVE} [data-terminal-state=retrying]')).to_be_attached()
        shell(name, f'deskyard terminal-kill {session}')
        page.evaluate('window.__blocked = false')
        expect(page.get_by_text(f'Session {session} ended')).to_be_visible(timeout=20000)
        expect(page.get_by_role('tab', name=tab_name(session))).to_have_count(0)
        time.sleep(1)
        assert session not in sessions(name)
        assert not [u for u in page.evaluate('window.__terminalSockets') if 'create=1' in u]
        browser.close()


def test_card_terminal_links(supervisor, desktop, name, cli, remote):
    native = create_session(name)
    process = cli_session(cli, remote, name, 'card-cli')
    try:
        with sync_playwright() as p:
            browser, page = signed_in(p, supervisor.base)
            card = page.locator('article').filter(has=page.get_by_role('heading', name=name, exact=True))
            links = card.get_by_label('Open', exact=True)
            expect(links.get_by_role('link', name='Desktop', exact=True)).to_have_attribute('href', f'/{name}/')
            expect(links.get_by_role('link', name=re.compile(rf'^{native}'))).to_have_attribute('href', f'/{name}/?terminal={native}', timeout=15000)
            expect(links.get_by_role('link', name=re.compile('^card-cli'))).to_contain_text('1')
            # Without main, + opens main, creating it.
            opener = links.get_by_role('link', name='Open a terminal', exact=True)
            expect(opener).to_have_attribute('href', f'/{name}/?terminal=main&create=1')
            opener.click()
            connected(page)
            assert page.url.endswith(f'/{name}/?terminal=main')
            assert 'main' in sessions(name)
            # With main open, + makes a new session.
            page.goto(supervisor.base + '/')
            new = links.get_by_role('button', name='Open a new terminal session', exact=True)
            expect(new).to_be_visible(timeout=15000)
            new.click()
            page.wait_for_url(re.compile(r'\?terminal=terminal-\d+$'))
            connected(page)
            made = page.url.rsplit('=', 1)[1]
            assert made != native and made in sessions(name)
            # A session link joins that session's tab.
            page.goto(supervisor.base + '/')
            links.get_by_role('link', name=re.compile('^card-cli')).click()
            connected(page)
            expect(page.get_by_role('tab', name=tab_name('card-cli'))).to_have_attribute('aria-selected', 'true')
            # More than four: three and "+N more".
            create_session(name)
            page.goto(supervisor.base + '/')
            expect(links.get_by_role('link', name='+2 more', exact=True)).to_have_attribute('href', f'/{name}/', timeout=15000)
            expect(links.get_by_role('link')).to_have_count(5)  # Desktop, three sessions, +2 more
            browser.close()
    finally:
        stop(process)


def test_tabs_on_phone(supervisor, desktop, name):
    for _ in range(6):
        create_session(name)
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base, viewport={'width': 390, 'height': 760}, is_mobile=True, has_touch=True)
        open_terminal(page, supervisor.base, name, 'terminal-1', create=False)
        tablist = page.get_by_role('tablist')
        more = page.get_by_role('button', name='More', exact=True)
        # The row that scrolls holds the tab list and +.
        geometry = page.evaluate('''() => { const t = document.querySelector('[role=tablist]').parentElement.getBoundingClientRect();
          return {page: document.documentElement.scrollWidth, tabsTop: t.top, tabsWidth: t.width}; }''')
        # A second row of its own, scrolling sideways, without page overflow.
        assert geometry['page'] <= 390, geometry
        assert geometry['tabsTop'] >= more.bounding_box()['y'] + more.bounding_box()['height'] - 1, geometry
        assert tablist.evaluate('e => e.parentElement.scrollWidth > e.parentElement.clientWidth'), 'expected the tabs to overflow sideways'
        for label in ('Computers', 'Paste', 'More'):
            box = page.get_by_role('button' if label != 'Computers' else 'link', name=label, exact=True).bounding_box()
            assert box and box['x'] >= 0 and box['x'] + box['width'] <= 390, (label, box)
        # Every tab and + are reachable by scrolling the row.
        last = page.get_by_role('tab', name=tab_name('terminal-6'))
        last.scroll_into_view_if_needed()
        last.tap()
        connected(page)
        assert page.url.endswith('?terminal=terminal-6')
        plus = page.get_by_role('button', name='New terminal', exact=True)
        plus.scroll_into_view_if_needed()
        box = plus.bounding_box()
        assert box['x'] >= 0 and box['x'] + box['width'] <= 390, box
        # Text size moves into ⋯ on phones.
        more.tap()
        expect(page.get_by_role('menuitemcheckbox', name='Text size: 16px')).to_be_visible()
        page.keyboard.press('Escape')
        assert page.evaluate('document.documentElement.scrollWidth') <= 390
        browser.close()


def test_native_picker(desktop, name):
    """The desktop's Terminal launcher: a number joins that session, Enter opens a new one."""
    first = create_session(name)
    create_session(name)

    def pick():
        master, slave = pty.openpty()
        output = bytearray()
        process = subprocess.Popen(['docker', 'exec', '-it', '-u', 'user', '-e', 'TERM=xterm-256color', f'deskyard-{name}',
                                    'deskyard', 'terminal-pick'], stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
        os.close(slave)

        def read():
            try:
                while chunk := os.read(master, 65536):
                    output.extend(chunk)
            except OSError:
                pass
        threading.Thread(target=read, daemon=True).start()
        assert wait_until(lambda: b'New session' in output, 20), output.decode(errors='replace')
        return process, master, output

    def clients():
        return shell(name, f'tmux -S {SOCKET} list-clients -F "#{{client_session}}"').splitlines()

    process, master, output = pick()
    try:
        text = output.decode(errors='replace')
        assert f'1) {first} — 0 attached — bash' in text, text
        os.write(master, b'1\r')
        assert wait_until(lambda: first in clients(), 15), clients()
        os.write(master, b'printf "%s" "$$" > /workspace/pick-pid\r')
        eventually(lambda: shell(name, 'cat /workspace/pick-pid 2>/dev/null || true'), pane(name, 'pane_pid', first))
        assert len(sessions(name)) == 2
    finally:
        process.terminate()
        process.wait(10)
        os.close(master)
    process, master, output = pick()
    try:
        os.write(master, b'\r')
        assert wait_until(lambda: len(sessions(name)) == 3, 15), sessions(name)
    finally:
        process.terminate()
        process.wait(10)
        os.close(master)
