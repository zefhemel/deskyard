"""The stream timing test's observer, without a supervisor or Docker.

    .venv/bin/python -m unittest test_frame_counter
"""
import unittest

from playwright.sync_api import sync_playwright

from test_ui_stream import COUNT_FRAMES


class FrameCounterTests(unittest.TestCase):
    def test_already_displayed_frame_counts_without_waiting_for_another(self):
        with sync_playwright() as p:
            browser = p.chromium.launch()
            try:
                page = browser.new_page()
                page.set_content("<iframe></iframe>")
                frame = page.frames[1]
                frame.evaluate("""async () => {
                    const canvas = document.createElement('canvas');
                    canvas.width = canvas.height = 32;
                    const video = document.createElement('video');
                    video.muted = true;
                    document.body.append(video);
                    const stream = canvas.captureStream(0);
                    video.srcObject = stream;
                    window.frameTrack = stream.getVideoTracks()[0];
                    window.frameCanvas = canvas;
                    const shown = new Promise(resolve => video.requestVideoFrameCallback(resolve));
                    const playing = video.play();
                    canvas.getContext('2d').fillRect(0, 0, 32, 32);
                    frameTrack.requestFrame();
                    await Promise.race([
                        Promise.all([playing, shown]),
                        new Promise((_, reject) => setTimeout(() => reject(Error('first frame not shown')), 5000))
                    ]);
                    window.deskyardScreen = {video: () => video};
                }""")
                frame.evaluate(COUNT_FRAMES)
                frame.wait_for_function("window.__frames > 0", timeout=1000)
                before = frame.evaluate("window.__frames")
                frame.evaluate("frameCanvas.getContext('2d').clearRect(0, 0, 32, 32); frameTrack.requestFrame()")
                frame.wait_for_function("before => window.__frames > before", arg=before, timeout=1000)
            finally:
                browser.close()


if __name__ == "__main__":
    unittest.main()
