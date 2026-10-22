import time

from sandbox import docker


def test_dock_always_visible(sandbox):
    """The bottom panel (dock) must not autohide behind the maximized Chromium."""
    assert sandbox.exec("xfconf-query -c xfce4-panel -p /panels/panel-2/autohide-behavior").stdout.strip() == "0"


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
        out = sandbox.exec("pgrep -n bash -P $(pgrep -n xfce4-terminal) || true").stdout.strip()
        if out:
            assert sandbox.exec(f"readlink /proc/{out}/cwd").stdout.strip() == "/workspace"
            return
        time.sleep(1)
    raise AssertionError("terminal shell did not start")


def test_existing_home_gets_dock_migration_once(make_sandbox, volumes, image):
    a = make_sandbox()
    # Simulate a home created by an older image: panel config saved with autohide on, no marker.
    a.exec("xfconf-query -c xfce4-panel -p /panels/panel-2/autohide-behavior -s 1")
    a.stop()  # xfconfd writes settings to disk on shutdown
    a.remove()
    docker("run", "--rm", "-v", f"{volumes.home}:/workspace", "--entrypoint", "rm", image,
           "-f", "/workspace/.config/workspace/migrated-dock-visible")
    b = make_sandbox()
    assert b.exec("xfconf-query -c xfce4-panel -p /panels/panel-2/autohide-behavior").stdout.strip() == "0"
    # A later choice by the user sticks.
    b.exec("xfconf-query -c xfce4-panel -p /panels/panel-2/autohide-behavior -s 2")
    b.stop()
    b.remove()
    c = make_sandbox()
    assert c.exec("xfconf-query -c xfce4-panel -p /panels/panel-2/autohide-behavior").stdout.strip() == "2"


def test_exec_starts_in_home(sandbox):
    assert sandbox.exec("pwd", login=False).stdout.strip() == "/workspace"


def test_nothing_opens_at_start(sandbox):
    """The desktop starts empty: Chromium and terminals only open when someone opens them."""
    time.sleep(5)
    for cls in ("xfce4-terminal", "chromium"):
        assert sandbox.exec(f"xdotool search --onlyvisible --class {cls}", check=False).returncode != 0, cls


CHROMIUM_FRAME_TOP = r"""
w=$(xdotool search --onlyvisible --class chromium | head -1)
eval "$(xdotool getwindowgeometry --shell "$w")"
top=$(xprop -id "$w" _NET_FRAME_EXTENTS | sed -E 's/.*= *[0-9]+, *[0-9]+, *([0-9]+),.*/\1/')
area=$(xprop -root _NET_WORKAREA | sed -E 's/.*= *[0-9]+, *([0-9]+),.*/\1/')
echo $((Y - top)) $area
"""


def test_chromium_title_bar_below_panel_at_boot(sandbox):
    """At boot Chromium (open when the desktop stopped, so it comes back) must not
    maximize before the top panel reserves its space."""
    sandbox.exec("curl -fs --max-time 60 -o /dev/null http://127.0.0.1:9221/json/version")
    sandbox.restart()
    for boot in range(3):
        deadline = time.time() + 20
        while sandbox.exec("xdotool search --onlyvisible --class chromium", check=False).returncode != 0:
            assert time.time() < deadline, "no Chromium window"
            time.sleep(1)
        time.sleep(2)
        frame_top, workarea_top = map(int, sandbox.exec(CHROMIUM_FRAME_TOP).stdout.split())
        assert workarea_top > 0, "panel reserved no space"
        assert frame_top >= workarea_top, f"boot {boot}: title bar at y={frame_top}, panel ends at {workarea_top}"
        sandbox.restart()


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


TOP_PANEL_TYPES = r"""
for id in $(xfconf-query -c xfce4-panel -p /panels/panel-1/plugin-ids | grep -E '^[0-9]+$'); do
  xfconf-query -c xfce4-panel -p /plugins/plugin-$id
done
"""


def test_top_panel_has_switcher_and_clock(sandbox, artifacts_dir):
    types = sandbox.exec(TOP_PANEL_TYPES).stdout.split()
    assert "actions" not in types, types
    assert {"applicationsmenu", "tasklist", "pager", "clock"} <= set(types), types
    (artifacts_dir / "top-panel.png").write_bytes(sandbox.exec_bytes("deskyard zoom 0 0 1280 40"))


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
    types = b.exec(TOP_PANEL_TYPES).stdout.split()
    assert "actions" not in types and "pager" in types and "clock" in types, types
    # A later choice sticks: drop the pager.
    b.exec("xfconf-query -c xfce4-panel -p /panels/panel-1/plugin-ids --force-array "
           + " ".join(f"-t int -s {i}" for i in (1, 2, 3, 6, 8)))
    b.stop()
    b.remove()
    c = make_sandbox()
    types = c.exec(TOP_PANEL_TYPES).stdout.split()
    assert "pager" not in types and "clock" in types, types


def test_home_trimmed_by_older_image_gets_switcher_and_clock_back(make_sandbox, volumes, image):
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
    types = b.exec(TOP_PANEL_TYPES).stdout.split()
    assert "pager" in types and "clock" in types and "actions" not in types, types
    # The user removes the clock; it stays removed.
    ids = b.exec("xfconf-query -c xfce4-panel -p /panels/panel-1/plugin-ids | grep -E '^[0-9]+$'").stdout.split()
    keep = [i for i in ids if b.exec(f"xfconf-query -c xfce4-panel -p /plugins/plugin-{i}").stdout.strip() != "clock"]
    b.exec("xfconf-query -c xfce4-panel -p /panels/panel-1/plugin-ids --force-array "
           + " ".join(f"-t int -s {i}" for i in keep))
    b.stop()
    b.remove()
    c = make_sandbox()
    types = c.exec(TOP_PANEL_TYPES).stdout.split()
    assert "clock" not in types and "pager" in types, types


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
