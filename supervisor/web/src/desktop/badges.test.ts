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
  return { sent, send, flush };
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
