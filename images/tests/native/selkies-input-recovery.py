"""Exercise the installed input backend against a private, real X server."""

import asyncio
import os
import subprocess
import unittest

from selkies import input_handler as input_module
from selkies.Xlib import X, display


class InputRecovery(unittest.IsolatedAsyncioTestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = subprocess.Popen(
            ["Xvfb", "-displayfd", "1", "-screen", "0", "640x480x24", "-noreset", "-ac", "-nolisten", "tcp"],
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True,
        )
        cls.previous_display = os.environ.get("DISPLAY")
        os.environ["DISPLAY"] = ":" + cls.server.stdout.readline().strip()
        # Same timeout path as production, shortened so the outage is deterministic.
        cls.previous_timeout = input_module.INPUT_X_REPLY_TIMEOUT_S
        input_module.INPUT_X_REPLY_TIMEOUT_S = 0.15

    @classmethod
    def tearDownClass(cls):
        input_module.INPUT_X_REPLY_TIMEOUT_S = cls.previous_timeout
        if cls.previous_display is None:
            os.environ.pop("DISPLAY", None)
        else:
            os.environ["DISPLAY"] = cls.previous_display
        cls.server.terminate()
        cls.server.wait(timeout=5)
        cls.server.stdout.close()

    async def asyncSetUp(self):
        self.observer = display.Display()
        self.handler = input_module.WebRTCInput(None, enable_cursors=False)
        self.handler.num_gamepads = 0
        await self.handler.connect()

    async def asyncTearDown(self):
        self.observer.ungrab_server()
        self.observer.sync()
        await self.handler.disconnect()
        self.observer.close()

    async def wait_for_input(self):
        async with asyncio.timeout(4):
            while self.handler.xdisplay is None or self.handler.keyboard is None or self.handler.mouse is None:
                await asyncio.sleep(0.02)

    async def wait_for_reconnect_worker(self):
        worker = self.handler._x_reconnect_thread
        async with asyncio.timeout(3):
            while worker.is_alive():
                await asyncio.sleep(0.01)
        await asyncio.sleep(0)  # Deliver any callback queued by that worker.

    async def assert_input_delivered(self):
        window = self.observer.screen().root.create_window(
            0, 0, 300, 200, 0, self.observer.screen().root_depth,
            event_mask=X.KeyPressMask | X.KeyReleaseMask | X.ButtonPressMask | X.ButtonReleaseMask,
        )
        try:
            window.map()
            window.set_input_focus(X.RevertToParent, X.CurrentTime)
            self.observer.sync()
            await self.handler.send_x11_mouse(80, 90, 1, 0)
            await self.handler.send_x11_mouse(80, 90, 0, 0)
            await self.handler.send_x11_keypress(ord("a"), True)
            await self.handler.send_x11_keypress(ord("a"), False)
            self.handler.xdisplay.sync()
            self.observer.sync()
            events = []
            while self.observer.pending_events():
                events.append(self.observer.next_event())
            self.assertEqual(
                [(e.type, e.detail) for e in events if e.type in (X.KeyPress, X.KeyRelease)],
                [(X.KeyPress, self.observer.keysym_to_keycode(ord("a"))),
                 (X.KeyRelease, self.observer.keysym_to_keycode(ord("a")))],
            )
            self.assertEqual(
                [(e.type, e.detail, e.event_x, e.event_y) for e in events
                 if e.type in (X.ButtonPress, X.ButtonRelease)],
                [(X.ButtonPress, 1, 80, 90), (X.ButtonRelease, 1, 80, 90)],
            )
        finally:
            window.destroy()
            self.observer.sync()

    async def test_retries_after_first_reconnect_times_out(self):
        self.observer.grab_server()
        self.observer.sync()
        try:
            self.handler._reconnect_xdisplay()
            await self.wait_for_reconnect_worker()
            self.assertIsNone(self.handler.xdisplay)
        finally:
            self.observer.ungrab_server()
            self.observer.sync()
        await self.wait_for_input()
        await self.assert_input_delivered()

    async def test_event_reader_recovers_closed_connection(self):
        old = self.handler.xdisplay
        old.close()
        async with asyncio.timeout(4):
            while self.handler.xdisplay is old or self.handler.xdisplay is None:
                task = self.handler.keymap_watch_task
                if task.done():
                    task.result()  # Surface an uncaught X error in the event reader.
                await asyncio.sleep(0.02)
        await self.wait_for_input()
        await self.assert_input_delivered()

    async def test_recovers_when_initial_connection_times_out(self):
        await self.handler.disconnect()
        self.observer.grab_server()
        self.observer.sync()
        try:
            await self.handler.connect()
            self.assertIsNone(self.handler.xdisplay)
        finally:
            self.observer.ungrab_server()
            self.observer.sync()
        await self.wait_for_input()
        await self.assert_input_delivered()

    async def test_disconnect_rejects_inflight_reconnect(self):
        self.observer.grab_server()
        self.observer.sync()
        self.handler._reconnect_xdisplay()
        await self.handler.disconnect()
        self.observer.ungrab_server()
        self.observer.sync()
        await self.wait_for_reconnect_worker()
        self.assertIsNone(self.handler.xdisplay, "shutdown must not install a late X connection")
        self.assertIsNone(self.handler.keyboard)
        self.assertIsNone(self.handler.mouse)


if __name__ == "__main__":
    unittest.main()
