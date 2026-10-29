import time


from sandbox import REPO, docker

PANEL = "xfconf-query -c xfce4-panel -p /panels/panel-1"
TWO_PANEL_LAYOUT = REPO / "fixtures" / "two-panel-layout.xml"
SAVED_PANEL = "/workspace/.config/xfce4/xfconf/xfce-perchannel-xml/xfce4-panel.xml"


def test_single_full_width_taskbar_at_bottom(sandbox):
    """One always-visible taskbar across the bottom holds the menu, launchers and windows."""
    assert sandbox.exec("xfconf-query -c xfce4-panel -p /panels | grep -E '^[0-9]+$'").stdout.split() == ["1"]
    assert sandbox.exec(f"{PANEL}/position").stdout.strip() == "p=8;x=0;y=0"
    assert sandbox.exec(f"{PANEL}/length").stdout.strip() == "100"
    assert sandbox.exec(f"{PANEL}/autohide-behavior").stdout.strip() == "0"
    types = sandbox.exec(PANEL_TYPES).stdout.split()
    assert types[0] == "applicationsmenu" and types[-1] == "showdesktop", types
    assert types.index("launcher") < types.index("tasklist"), types


def test_terminal_opens_in_home(sandbox):
    """Apps started from the desktop (panel, menu) inherit the session's folder."""
    for proc in ("xfce4-session", "xfce4-panel", "xfdesktop"):
        assert sandbox.exec(f"readlink /proc/$(pgrep -o {proc})/cwd").stdout.strip() == "/workspace", proc


def test_terminal_launched_from_panel_starts_in_home(sandbox):
    # The panel's terminal launcher runs exo-open; emulate a click on it via the panel's environment.
    pid = sandbox.exec("pgrep -o xfce4-panel").stdout.strip()
    sandbox.exec(f"cd \"$(readlink /proc/{pid}/cwd)\" && setsid -f xfce4-terminal --title=cwd-probe >/dev/null 2>&1")
    deadline = time.time() + 15
    while time.time() < deadline:
        out = sandbox.exec("tmux -S /run/workspace/deskyard-terminal/tmux.sock list-panes -a -F '#{pane_pid}' 2>/dev/null || true").stdout.strip()
        if out:
            assert sandbox.exec(f"readlink /proc/{out}/cwd").stdout.strip() == "/workspace"
            return
        time.sleep(1)
    raise AssertionError("terminal shell did not start")


def test_existing_two_panel_home_becomes_single_taskbar_once(make_sandbox, volumes, image):
    a = make_sandbox()
    a.stop()
    a.remove()
    # A home saved by an older image: top panel plus autohiding dock, no markers.
    docker("run", "--rm", "-u", "0", "-v", f"{volumes.home}:/workspace",
           "-v", f"{TWO_PANEL_LAYOUT}:/tmp/old.xml:ro", "--entrypoint", "sh", image, "-c",
           f"cp /tmp/old.xml {SAVED_PANEL} && chown 1000:1000 {SAVED_PANEL}"
           " && rm -rf /workspace/.config/workspace")
    b = make_sandbox()
    assert b.exec("xfconf-query -c xfce4-panel -p /panels | grep -E '^[0-9]+$'").stdout.split() == ["1"]
    assert b.exec(f"{PANEL}/position").stdout.strip() == "p=8;x=0;y=0"
    types = b.exec(PANEL_TYPES).stdout.split()
    assert types[0] == "applicationsmenu" and types[-1] == "showdesktop", types
    assert types.count("launcher") == 4 and "directorymenu" not in types and "actions" not in types, types
    assert {"tasklist", "systray", "clock"} <= set(types) and "pager" not in types, types
    # A later choice by the user sticks: a smaller taskbar.
    b.exec(f"{PANEL}/size -s 30")
    b.stop()
    b.remove()
    c = make_sandbox()
    assert c.exec(f"{PANEL}/size").stdout.strip() == "30"


def test_existing_home_dock_terminal_becomes_deskyard_terminal(make_sandbox):
    """A home whose taskbar kept the plain terminal launcher gets the Deskyard Terminal once."""
    a = make_sandbox()
    launcher = a.exec("grep -l '^Exec=deskyard terminal-pick' ~/.config/xfce4/panel/launcher-*/*.desktop").stdout.split()
    assert len(launcher) == 1, launcher
    # As an older image left it.
    a.exec(f"sed -i 's|^Exec=.*|Exec=exo-open --launch TerminalEmulator|' {launcher[0]}"
           " && rm -f ~/.config/workspace/deskyard-terminal-launcher")
    a.restart()
    assert a.exec(f"grep '^Exec=' {launcher[0]}").stdout.strip() == "Exec=deskyard terminal-pick"
    # Once only: a launcher the user changes afterwards stays.
    a.exec(f"sed -i 's|^Exec=.*|Exec=xterm|' {launcher[0]}")
    a.restart()
    assert a.exec(f"grep '^Exec=' {launcher[0]}").stdout.strip() == "Exec=xterm"


