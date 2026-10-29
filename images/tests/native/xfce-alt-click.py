"""Private Xvfb/XFWM regression: never injects into the user's display.

Run with /opt/selkies/bin/python; optional arguments select source-tree XDG
configuration and migration script instead of the installed image files.
"""

import os
import subprocess
import tempfile
import time
import sys

if len(sys.argv) == 1 or sys.argv[1] != "private":
    with tempfile.TemporaryDirectory(prefix="deskyard-alt-wm-") as tmp:
        env = {
            **os.environ,
            "HOME": tmp,
            "XDG_CONFIG_HOME": tmp + "/config",
            "XDG_CACHE_HOME": tmp + "/cache",
        }
        if len(sys.argv) > 1:
            env["XDG_CONFIG_DIRS"] = sys.argv[1]
        env["DESKYARD_ALT_MIGRATION"] = (
            sys.argv[2]
            if len(sys.argv) > 2
            else "/usr/local/bin/workspace-migrate-window-modifiers"
        )
        for k in ["DBUS_SESSION_BUS_ADDRESS", "SESSION_MANAGER"]:
            env.pop(k, None)
        try:
            subprocess.run(
                ["dbus-run-session", "--", sys.executable, __file__, "private"],
                env=env,
                check=True,
            )
        finally:
            # Let xfconfd finish flushing the private home after bus shutdown.
            time.sleep(0.3)
    sys.exit()
from selkies.Xlib import X, display, XK
from selkies.Xlib.ext import xtest

xvfb = subprocess.Popen(
    ["Xvfb", "-displayfd", "1", "-screen", "0", "800x600x24", "-nolisten", "tcp"],
    stdout=subprocess.PIPE,
    stderr=subprocess.DEVNULL,
    text=True,
)
wm = None
try:
    os.environ["DISPLAY"] = ":" + xvfb.stdout.readline().strip()
    d = display.Display()
    root = d.screen().root
    w = root.create_window(
        100,
        100,
        350,
        250,
        0,
        d.screen().root_depth,
        X.InputOutput,
        X.CopyFromParent,
        background_pixel=0xFFFFFF,
        event_mask=X.ButtonPressMask | X.ButtonReleaseMask,
    )
    w.set_wm_name("Private Alt-click regression")
    w.map()
    d.sync()
    wm = subprocess.Popen(
        ["xfwm4", "--compositor=off"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    atom = d.intern_atom("_NET_SUPPORTING_WM_CHECK")
    for _ in range(100):
        if root.get_full_property(atom, X.AnyPropertyType):
            break
        time.sleep(0.05)
    else:
        raise RuntimeError("private window manager did not start")
    time.sleep(0.3)

    def setting():
        return subprocess.check_output(
            ["xfconf-query", "-c", "xfwm4", "-p", "/general/easy_click"], text=True
        ).strip()

    def set_modifier(value):
        subprocess.run(
            ["xfconf-query", "-c", "xfwm4", "-p", "/general/easy_click", "-s", value],
            check=True,
        )
        time.sleep(0.2)

    def alt_click():
        subprocess.run(
            ["xdotool", "mousemove", "--window", str(w.id), "80", "80"], check=True
        )
        d.sync()
        while d.pending_events():
            d.next_event()
        kc = d.keysym_to_keycode(XK.string_to_keysym("Alt_L"))
        xtest.fake_input(d, X.KeyPress, kc)
        d.sync()
        time.sleep(0.05)
        xtest.fake_input(d, X.ButtonPress, 1)
        d.sync()
        time.sleep(0.05)
        xtest.fake_input(d, X.ButtonRelease, 1)
        xtest.fake_input(d, X.KeyRelease, kc)
        d.sync()
        time.sleep(0.1)
        events = []
        while d.pending_events():
            e = d.next_event()
            if e.type in (X.ButtonPress, X.ButtonRelease):
                events.append((e.type, e.state))
        return events

    def assert_alt_click():
        events = alt_click()
        assert [kind for kind, state in events] == [X.ButtonPress, X.ButtonRelease], (
            events
        )
        assert all(state & X.Mod1Mask for kind, state in events), events

    assert_alt_click()  # Shipped defaults must deliver Alt-click to the application.
    assert setting() == "Super", setting()
    set_modifier("Alt")
    assert alt_click() == [], (
        "control: XFWM must reproduce the original swallowed click"
    )
    migration = os.environ["DESKYARD_ALT_MIGRATION"]
    subprocess.run(["bash", migration], check=True)
    time.sleep(0.2)
    assert setting() == "Super", setting()
    assert_alt_click()
    # A later explicit user choice must survive future session starts.
    set_modifier("Alt")
    subprocess.run(["bash", migration], check=True)
    assert setting() == "Alt", "migration ran more than once"
    # An existing non-default preference must not be overwritten on first run.
    from pathlib import Path

    Path(os.environ["HOME"], ".config/workspace/window-modifiers-v1").unlink()
    set_modifier("Control")
    subprocess.run(["bash", migration], check=True)
    assert setting() == "Control", "migration overwrote a custom modifier"
    print(
        "PASS: native Alt-click, default migration, custom preference and once-only behavior",
        flush=True,
    )
    d.close()
finally:
    if wm:
        wm.terminate()
        wm.wait(timeout=5)
    xvfb.terminate()
    xvfb.wait(timeout=5)
