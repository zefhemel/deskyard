import assert from "node:assert/strict";
import { test } from "node:test";
import { TerminalConnection, type Socket } from "./connection.ts";
class Wire implements Socket {
  readyState = 1;
  bufferedAmount = 0;
  binaryType = "";
  sent: (string | Uint8Array)[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: unknown }) => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  onerror: (() => void) | null = null;
  send(data: string | Uint8Array) {
    this.sent.push(data);
  }
  close() {
    this.readyState = 3;
  }
  message(data: unknown) {
    this.onmessage?.({ data });
  }
}
function setup() {
  const wires: Wire[] = [];
  const callbacks: (() => void)[] = [];
  const timers: (() => void)[] = [];
  const states: string[] = [];
  const warnings: string[] = [];
  let permitted = true;
  const c = new TerminalConnection({
    url: "ws://test",
    size: () => [80, 24],
    socket: () => {
      const w = new Wire();
      wires.push(w);
      return w;
    },
    preflight: async () => {
      if (!permitted) throw new Error("Signed out");
    },
    write: (_data, done) => callbacks.push(done),
    state: (s) => states.push(s),
    warning: (s) => warnings.push(s),
    schedule: (f, ms) => {
      if (ms !== 10000) timers.push(f);
      return () => {};
    },
  });
  return {
    c,
    wires,
    callbacks,
    timers,
    states,
    warnings,
    deny: () => {
      permitted = false;
    },
  };
}
test("disconnected input is discarded and output ACK waits for render", async () => {
  const t = setup();
  assert.equal(t.c.input(new Uint8Array([3])), false);
  await t.c.start();
  const w = t.wires[0]!;
  w.onopen?.();
  assert.equal(t.c.input(new Uint8Array([3])), false);
  w.message(JSON.stringify({ ready: true, generation: "one" }));
  assert.equal(t.c.input(new Uint8Array([3])), true);
  w.message(new Uint8Array([65, 66]).buffer);
  assert.equal(
    w.sent.some((x) => x === '{"ack":2}'),
    false,
  );
  t.callbacks[0]!();
  assert.equal(w.sent.at(-1), '{"ack":2}');
  w.onclose?.({ code: 1006 });
  assert.equal(t.c.input(new Uint8Array([4])), false);
  t.c.stop();
});
test("stale renderer callback cannot ACK new socket; generation warns", async () => {
  const t = setup();
  await t.c.start();
  const w = t.wires[0]!;
  w.onopen?.();
  w.message('{"ready":true,"generation":"one"}');
  w.message(new Uint8Array([1]).buffer);
  w.onclose?.({ code: 1006 });
  t.timers[0]!();
  await new Promise((r) => setImmediate(r));
  const next = t.wires[1]!;
  next.onopen?.();
  next.message('{"ready":true,"generation":"two"}');
  t.callbacks[0]!();
  assert.deepEqual(next.sent, ['{"cols":80,"rows":24}', '{"resize":[80,24]}']);
  assert.deepEqual(w.sent, ['{"cols":80,"rows":24}', '{"resize":[80,24]}']);
  assert.equal(t.warnings.length, 1);
  t.c.stop();
});
test("shell exit, protocol/auth close and rejected preflight stop retries", async () => {
  for (const control of ['{"exit":0}', '{"error":"Denied"}']) {
    const t = setup();
    await t.c.start();
    const w = t.wires[0]!;
    w.message(control);
    w.onclose?.({ code: 1006 });
    assert.equal(t.timers.length, 0);
    t.c.stop();
  }
  for (const code of [1000, 1002, 1008]) {
    const t = setup();
    await t.c.start();
    t.wires[0]!.onclose?.({ code });
    assert.equal(t.timers.length, 0);
    t.c.stop();
  }
  const t = setup();
  t.deny();
  await t.c.start();
  assert.equal(t.wires.length, 0);
  assert.equal(t.timers.length, 0);
});
test("outgoing paste is bounded without queueing", async () => {
  const t = setup();
  await t.c.start();
  const w = t.wires[0]!;
  w.onopen?.();
  w.message('{"ready":true,"generation":"one"}');
  assert.equal(t.c.input(new Uint8Array(65537)), false);
  w.bufferedAmount = 262144;
  assert.equal(t.c.input(new Uint8Array([1])), false);
  t.c.stop();
});
test("every attachment resets renderer before output and retries are bounded", async () => {
  const events: string[] = [];
  const t = setup();
  // Public reset hook is part of attachment, and precedes any fresh output.
  const w = new Wire();
  const c = new TerminalConnection({
    url: "ws://test",
    size: () => [80, 24],
    socket: () => w,
    preflight: async () => {},
    write: () => events.push("write"),
    reset: () => events.push("reset"),
    state: () => {},
    warning: () => {},
  });
  await c.start();
  w.message('{"ready":true,"generation":"one"}');
  w.message(new Uint8Array([65]).buffer);
  assert.deepEqual(events, ["reset", "write"]);
  c.stop();
  await t.c.start();
  for (let i = 0; i < 6; i++) {
    const wire = t.wires[i]!;
    assert.ok(wire);
    wire.onclose?.({ code: 1006 });
    const timer = t.timers[i];
    if (timer) {
      timer();
      await new Promise((r) => setImmediate(r));
    }
  }
  assert.equal(t.wires.length, 6);
  assert.equal(t.timers.length, 5);
  assert.equal(t.states.at(-1), "error");
  t.c.stop();
});
test("transient preflight recovers and successful episodes reset the failure budget", async () => {
  let failures = 1;
  const timers: (() => void)[] = [];
  const wires: Wire[] = [];
  const states: string[] = [];
  const c = new TerminalConnection({
    url: "ws://test",
    size: () => [80, 24],
    socket: () => {
      const w = new Wire();
      wires.push(w);
      return w;
    },
    preflight: async () => {
      if (failures-- > 0) throw new TypeError("Failed to fetch");
    },
    write: () => {},
    state: (s) => states.push(s),
    warning: () => {},
    schedule: (f, ms) => {
      if (ms < 10000) timers.push(f);
      return () => {};
    },
  });
  await c.start();
  assert.equal(wires.length, 0);
  assert.equal(states.at(-1), "retrying");
  timers.shift()!();
  await new Promise((r) => setImmediate(r));
  assert.equal(wires.length, 1);
  for (let episode = 0; episode < 8; episode++) {
    const w = wires.at(-1)!;
    w.message('{"ready":true,"generation":"one"}');
    w.onclose?.({ code: 1006 });
    assert.equal(states.at(-1), "retrying");
    timers.shift()!();
    await new Promise((r) => setImmediate(r));
  }
  assert.equal(wires.length, 9);
  c.stop();
});
test("runtime generation survives page attachment replacement", async () => {
  const w = new Wire();
  const warnings: string[] = [];
  let generation = "old";
  const c = new TerminalConnection({
    url: "ws://test",
    size: () => [80, 24],
    socket: () => w,
    preflight: async () => {},
    write: () => {},
    initialGeneration: generation,
    generation: (value) => (generation = value),
    state: () => {},
    warning: (s) => warnings.push(s),
  });
  await c.start();
  w.message('{"ready":true,"generation":"new"}');
  assert.equal(warnings.length, 1);
  assert.equal(generation, "new");
  c.stop();
});
test("ready resends current viewport size after startup and reconnect resize", async () => {
  let size: [number, number] = [80, 24];
  const wires: Wire[] = [];
  const retries: (() => void)[] = [];
  const c = new TerminalConnection({
    url: "ws://test",
    size: () => size,
    socket: () => {
      const w = new Wire();
      wires.push(w);
      return w;
    },
    preflight: async () => {},
    write: () => {},
    state: () => {},
    warning: () => {},
    schedule: (f, ms) => {
      if (ms !== 10000) retries.push(f);
      return () => {};
    },
  });
  await c.start();
  const first = wires[0]!;
  first.onopen?.();
  assert.equal(first.sent.at(-1), '{"cols":80,"rows":24}');
  size = [40, 12];
  c.resize();
  assert.equal(first.sent.length, 1);
  assert.equal(c.input(new Uint8Array([3])), false);
  first.message('{"ready":true,"generation":"one"}');
  assert.equal(first.sent.at(-1), '{"resize":[40,12]}');
  first.onclose?.({ code: 1006 });
  retries[0]!();
  await new Promise((resolve) => setImmediate(resolve));
  const second = wires[1]!;
  second.onopen?.();
  size = [90, 20];
  c.resize();
  assert.equal(c.input(new Uint8Array([4])), false);
  second.message('{"ready":true,"generation":"one"}');
  assert.equal(second.sent.at(-1), '{"resize":[90,20]}');
  const firstCount = first.sent.length;
  first.message('{"ready":true,"generation":"stale"}');
  assert.equal(first.sent.length, firstCount);
  assert.equal(
    second.sent.some((frame) => frame instanceof Uint8Array),
    false,
  );
  c.stop();
});
function joining(options: { create?: boolean; alive?: () => boolean } = {}) {
  const wires: (Wire & { url: string })[] = [];
  const timers: (() => void)[] = [];
  const states: [string, string | undefined][] = [];
  const c = new TerminalConnection({
    url: "ws://test/api/computers/a/terminal?session=main",
    create: options.create,
    alive: options.alive,
    size: () => [80, 24],
    socket: (url) => {
      const w = Object.assign(new Wire(), { url });
      wires.push(w);
      return w;
    },
    preflight: async () => {},
    write: (_data, done) => done(),
    state: (s, m) => states.push([s, m]),
    warning: () => {},
    schedule: (f, ms) => {
      if (ms !== 10000) timers.push(f);
      return () => {};
    },
  });
  return { c, wires, timers, states };
}
const flush = () => new Promise<void>((resolve) => setImmediate(resolve));

