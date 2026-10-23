#!/usr/bin/env python3
"""Native PTY attachment with a product-owned detach message.

The engine emulates pane output internally, so its alternate-screen teardown is
outside user shell output. Preserve all bytes while attached; only relabel the
final native goodbye after the client leaves its alternate screen.
"""
import errno
import fcntl
import os
import pty
import select
import signal
import sys
import termios
import tty

ENTER = b"\x1b[?1049h"
LEAVE = b"\x1b[?1049l"


class AttachOutput:
    def __init__(self):
        self.pending = b""
        self.teardown = None

    def feed(self, data):
        self.pending += data
        ready = bytearray()
        while self.pending:
            marker = ENTER if self.teardown is not None else LEAVE
            index = self.pending.find(marker)
            if index < 0:
                # Retain only the possible partial marker across reads.
                keep = 0
                for length in range(1, min(len(marker), len(self.pending) + 1)):
                    if self.pending.endswith(marker[:length]):
                        keep = length
                count = len(self.pending) - keep
                prefix, self.pending = self.pending[:count], self.pending[count:]
                if self.teardown is None:
                    ready.extend(prefix)
                else:
                    self.teardown += prefix
                break
            prefix = self.pending[:index + len(marker)]
            self.pending = self.pending[index + len(marker):]
            if self.teardown is None:
                ready.extend(prefix[:-len(marker)])
                self.teardown = marker
            else:
                ready.extend(self.teardown + prefix)
                self.teardown = None
        return bytes(ready)

    def finish(self):
        if self.teardown is None:
            return self.pending
        return (self.teardown + self.pending).replace(
            b"Bye from Zellij!", b"Detached from Deskyard Terminal.")


def main():
    if not sys.stdin.isatty() or not sys.stdout.isatty():
        sys.exit("Interactive terminal required; use deskyard exec --tty.")
    original = termios.tcgetattr(0)
    child, master = pty.fork()
    if child == 0:
        os.execvp("zellij", ["zellij", *sys.argv[1:]])
    output = AttachOutput()

    def resize(*_):
        try:
            size = fcntl.ioctl(0, termios.TIOCGWINSZ, b"\0" * 8)
            fcntl.ioctl(master, termios.TIOCSWINSZ, size)
        except OSError:
            pass

    def forward(sig, _frame):
        try:
            os.killpg(child, sig)
        except ProcessLookupError:
            pass

    def write(data):
        sys.stdout.buffer.write(data)
        sys.stdout.buffer.flush()

    signal.signal(signal.SIGWINCH, resize)
    for sig in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT):
        signal.signal(sig, forward)
    try:
        resize()
        tty.setraw(0)
        while True:
            readable, _, _ = select.select([0, master], [], [])
            if master in readable:
                try:
                    data = os.read(master, 65536)
                except OSError as error:
                    if error.errno == errno.EIO:
                        break
                    raise
                if not data:
                    break
                write(output.feed(data))
            if 0 in readable:
                data = os.read(0, 65536)
                if not data:
                    forward(signal.SIGHUP, None)
                    break
                while data:
                    count = os.write(master, data)
                    data = data[count:]
        write(output.finish())
    finally:
        termios.tcsetattr(0, termios.TCSADRAIN, original)
        os.close(master)
        _, status = os.waitpid(child, 0)
    code = os.waitstatus_to_exitcode(status)
    sys.exit(code if code >= 0 else 128 - code)


if __name__ == "__main__":
    main()
