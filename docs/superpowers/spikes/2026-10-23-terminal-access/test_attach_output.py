"""Only the native client's teardown message may be relabeled."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("terminal_attach", Path(__file__).with_name("terminal_attach.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class AttachOutputTest(unittest.TestCase):
    def test_prompt_is_emitted_immediately(self):
        stream = module.AttachOutput()
        self.assertEqual(stream.feed(b"deskyard $ "), b"deskyard $ ")

    def test_shell_output_and_terminal_sequences_remain_exact(self):
        source = b"\x1b[?1049hprompt: Bye from Zellij!\r\n\x1b[31mred\x1b[m"
        stream = module.AttachOutput()
        actual = b"".join(stream.feed(bytes([byte])) for byte in source) + stream.finish()
        self.assertEqual(actual, source)

    def test_split_native_detach_message_is_relabelled(self):
        source = b"\x1b[?1049hBye from Zellij!\r\nhello\x1b[?1049l\x1b[m\x1b[30;1HBye from Zellij!\r\n"
        stream = module.AttachOutput()
        actual = b"".join(stream.feed(bytes([byte])) for byte in source) + stream.finish()
        self.assertEqual(actual, source[:-len(b"Bye from Zellij!\r\n")] + b"Detached from Deskyard Terminal.\r\n")

    def test_whole_read_preserves_shell_text_before_teardown(self):
        source = b"\x1b[?1049hBye from Zellij!\r\n\x1b[?1049lBye from Zellij!\r\n"
        stream = module.AttachOutput()
        self.assertEqual(stream.feed(source) + stream.finish(), b"\x1b[?1049hBye from Zellij!\r\n\x1b[?1049lDetached from Deskyard Terminal.\r\n")

    def test_reentering_fullscreen_flushes_buffer_unchanged(self):
        source = b"\x1b[?1049hhello\x1b[?1049ltemporary\x1b[?1049hprompt"
        stream = module.AttachOutput()
        actual = stream.feed(source) + stream.finish()
        self.assertEqual(actual, source)


if __name__ == "__main__":
    unittest.main()
