import { test } from "node:test";
import assert from "node:assert/strict";
import { AutoFramerate, startAutoQuality, type StatsWindow } from "./auto-quality.ts";
import type { DeskyardScreen } from "../selkies/screen.ts";
import { autoPreset, defaultQuality, isQualityId, qualities, qualityOf, qualitySettings } from "./selkies.ts";

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

test("monitor stops adapting while hidden, disconnected or secondary, and disposes stats/timer", (context) => {
  let clock = 1000;
  context.mock.method(Date, "now", () => clock);
  let tick = () => {}, cancelled = false;
  const messages: object[] = [];
  let owner = true, state = "connected";
  const target: StatsWindow = { document: { hidden: false }, stream_stats: { latest: null } };
  const screen = { display: () => ({ owner, width: 1280, height: 720, settings: { encoder: "jpeg" } }), state: () => state, post: (m: object) => messages.push(m) } as unknown as DeskyardScreen;
  const stop = startAutoQuality(screen, target, ((fn: () => void) => { tick = fn; return 1; }) as unknown as typeof setInterval, (() => { cancelled = true; }) as typeof clearInterval);
  let lost = 0;
  const sample = () => { target.stream_stats!.latest = { t: clock += 1000, fps: 15, frames_not_shown: lost }; lost += 10; tick(); };
  for (const blocked of ["hidden", "owner", "state"]) {
    target.document.hidden = blocked === "hidden"; owner = blocked !== "owner"; state = blocked === "state" ? "disconnected" : "connected";
    for (let i = 0; i < 5; i++) sample();
  }
  assert.deepEqual(messages, [{ type: "statsOpen", open: true }]);
  target.document.hidden = false; owner = true; state = "connected";
  for (let i = 0; i < 4; i++) sample();
  assert.deepEqual(messages.at(-1), { type: "settings", settings: { framerate: 14 } });
  stop(); assert.ok(cancelled);
  assert.deepEqual(messages.at(-1), { type: "statsOpen", open: false });
});