def test_exec_starts_in_home(sandbox):
    assert sandbox.exec("pwd", login=False).stdout.strip() == "/workspace"
    # Shell pwd can use PWD and hide a cwd outside the destination mount tree.
    # Native getcwd (used by uv and other tools) must resolve it too.
    assert sandbox.exec("python3 -c 'import os; print(os.getcwd())'", login=False).stdout.strip() == "/workspace"


def test_nothing_opens_at_start(sandbox):
    """The desktop starts empty: Chromium and terminals only open when someone opens them."""
    time.sleep(5)  # past the session's autostart
    for cls in ("xfce4-terminal", "chromium"):
        assert sandbox.exec(f"xdotool search --onlyvisible --class {cls}", check=False).returncode != 0, cls
    # Not even in the background.
    assert sandbox.exec("pgrep -f '[c]hromium-shared'", check=False).returncode != 0, "Chromium runs"
    assert sandbox.exec("pgrep -x xfce4-terminal", check=False).returncode != 0, "a terminal runs"


# Chromium's frame bottom, the work area's bottom and the screen height.
CHROMIUM_FRAME_BOTTOM = r"""
w=$(xdotool search --onlyvisible --class chromium | head -1)
eval "$(xdotool getwindowgeometry --shell "$w")"
bottom=$(xprop -id "$w" _NET_FRAME_EXTENTS | sed -E 's/.*= *[0-9]+, *[0-9]+, *[0-9]+, *([0-9]+).*/\1/')
area=$(xprop -root _NET_WORKAREA | sed -E 's/.*= *[0-9]+, *([0-9]+), *[0-9]+, *([0-9]+).*/\1 \2/')
echo $((Y + HEIGHT + bottom)) $((${area% *} + ${area#* })) $(xdotool getdisplaygeometry | cut -d' ' -f2)
"""


def test_chromium_stays_above_taskbar_at_boot(sandbox):
    """At boot Chromium (open when the desktop stopped, so it comes back) must not
    maximize before the taskbar reserves its space."""
    sandbox.exec("curl -fs --max-time 60 -o /dev/null http://127.0.0.1:9221/json/version")
    for boot in range(3):
        sandbox.restart()
        deadline = time.time() + 20
        while sandbox.exec("xdotool search --onlyvisible --class chromium", check=False).returncode != 0:
            assert time.time() < deadline, "no Chromium window"
            time.sleep(1)
        time.sleep(2)  # where the window manager leaves it
        frame_bottom, workarea_bottom, screen = map(int, sandbox.exec(CHROMIUM_FRAME_BOTTOM).stdout.split())
        assert workarea_bottom < screen, "taskbar reserved no space"
        assert frame_bottom <= workarea_bottom, f"boot {boot}: window ends at y={frame_bottom}, taskbar starts at {workarea_bottom}"


def test_dock_web_browser_opens_shared_chromium_without_asking(sandbox):
    sandbox.exec("echo '<title>helper-probe</title>' > ~/helper.html")
    sandbox.exec("setsid -f exo-open --launch WebBrowser file:///workspace/helper.html >/dev/null 2>&1")
    deadline = time.time() + 20
    while "helper-probe" not in sandbox.exec("curl -s http://127.0.0.1:9221/json/list").stdout:
        assert time.time() < deadline, "exo-open did not open the page in the shared Chromium"
        time.sleep(1)
    assert sandbox.exec("xdotool search --onlyvisible --name 'Choose Preferred Application'", check=False).returncode != 0


def test_links_from_apps_open_in_chromium(sandbox):
    for mime in ("x-scheme-handler/http", "x-scheme-handler/https", "text/html"):
        assert sandbox.exec(f"xdg-mime query default {mime}").stdout.strip() == "chromium.desktop", mime


PANEL_TYPES = r"""
for id in $(xfconf-query -c xfce4-panel -p /panels/panel-1/plugin-ids | grep -E '^[0-9]+$'); do
  xfconf-query -c xfce4-panel -p /plugins/plugin-$id
done
"""


def test_taskbar_has_clock_and_no_switcher(sandbox, artifacts_dir):
    types = sandbox.exec(PANEL_TYPES).stdout.split()
    assert "actions" not in types, types
    assert {"applicationsmenu", "tasklist", "clock"} <= set(types) and "pager" not in types, types
    (artifacts_dir / "taskbar.png").write_bytes(sandbox.exec_bytes("deskyard zoom 0 760 1280 800"))


def set_top_panel(box, kinds):
    """Make panel-1 hold plugins of the given types, ids 1..n."""
    for i, kind in enumerate(kinds, 1):
        box.exec(f"xfconf-query -c xfce4-panel -p /plugins/plugin-{i} -n -t string -s {kind}")
    box.exec("xfconf-query -c xfce4-panel -p /panels/panel-1/plugin-ids --force-array "
             + " ".join(f"-t int -s {i}" for i in range(1, len(kinds) + 1)))


