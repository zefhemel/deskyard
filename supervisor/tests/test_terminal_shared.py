"""Real CLI, native launcher and owned browser share one persistent shell."""
import os
import pty
import signal
import subprocess
import threading
import uuid

import pytest
from playwright.sync_api import expect, sync_playwright

from helpers import in_desktop, signed_in, wait_until
from test_terminal_transport import eventually, shell
from test_ui_terminal import command, connected, pane, tab_name


def test_browser_cli_and_native_launcher_share_shell(supervisor, name, desktop, cli, remote):
    nonce = uuid.uuid4().hex
    master, slave = pty.openpty()
    process = None
    first_output = threading.Event()

    def read_output():
        try:
            while os.read(master, 65536):
                first_output.set()
        except OSError:
            pass

    try:
        with sync_playwright() as p:
            browser, page = signed_in(p, supervisor.base)
            try:
                # Use the installed .desktop entry and its Terminal=true handling.
                subprocess.run(['docker', 'exec', '-d', '-u', 'user', '-e', 'DISPLAY=:1',
                                f'deskyard-{name}', 'xfce4-appfinder', '--disable-server'], check=True)
                assert wait_until(lambda: in_desktop(name, 'xdotool', 'search', '--onlyvisible',
                                                     '--class', 'xfce4-appfinder', check=False).strip()), 'Application finder did not open'
                in_desktop(name, 'xdotool', 'type', '--clearmodifiers', '--delay', '30', 'Deskyard Terminal')
                in_desktop(name, 'xdotool', 'key', 'Return')
                def windows():
                    return in_desktop(name, 'xdotool', 'search', '--onlyvisible', '--class', 'xfce4-terminal', check=False).splitlines()
                assert wait_until(windows), 'Installed Deskyard Terminal launcher did not open a terminal'
                def native_sessions():
                    response = page.request.get(f'{supervisor.base}/api/computers/{name}/terminal/sessions')
                    assert response.status == 200, response.text()
                    return [entry['name'] for entry in response.json()['sessions']]
                assert wait_until(lambda: len(native_sessions()) == 1), 'Native session not discoverable'
                session = native_sessions()[0]
                assert session.startswith('terminal-')
                # The native session is a tab; a link to it joins it.
                page.goto(f'{supervisor.base}/{name}/')
                page.get_by_role('tab', name=tab_name(session)).click()
                connected(page)
                assert page.url.endswith(f'?terminal={session}')
                command(page, f'export SHARED_PROOF={nonce}; printf "%s" "$$" > /workspace/shared-browser-pid')
                eventually(lambda: shell(name, 'cat /workspace/shared-browser-pid 2>/dev/null || true').isdigit(), True)
                pid = shell(name, 'cat /workspace/shared-browser-pid')
                process = subprocess.Popen([cli, '-c', name, 'terminal', session], env=remote.env,
                                           stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
                os.close(slave)
                slave = None
                threading.Thread(target=read_output, daemon=True).start()
                assert first_output.wait(20), 'CLI never rendered tmux output'
                assert native_sessions() == [session], 'Private browser/CLI groups leaked into discovery'
                os.write(master, b'printf "%s" "$SHARED_PROOF:$$" > /workspace/shared-cli-proof\r')
                eventually(lambda: shell(name, 'cat /workspace/shared-cli-proof 2>/dev/null || true'), f'{nonce}:{pid}')

                in_desktop(name, 'xdotool', 'windowactivate', '--sync', windows()[-1])
                in_desktop(name, 'xdotool', 'type', '--clearmodifiers', '--delay', '20',
                           'printf "%s" "$SHARED_PROOF:$$" > /workspace/shared-native-proof')
                in_desktop(name, 'xdotool', 'key', 'Return')
                eventually(lambda: shell(name, 'cat /workspace/shared-native-proof 2>/dev/null || true'), f'{nonce}:{pid}')

                os.write(master, b'\x02d')
                assert process.wait(timeout=15) == 0
                command(page, 'sleep 1; printf "%s" "$SHARED_PROOF:$$" > /workspace/shared-persisted &')
                page.reload()
                connected(page)
                eventually(lambda: shell(name, 'cat /workspace/shared-persisted 2>/dev/null || true'), f'{nonce}:{pid}')
                assert pane(name, 'pane_pid', session) == pid
            finally:
                browser.close()
    finally:
        if process and process.poll() is None:
            process.send_signal(signal.SIGTERM)
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        if slave is not None:
            os.close(slave)
        os.close(master)


def desktop_sessions(name):
    """The public sessions as the desktop itself lists them, by name."""
    lines = in_desktop(name, 'deskyard', 'terminal-list', check=False).splitlines()
    return [line.split('\t')[0] for line in lines if line]


@pytest.mark.serial  # a tab within seconds of the session
def test_native_session_tab_follows_promptly(supervisor, name, desktop):
    """The desktop's tmux hooks report changes: no waiting for the slow poll."""
    with sync_playwright() as p:
        browser, page = signed_in(p, supervisor.base)
        try:
            page.goto(f'{supervisor.base}/{name}/')
            expect(page.get_by_role('tab', name='Desktop')).to_be_visible(timeout=15000)
            page.wait_for_timeout(2000)  # the supervisor follows the desktop's stream once someone looks
            subprocess.run(['docker', 'exec', '-d', '-u', 'user', '-e', 'DISPLAY=:1',
                            f'deskyard-{name}', 'xfce4-appfinder', '--disable-server'], check=True)
            assert wait_until(lambda: in_desktop(name, 'xdotool', 'search', '--onlyvisible',
                                                 '--class', 'xfce4-appfinder', check=False).strip()), 'Application finder did not open'
            in_desktop(name, 'xdotool', 'type', '--clearmodifiers', '--delay', '30', 'Deskyard Terminal')
            in_desktop(name, 'xdotool', 'key', 'Return')
            sessions = wait_until(lambda: desktop_sessions(name), every=0.05)
            assert sessions, 'Deskyard Terminal launcher opened no session'
            session = sessions[0]
            tab = page.get_by_role('tab', name=tab_name(session))
            # The old 10 s poll can't pass this; the hook-driven refresh takes well under a second.
            expect(tab).to_be_visible(timeout=3000)

            window = in_desktop(name, 'xdotool', 'search', '--onlyvisible', '--class', 'xfce4-terminal').split()[-1]
            in_desktop(name, 'xdotool', 'windowactivate', '--sync', window)
            in_desktop(name, 'xdotool', 'type', '--clearmodifiers', '--delay', '20', 'exit')
            in_desktop(name, 'xdotool', 'key', 'Return')
            assert wait_until(lambda: session not in desktop_sessions(name), every=0.05), 'exit did not end the session'
            expect(tab).to_have_count(0, timeout=3000)
        finally:
            browser.close()
