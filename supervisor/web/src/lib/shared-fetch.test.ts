import assert from "node:assert/strict";
import { test } from "node:test";
import { SharedFetches } from "./shared-fetch.ts";

const tick = () => new Promise((r) => setTimeout(r, 0));

test("views of a key share one load and see its result", async () => {
  const s = new SharedFetches();
  let calls = 0;
  const fn = async () => ++calls;
  let seen = 0;
  s.subscribe("k", () => seen++);
  s.subscribe("k", () => seen++);
  s.load("k", fn);
  s.load("k", fn);
  await tick();
  assert.equal(calls, 1);
  assert.equal(seen, 2);
  assert.deepEqual(s.get("k"), { data: 1, error: null });
});

test("a forced load supersedes one under way, and every view sees it", async () => {
  const s = new SharedFetches();
  const resolvers: ((v: string) => void)[] = [];
  const fn = () => new Promise<string>((r) => resolvers.push(r));
  let seen = 0;
  s.subscribe("k", () => seen++);
  s.load("k", fn);
  s.load("k", fn, true);
  resolvers[1]!("new");
  resolvers[0]!("old");
  await tick();
  assert.equal(s.get("k").data, "new");
  assert.equal(seen, 1);
});

test("a failed load keeps the data it had and reports the error", async () => {
  const s = new SharedFetches();
  s.load("k", async () => "first");
  await tick();
  s.load("k", async () => {
    throw new Error("down");
  });
  await tick();
  assert.deepEqual(s.get("k"), { data: "first", error: "down" });
});

test("an unsubscribed view hears nothing more", async () => {
  const s = new SharedFetches();
  let seen = 0;
  const off = s.subscribe("k", () => seen++);
  off();
  s.load("k", async () => 1);
  await tick();
  assert.equal(seen, 0);
});