def test_existing_home_gets_top_panel_trimmed_once(make_sandbox, volumes, image):
    a = make_sandbox()
    # A home from an older image: the full Debian top panel, no migration markers.
    set_top_panel(a, ["applicationsmenu", "tasklist", "separator", "pager", "separator",
                      "systray", "separator", "clock", "separator", "actions"])
    a.stop()
    a.remove()
    docker("run", "--rm", "-v", f"{volumes.home}:/workspace", "--entrypoint", "rm", image,
           "-f", "/workspace/.config/workspace/migrated-top-panel",
           "/workspace/.config/workspace/restored-top-panel")
    b = make_sandbox()
    types = b.exec(PANEL_TYPES).stdout.split()
    assert "actions" not in types and "pager" in types and "clock" in types, types
    # A later choice sticks: drop the pager.
    b.exec("xfconf-query -c xfce4-panel -p /panels/panel-1/plugin-ids --force-array "
           + " ".join(f"-t int -s {i}" for i in (1, 2, 3, 6, 8)))
    b.stop()
    b.remove()
    c = make_sandbox()
    types = c.exec(PANEL_TYPES).stdout.split()
    assert "pager" not in types and "clock" in types, types


def test_home_trimmed_by_older_image_gets_clock_back(make_sandbox, volumes, image):
    a = make_sandbox()
    # Trimmed by an older image: ids 1,2,3,6 only; trim marker present, restore marker absent.
    a.exec("xfconf-query -c xfce4-panel -p /panels/panel-1/plugin-ids --force-array "
           + " ".join(f"-t int -s {i}" for i in (1, 2, 3, 6)))
    a.stop()
    a.remove()
    docker("run", "--rm", "-v", f"{volumes.home}:/workspace", "--entrypoint", "sh", image, "-c",
           "mkdir -p /workspace/.config/workspace && touch /workspace/.config/workspace/migrated-top-panel"
           " && rm -f /workspace/.config/workspace/restored-top-panel")
    b = make_sandbox()
    types = b.exec(PANEL_TYPES).stdout.split()
    assert "clock" in types and "actions" not in types, types
    # The user removes the clock; it stays removed.
    ids = b.exec("xfconf-query -c xfce4-panel -p /panels/panel-1/plugin-ids | grep -E '^[0-9]+$'").stdout.split()
    keep = [i for i in ids if b.exec(f"xfconf-query -c xfce4-panel -p /plugins/plugin-{i}").stdout.strip() != "clock"]
    b.exec("xfconf-query -c xfce4-panel -p /panels/panel-1/plugin-ids --force-array "
           + " ".join(f"-t int -s {i}" for i in keep))
    b.stop()
    b.remove()
    c = make_sandbox()
    types = c.exec(PANEL_TYPES).stdout.split()
    assert "clock" not in types, types


# The first file with a given name wins (XDG_DATA_DIRS order); later ones are shadowed.
VISIBLE_MENU_NAMES = r"""
declare -A seen
for d in /workspace/.local/share/applications /usr/local/share/applications /usr/share/applications; do
  for f in "$d"/*.desktop; do
    [ -f "$f" ] || continue
    [ -n "${seen[${f##*/}]:-}" ] && continue
    seen[${f##*/}]=1
    grep -qE '^(NoDisplay|Hidden)=true' "$f" && continue
    grep -m1 '^Name=' "$f"
  done
done
"""


def test_no_mail_reader_in_menu(sandbox):
    names = sandbox.exec(VISIBLE_MENU_NAMES).stdout.splitlines()
    assert "Name=Mail Reader" not in names, [n for n in names if "Mail" in n]


def test_existing_home_at_stock_look_gets_arc_once(make_sandbox, volumes, image):
    a = make_sandbox()
    # A home from an image before the Arc look: XFCE's stock values saved, no marker.
    a.exec("xfconf-query -c xsettings -p /Net/ThemeName -s Xfce"
           " && xfconf-query -c xsettings -p /Net/IconThemeName -s Tango"
           " && xfconf-query -c xfwm4 -p /general/theme -n -t string -s Default")
    a.stop()
    a.remove()
    docker("run", "--rm", "-v", f"{volumes.home}:/workspace", "--entrypoint", "rm", image,
           "-f", "/workspace/.config/workspace/arc-look")
    b = make_sandbox()
    look = "xfconf-query -c xsettings -p /Net/ThemeName; xfconf-query -c xsettings -p /Net/IconThemeName; xfconf-query -c xfwm4 -p /general/theme"
    assert b.exec(look).stdout.split() == ["Arc-Dark", "Papirus-Dark", "Arc-Dark"]
    # A later choice by the user sticks.
    b.exec("xfconf-query -c xsettings -p /Net/ThemeName -s Greybird")
    b.stop()
    b.remove()
    c = make_sandbox()
    assert c.exec("xfconf-query -c xsettings -p /Net/ThemeName").stdout.strip() == "Greybird"
