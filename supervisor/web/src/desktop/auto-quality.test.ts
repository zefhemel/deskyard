import { test } from "node:test";
import assert from "node:assert/strict";
import { AutoFramerate, hasStats, startAutoQuality, type StatsScreen } from "./auto-quality.ts";
import type { DeskyardScreen, Display, StreamSample } from "../selkies/screen.ts";
import { autoPreset, defaultQuality, isQualityId, presetOf, qualities, qualityOf, qualitySettings } from "./selkies.ts";

function feed(policy: AutoFramerate) {
  let t = 1000, lost = 0;
  return (fps: number, dropped = 0, present_ms = 10) => {
    t += 1000; lost += dropped;
    return policy.sample({ t, fps, frames_not_shown: lost, present_ms });
  };
}

test("Auto is a choice, not a preset: it streams with Medium, recognised as Medium", () => {
  assert.equal(defaultQuality, "auto");
  assert.ok(isQualityId("auto"), "a stored auto still means Auto");
  assert.equal(autoPreset, "medium");
  assert.ok(qualities.every((q) => (q.id as string) !== "auto"));
  for (const jpeg of [true, false]) {
    assert.deepEqual(qualitySettings("auto", jpeg), qualitySettings("medium", jpeg));
    assert.equal(qualityOf(qualitySettings("auto", jpeg)), "medium");
  }
});

test("sustained presentation loss lowers the rate; a brief stall does not", () => {
  const p = new AutoFramerate(60), next = feed(p);
  next(30); assert.equal(next(30, 15), null);
  next(60); // breaks the overload run
  next(30, 15); next(30, 15);
  assert.equal(next(30, 15), 28);
  for (let i = 0; i < 100; i++) next(1, 10);
  assert.equal(p.rate, 10);
});

test("idle and low FPS alone never reduce quality or manufacture recovery headroom", () => {
  const p = new AutoFramerate(60), next = feed(p);
  for (let i = 0; i < 100; i++) next(i % 2 ? 0 : 5);
  assert.equal(p.rate, 60);
  next(20, 20); next(20, 20); assert.equal(next(20, 20), 19);
  for (let i = 0; i < 100; i++) next(0);
  assert.equal(p.rate, 19);
  for (let i = 0; i < 19; i++) assert.equal(next(19), null);
  assert.equal(next(19), 21);
  for (let i = 0; i < 100; i++) next(21, 0, 200);
  assert.equal(p.rate, 21);
});

test("duplicates, counter resets and suspended sampling cannot cause an adjustment", () => {
  const p = new AutoFramerate(30);
  const sample = { t: 1000, fps: 10, frames_not_shown: 20 };
  for (let i = 0; i < 100; i++) assert.equal(p.sample(sample), null);
  assert.equal(p.sample({ ...sample, t: 10000, frames_not_shown: 100 }), null);
  assert.equal(p.sample({ ...sample, t: 11000, frames_not_shown: 0 }), null);
  assert.equal(p.rate, 30);
});

function monitored(context: { mock: { method: typeof import("node:test").mock.method } }) {
  let clock = 1000;
  context.mock.method(Date, "now", () => clock);
  const state = { hidden: false, latest: null as StreamSample | null, tick: () => {}, cancelled: false, lost: 0 };
  const messages: object[] = [];
  const displays = new Set<(d: Display) => void>();
  let display: Display = { owner: true, width: 1280, height: 720, settings: { encoder: "jpeg" } };
  const screen = {
    display: () => display,
    on: (type: string, fn: (d: Display) => void) => {
      assert.equal(type, "deskyard.display");
      displays.add(fn);
      return () => displays.delete(fn);
    },
    post: (m: object) => messages.push(m),
    stats: () => state.latest,
    showStats: (open: boolean) => messages.push({ stats: open }),
  } as unknown as StatsScreen;
  const stop = startAutoQuality(screen, presetOf("auto"), () => state.hidden,
    ((fn: () => void) => { state.tick = fn; return 1; }) as unknown as typeof setInterval,
    (() => { state.cancelled = true; }) as typeof clearInterval);
  return {
    state, messages, stop, displays,
    /** A second of 15 fps with 10 frames lost. */
    overloaded() { state.latest = { t: clock += 1000, fps: 15, frames_not_shown: state.lost }; state.lost += 10; state.tick(); },
    advance(ms: number) { clock += ms; },
    setDisplay(next: Partial<Display>) { display = { ...display, ...next }; displays.forEach((fn) => fn(display)); },
  };
}

test("the monitor lowers the rate under load, pauses while hidden or stale, and disposes stats, timer and listener", (context) => {
  const m = monitored(context);
  assert.deepEqual(m.messages, [{ stats: true }]);
  m.state.hidden = true;
  for (let i = 0; i < 5; i++) m.overloaded();
  m.state.hidden = false;
  m.state.latest = { t: 0, fps: 15, frames_not_shown: 0 }; // stale
  m.state.tick();
  assert.deepEqual(m.messages, [{ stats: true }]);
  for (let i = 0; i < 4; i++) m.overloaded();
  // JPEG: Auto's preset tops out at 30 fps; 15 shown makes 14.
  assert.deepEqual(m.messages.at(-1), { type: "settings", settings: { framerate: 14 } });
  m.stop();
  assert.ok(m.state.cancelled);
  assert.equal(m.displays.size, 0);
  assert.deepEqual(m.messages.at(-1), { stats: false });
});

test("the monitor starts over at the display's rate when its encoder or size changes, capped by the preset", (context) => {
  const m = monitored(context);
  const healthy = (fps: number, n: number) => {
    for (let i = 0; i < n; i++) {
      m.advance(1000);
      m.state.latest = { t: Date.now(), fps, frames_not_shown: m.state.lost, present_ms: 10 };
      m.state.tick();
    }
  };
  m.setDisplay({ width: 1920, height: 1080, settings: { encoder: "jpeg", framerate: 28 } });
  healthy(28, 21);
  assert.deepEqual(m.messages.at(-1), { type: "settings", settings: { framerate: 30 } });
  m.messages.length = 0;
  healthy(30, 21);
  assert.deepEqual(m.messages, [], "JPEG stays at the preset's 30 fps");
  m.setDisplay({ settings: { encoder: "x264enc", framerate: 28 } });
  healthy(28, 21);
  assert.deepEqual(m.messages, [{ type: "settings", settings: { framerate: 31 } }], "H.264 goes on towards 60");
  m.stop();
});

test("only a screen with statistics can adapt", () => {
  const base = {} as DeskyardScreen;
  assert.equal(hasStats(base), false);
  assert.equal(hasStats({ ...base, stats: () => null, showStats: () => {} }), true);
});
