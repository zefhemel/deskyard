"""The selected MATE defaults and the user's later appearance choices."""

import os
import json
import time

import pytest

pytestmark = pytest.mark.skipif(os.environ.get('AC_DESKTOP', 'xfce') != 'mate', reason='MATE appearance')


def test_mate_uses_selected_appearance(sandbox, artifacts_dir):
    assert sandbox.exec('gsettings get org.mate.interface gtk-theme').stdout.strip() == "'Arc'"
    assert sandbox.exec('gsettings get org.mate.interface icon-theme').stdout.strip() == "'Papirus'"
    assert sandbox.exec('gsettings get org.mate.panel toplevel-id-list').stdout.strip() == "['bottom']"
    assert sandbox.exec('gsettings get org.mate.panel.toplevel:/org/mate/panel/toplevels/bottom/ size').stdout.strip() == '40'
    assert sandbox.exec('gsettings get org.mate.background picture-filename').stdout.strip() == "'/usr/share/backgrounds/deskyard/mate.svg'"
    for entry in ('/usr/share/applications/caja-browser.desktop', '/usr/local/share/applications/chromium.desktop', '/usr/share/applications/org.gnome.Software.desktop'):
        sandbox.exec(f'test -f {entry}')
    assert 'mate-panel-menu-bar' in sandbox.exec('cat ~/.config/gtk-3.0/gtk.css').stdout
    # Exercise the actual Files launcher, not only its saved configuration.
    sandbox.exec('xdotool mousemove 64 780 click 1')
    deadline = time.monotonic() + 20
    while sandbox.exec("xdotool search --onlyvisible --name '^workspace$'", check=False).returncode != 0:
        assert time.monotonic() < deadline, 'Files launcher did not open the home folder'
        time.sleep(0.5)
    sandbox.exec('xdotool mousemove 1260 700')
    (artifacts_dir / 'mate-defaults.png').write_bytes(sandbox.exec_bytes('deskyard screenshot'))


def test_mate_preserves_user_appearance_on_restart(sandbox):
    sandbox.exec("gsettings set org.mate.interface gtk-theme Adwaita"
                 " && gsettings set org.mate.interface icon-theme Adwaita"
                 " && gsettings set org.mate.panel.toplevel:/org/mate/panel/toplevels/bottom/ size 32"
                 " && mkdir -p ~/.config/gtk-3.0"
                 " && printf '/* personal stylesheet */\\n' > ~/.config/gtk-3.0/gtk.css")
    sandbox.exec("gsettings set org.mate.panel toplevel-id-list \"['top','bottom']\"")
    sandbox.restart()
    assert sandbox.exec('gsettings get org.mate.interface gtk-theme').stdout.strip() == "'Adwaita'"
    assert sandbox.exec('gsettings get org.mate.interface icon-theme').stdout.strip() == "'Adwaita'"
    assert sandbox.exec('gsettings get org.mate.panel.toplevel:/org/mate/panel/toplevels/bottom/ size').stdout.strip() == '32'
    assert sandbox.exec('gsettings get org.mate.panel toplevel-id-list').stdout.strip() == "['top', 'bottom']"
    assert sandbox.exec('cat ~/.config/gtk-3.0/gtk.css').stdout == '/* personal stylesheet */\n'


def test_panel_browser_uses_shared_profile(sandbox):
    sandbox.exec('xdotool mousemove 106 780 click 1')
    deadline = time.monotonic() + 20
    while sandbox.exec("pgrep -f '[c]hromium-shared'", check=False).returncode != 0:
        assert time.monotonic() < deadline, 'Panel browser bypassed the shared profile'
        time.sleep(0.5)
    # Compare the existing human browser's target with the agent endpoint.
    # Direct 9220 does not start Chromium, unlike the on-demand agent proxy.
    human = json.loads(sandbox.exec('curl -fs --retry 20 --retry-connrefused --retry-delay 1 --max-time 30 http://127.0.0.1:9220/json/version').stdout)
    agent = sandbox.cdp_json(timeout=30)
    assert human['webSocketDebuggerUrl'].rsplit('/', 1)[-1] == agent['webSocketDebuggerUrl'].rsplit('/', 1)[-1]
    assert sandbox.exec('xdotool search --onlyvisible --class chromium | wc -l').stdout.strip() == '1'
