"""A TCP proxy for tests that makes the network worse, between a browser and the supervisor.

Everything through it suffers alike (the page, its API calls and the stream WebSocket). The
impairments combine:
- delay_ms ± jitter_ms, per direction: each chunk is held for delay plus a uniform
  jitter, never overtaking the chunk before it on the same connection (byte order
  is kept);
- down_kbps / up_kbps: a bandwidth cap per direction, shared by all connections
  (down is supervisor → browser);
- stall_every_s / stall_ms: every N seconds all traffic is held for M ms (Wi-Fi
  hiccups).

    with DelayProxy("127.0.0.1", 7343, delay_ms=60, jitter_ms=20) as proxy:
        page.goto(f"http://127.0.0.1:{proxy.port}/")

It runs its own asyncio loop in a thread, so sync tests (Playwright's sync API) can use it.
"""

from __future__ import annotations

import asyncio
import random
import threading
import time

CHUNK = 16 * 1024


class _Link:
    """One direction's shared bandwidth: chunks queue for the wire, one at a time."""

    def __init__(self, kbps: float | None):
        self.kbps = kbps
        self.free_at = 0.0
        self.lock = asyncio.Lock()

    async def send(self, n: int):
        if not self.kbps:
            return
        async with self.lock:
            start = max(self.free_at, time.monotonic())
            self.free_at = start + n * 8 / (self.kbps * 1000)
            await asyncio.sleep(max(0.0, self.free_at - time.monotonic()))


class DelayProxy:
    def __init__(self, target_host: str, target_port: int, *, delay_ms: float = 0, jitter_ms: float = 0,
                 down_kbps: float | None = None, up_kbps: float | None = None,
                 stall_every_s: float | None = None, stall_ms: float = 0):
        self.target = (target_host, target_port)
        self.delay = delay_ms / 1000
        self.jitter = jitter_ms / 1000
        self.stall_every = stall_every_s
        self.stall = stall_ms / 1000
        self._kbps = {"down": down_kbps, "up": up_kbps}
        self.port = 0
        self._loop = asyncio.new_event_loop()
        self._thread = threading.Thread(target=self._loop.run_forever, daemon=True)
        self._server: asyncio.base_events.Server | None = None
        self._t0 = 0.0

    def start(self) -> "DelayProxy":
        self._thread.start()
        asyncio.run_coroutine_threadsafe(self._start(), self._loop).result(10)
        return self

    def stop(self):
        async def close():
            if self._server:
                self._server.close()
            tasks = [t for t in asyncio.all_tasks() if t is not asyncio.current_task()]
            for t in tasks:
                t.cancel()
            await asyncio.gather(*tasks, return_exceptions=True)
        asyncio.run_coroutine_threadsafe(close(), self._loop).result(10)
        self._loop.call_soon_threadsafe(self._loop.stop)
        self._thread.join(10)

    def __enter__(self):
        return self.start()

    def __exit__(self, *exc):
        self.stop()

    @property
    def base(self) -> str:
        return f"http://127.0.0.1:{self.port}"

    async def _start(self):
        self._links = {d: _Link(k) for d, k in self._kbps.items()}
        self._t0 = time.monotonic()
        self._server = await asyncio.start_server(self._client, "127.0.0.1", 0)
        self.port = self._server.sockets[0].getsockname()[1]

    def stall_starts(self) -> list[float]:
        """When each stall so far began, as time.time() values."""
        if not self.stall_every or not self.stall:
            return []
        now = time.monotonic()
        n = int((now - self._t0) / self.stall_every)
        return [time.time() - (now - (self._t0 + k * self.stall_every)) for k in range(1, n + 1)]

    def _stall_end(self, now: float) -> float:
        """Seconds until the current stall ends (0 outside a stall)."""
        if not self.stall_every or not self.stall:
            return 0.0
        k = int((now - self._t0) / self.stall_every)
        if k == 0:
            return 0.0  # the first stall comes after stall_every seconds
        start = self._t0 + k * self.stall_every
        return max(0.0, start + self.stall - now)

    async def _client(self, creader, cwriter):
        try:
            sreader, swriter = await asyncio.open_connection(*self.target)
        except OSError:
            cwriter.close()
            return
        pipes = [asyncio.ensure_future(self._pipe(creader, swriter, "up")),
                 asyncio.ensure_future(self._pipe(sreader, cwriter, "down"))]
        try:
            await asyncio.wait(pipes)
        finally:
            for t in pipes:
                t.cancel()
            await asyncio.gather(*pipes, return_exceptions=True)
            swriter.close()
            cwriter.close()

    async def _pipe(self, reader, writer, direction: str):
        queue: asyncio.Queue = asyncio.Queue()
        link = self._links[direction]

        async def deliver():
            while True:
                due, data = await queue.get()
                if data is None:
                    break
                await asyncio.sleep(max(0.0, due - time.monotonic()))
                while (wait := self._stall_end(time.monotonic())) > 0:
                    await asyncio.sleep(wait)
                await link.send(len(data))
                writer.write(data)
                await writer.drain()
            writer.close()

        task = asyncio.ensure_future(deliver())
        last = 0.0
        try:
            while data := await reader.read(CHUNK):
                due = time.monotonic() + self.delay + random.uniform(-self.jitter, self.jitter)
                last = max(due, last)  # never overtake the chunk before
                queue.put_nowait((last, data))
            queue.put_nowait((last, None))
            await task
        except (ConnectionError, OSError):
            pass
        finally:
            task.cancel()
            await asyncio.gather(task, return_exceptions=True)
