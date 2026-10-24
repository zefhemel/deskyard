import assert from "node:assert/strict";
import { test } from "node:test";
import type { Job } from "../api.ts";
import { JobTracker } from "./job-tracker.ts";

function setup() {
  const updates = new Map<string, (job: Job) => void>();
  const opened: string[] = [], closed: string[] = [];
  const tracker = new JobTracker((id, update) => {
    opened.push(id);
    updates.set(id, update);
    return () => { closed.push(id); };
  });
  return { tracker, opened, closed, send: (id: string, job: Job) => updates.get(id)!(job) };
}
const running: Job = { status: "running", message: "Downloading", progress: 0.2, desktop: "alice" };
const failed: Job = { status: "failed", message: "Failed", error: "Download interrupted", progress: null, desktop: "alice" };

test("local and discovered followers share one transport and terminal state", () => {
  const { tracker, opened, send } = setup();
  const local: Job[] = [], discovered: Job[] = [];
  tracker.track("one", job => local.push(job));
  tracker.track("one", job => discovered.push(job), "alice");
  send("one", running);
  send("one", failed);
  assert.deepEqual(opened, ["one"]);
  assert.equal(tracker.get("one"), failed);
  assert.equal(tracker.forDesktop("alice"), failed);
  assert.deepEqual(tracker.runningByDesktop(), {});
  assert.equal(local.at(-1), failed);
  assert.equal(discovered.at(-1), failed);
  const remounted: Job[] = [];
  tracker.track("one", job => remounted.push(job));
  assert.deepEqual(remounted, [failed]);
  assert.deepEqual(opened, ["one"]);
});

test("old completion cannot replace a newer desktop operation", () => {
  const { tracker, send } = setup();
  tracker.track("old", undefined, "alice");
  tracker.track("new", undefined, "alice");
  send("new", running);
  send("old", failed);
  assert.equal(tracker.forDesktop("alice"), running);
});

test("removing a callback keeps shared progress; disposing closes streams and ignores late output", () => {
  const { tracker, closed, send } = setup();
  const seen: Job[] = [];
  const unsubscribe = tracker.track("one", job => seen.push(job), "alice");
  unsubscribe();
  send("one", running);
  assert.equal(seen.length, 1);
  assert.equal(tracker.forDesktop("alice"), running);
  tracker.dispose();
  assert.deepEqual(closed, ["one"]);
  send("one", failed);
  assert.equal(tracker.forDesktop("alice"), null);
});

test("discovered jobs publish Starting before the transport sends any payload", () => {
  const { tracker } = setup();
  const seen: string[] = [];
  tracker.subscribe(() => seen.push(tracker.forDesktop("alice")?.message ?? "missing"));
  tracker.track("one", undefined, "alice");
  assert.deepEqual(seen, ["Starting"]);
  assert.equal(tracker.runningByDesktop().alice?.status, "running");
  tracker.track("one", undefined, "alice");
  assert.deepEqual(seen, ["Starting"]);
});
