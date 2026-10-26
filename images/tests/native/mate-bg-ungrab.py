"""Exercise the installed libmate-desktop root-background calls against a private X server.

mate-settings-daemon draws the root background with these calls, which grab
the X server. Each must leave the server released when it returns, even when
the caller makes no further X request: a client that opens another X
connection before its next flush otherwise waits on its own grab forever.
"""

import ctypes
import os
import subprocess
import unittest


class GdkRGBA(ctypes.Structure):
    _fields_ = [("red", ctypes.c_double), ("green", ctypes.c_double),
                ("blue", ctypes.c_double), ("alpha", ctypes.c_double)]


class RootBackgroundUngrab(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = subprocess.Popen(
            ["Xvfb", "-displayfd", "1", "-screen", "0", "640x480x24", "-noreset", "-ac", "-nolisten", "tcp"],
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True,
        )
        cls.display = ":" + cls.server.stdout.readline().strip()
        os.environ["DISPLAY"] = cls.display
        gdk = ctypes.CDLL("libgdk-3.so.0")
        gdk.gdk_init(None, None)
        gdk.gdk_screen_get_default.restype = ctypes.c_void_p
        gdk.gdk_screen_get_root_window.restype = ctypes.c_void_p
        gdk.gdk_screen_get_root_window.argtypes = [ctypes.c_void_p]
        cls.screen = gdk.gdk_screen_get_default()
        cls.root = gdk.gdk_screen_get_root_window(cls.screen)

        bg = ctypes.CDLL("libmate-desktop-2.so.17")
        bg.mate_bg_new.restype = ctypes.c_void_p
        bg.mate_bg_set_color.argtypes = [ctypes.c_void_p, ctypes.c_int,
                                         ctypes.POINTER(GdkRGBA), ctypes.POINTER(GdkRGBA)]
        bg.mate_bg_create_surface.restype = ctypes.c_void_p
        bg.mate_bg_create_surface.argtypes = [ctypes.c_void_p, ctypes.c_void_p,
                                              ctypes.c_int, ctypes.c_int, ctypes.c_int]
        bg.mate_bg_set_surface_as_root.argtypes = [ctypes.c_void_p, ctypes.c_void_p]
        bg.mate_bg_set_surface_as_root_with_crossfade.restype = ctypes.c_void_p
        bg.mate_bg_set_surface_as_root_with_crossfade.argtypes = [ctypes.c_void_p, ctypes.c_void_p]
        cls.bg = bg

    @classmethod
    def tearDownClass(cls):
        cls.server.terminate()
        cls.server.wait(timeout=5)
        cls.server.stdout.close()

    def surface(self, blue):
        """A root-sized solid background, as the settings daemon's plugin makes one."""
        bg = self.bg.mate_bg_new()
        color = GdkRGBA(0.2, 0.5, blue, 1.0)
        self.bg.mate_bg_set_color(bg, 0, ctypes.byref(color), ctypes.byref(color))  # MATE_BG_COLOR_SOLID
        return self.bg.mate_bg_create_surface(bg, self.root, 640, 480, 1)

    def assert_server_released(self):
        # Another client, as the settings daemon's own second connection would be.
        probe = subprocess.run(["timeout", "3", "xprop", "-display", self.display, "-root", "_XROOTPMAP_ID"],
                               capture_output=True, text=True)
        self.assertEqual(probe.returncode, 0, "X server still grabbed after the call returned")

    def test_root_background_calls_release_server(self):
        # Made first: making one opens its own X connection, which a leaked grab would block.
        first, second = self.surface(0.8), self.surface(0.9)
        self.bg.mate_bg_set_surface_as_root(self.screen, first)
        self.assert_server_released()
        # Starting the fade draws its first frame at once, under a grab of its own.
        self.assertTrue(self.bg.mate_bg_set_surface_as_root_with_crossfade(self.screen, second))
        self.assert_server_released()


if __name__ == "__main__":
    unittest.main()
