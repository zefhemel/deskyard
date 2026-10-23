import assert from "node:assert/strict";
import { test } from "node:test";
import { Badges, type Badge } from "./badges.ts";

function recorder() {
  const sent: [Badge, boolean, number][] = [];
  let release: (() => void)[] = [];
  const send = (kind: Badge, on: boolean, seq: number) =>
    new Promise<void>((resolve) => {
      sent.push([kind, on, seq]);
      release.push(resolve);
    });
  const flush = async () => {
    while (release.length) {
      const r = release;
      release = [];
      for (const f of r) f();
      await new Promise((r) => setTimeout(r, 0));
    }
  };
  const flushOne = () => release.shift()?.();
  return { sent, send, flush, flushOne };
}

test("reports go one at a time, numbered in order", async () => {
  const r = recorder();
  const b = new Badges(r.send);
  void b.set("mic", true);
  void b.set("cam", true);
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(r.sent.length, 1); // the second waits for the first
  await r.flush();
  assert.deepEqual(r.sent, [
    ["mic", true, 1],
    ["cam", true, 2],
  ]);
});

test("off then on quickly: the latest intent wins", async () => {
  const r = recorder();
  const b = new Badges(r.send);
  const first = b.set("mic", true);
  await new Promise((r) => setTimeout(r, 0));
  void b.set("mic", false);
  const last = b.set("mic", true);
  await r.flush();
  await first;
  await last;
  // The off never went: the on made after it replaced it.
  assert.deepEqual(r.sent, [
    ["mic", true, 1],
    ["mic", true, 3],
  ]);
  assert.equal(b.wants("mic"), true);
});

test("a refused report rejects, and later ones still go", async () => {
  const sent: number[] = [];
  const b = new Badges(async (_k, _on, seq) => {
    sent.push(seq);
    if (seq === 1) throw new Error("no stream from that tab");
  });
  await assert.rejects(b.set("mic", true), /no stream/);
  await b.set("mic", false);
  assert.deepEqual(sent, [1, 2]);
});

test("a replaced report settles only once its replacement has gone", async () => {
  const r = recorder();
  const b = new Badges(r.send);
  void b.set("mic", false); // in flight
  await new Promise((r) => setTimeout(r, 0));
  let settled = false;
  const replacedOn = b.set("mic", true).then(() => (settled = true));
  void b.set("mic", false);
  const lastOn = b.set("mic", true);
  await new Promise((r) => setTimeout(r, 0));
  r.flushOne(); // the first "off" lands; the two dropped reports never go
  await new Promise((r) => setTimeout(r, 0));
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(settled, false); // the last "on" hasn't been confirmed yet
  assert.deepEqual(r.sent.map(([, on, seq]) => [on, seq]), [
    [false, 1],
    [true, 4],
  ]);
  r.flushOne();
  await lastOn;
  await replacedOn;
  assert.equal(settled, true);
});

test("a replaced report rejects when its replacement is refused", async () => {
  let n = 0;
  let release: () => void = () => {};
  const b = new Badges((_k, _on) => {
    n++;
    if (n === 1) return new Promise<void>((r) => (release = r));
    return Promise.reject(new Error("no stream from that tab"));
  });
  void b.set("cam", false); // in flight
  await new Promise((r) => setTimeout(r, 0));
  const replaced = b.set("cam", true);
  b.set("cam", false).catch(() => {}); // replaced too, and settles as the last
  const last = b.set("cam", true);
  await new Promise((r) => setTimeout(r, 0));
  release();
  await assert.rejects(last, /no stream/);
  await assert.rejects(replaced, /no stream/);
});

test("settled waits for the newest report, however many came", async () => {
  const r = recorder();
  const b = new Badges(r.send);
  void b.set("mic", true);
  await new Promise((r) => setTimeout(r, 0));
  let done = false;
  const s = b.settled("mic").then(() => (done = true));
  void b.set("mic", false);
  r.flushOne(); // the first "on" lands
  await new Promise((r) => setTimeout(r, 0));
  void b.set("mic", true); // made while the "off" is in flight
  await new Promise((r) => setTimeout(r, 0));
  r.flushOne(); // the "off" lands
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(done, false);
  r.flushOne(); // the last "on" lands
  await s;
  assert.equal(b.wants("mic"), true);
});