test("an ended session stops reconnection", async () => {
  const t = joining();
  await t.c.start();
  const w = t.wires[0]!;
  w.onopen?.();
  w.message(JSON.stringify({ ready: true, generation: "g" }));
  w.message(JSON.stringify({ ended: true }));
  w.onclose?.({ code: 1000 });
  assert.equal(t.states.at(-1)?.[0], "ended");
  assert.equal(t.timers.length, 0);
  t.c.resume();
  await flush();
  assert.equal(t.wires.length, 1);
});

test("a join to a missing session ends without retrying", async () => {
  const t = joining();
  await t.c.start();
  t.wires[0]!.onopen?.();
  t.wires[0]!.message(JSON.stringify({ ended: true }));
  t.wires[0]!.onclose?.({ code: 1000 });
  assert.deepEqual(t.states.map((s) => s[0]), ["connecting", "ended"]);
  assert.equal(t.timers.length, 0);
});

test("only the first attach asks to create; reconnects only join", async () => {
  const t = joining({ create: true });
  await t.c.start();
  assert.match(t.wires[0]!.url, /[?&]create=1/);
  assert.match(t.wires[0]!.url, /session=main/);
  t.wires[0]!.onopen?.();
  t.wires[0]!.message(JSON.stringify({ ready: true, generation: "g" }));
  t.wires[0]!.onclose?.({ code: 1006 });
  t.timers.shift()!();
  await flush();
  assert.equal(t.wires.length, 2);
  assert.doesNotMatch(t.wires[1]!.url, /create/);
  const j = joining();
  await j.c.start();
  assert.doesNotMatch(j.wires[0]!.url, /create/);
});

test("a reconnect checks the latest session list first", async () => {
  let alive = true;
  const t = joining({ alive: () => alive });
  await t.c.start();
  t.wires[0]!.onopen?.();
  t.wires[0]!.message(JSON.stringify({ ready: true, generation: "g" }));
  t.wires[0]!.onclose?.({ code: 1006 });
  assert.equal(t.states.at(-1)?.[0], "retrying");
  alive = false;
  t.timers.shift()!();
  await flush();
  assert.equal(t.wires.length, 1, "no new socket for a session that is gone");
  assert.equal(t.states.at(-1)?.[0], "ended");
  // Gone already when the connection drops: no retry at all.
  const u = joining({ alive: () => false });
  await u.c.start();
  u.wires[0]!.onopen?.();
  u.wires[0]!.message(JSON.stringify({ ready: true, generation: "g" }));
  u.wires[0]!.onclose?.({ code: 1006 });
  assert.equal(u.states.at(-1)?.[0], "ended");
  assert.equal(u.timers.length, 0);
});
