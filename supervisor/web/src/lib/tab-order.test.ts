import assert from "node:assert/strict";
import { test } from "node:test";
import { leftNeighbour, loadOrder, moveTab, moveTabTo, orderTabs, saveOrder, vanished } from "./tab-order.ts";

test("stored order is kept, new sessions follow sorted by name, gone ones are dropped", () => {
  assert.deepEqual(orderTabs(["main", "terminal-1", "terminal-2"], ["terminal-2", "main"]), ["terminal-2", "main", "terminal-1"]);
  assert.deepEqual(orderTabs(["zeta", "alpha", "main"], ["main"]), ["main", "alpha", "zeta"]);
  assert.deepEqual(orderTabs(["main"], ["gone", "main", "also-gone"]), ["main"]);
  assert.deepEqual(orderTabs([], ["main"]), []);
  assert.deepEqual(orderTabs(["b", "a"], []), ["a", "b"]);
  // Duplicates in a corrupted store don't duplicate tabs.
  assert.deepEqual(orderTabs(["a", "b"], ["b", "b", "a"]), ["b", "a"]);
});

test("keyboard moves stay within the terminal tabs", () => {
  assert.deepEqual(moveTab(["a", "b", "c"], "b", -1), ["b", "a", "c"]);
  assert.deepEqual(moveTab(["a", "b", "c"], "b", 1), ["a", "c", "b"]);
  assert.deepEqual(moveTab(["a", "b", "c"], "a", -1), ["a", "b", "c"]);
  assert.deepEqual(moveTab(["a", "b", "c"], "c", 1), ["a", "b", "c"]);
  assert.deepEqual(moveTab(["a", "b"], "x", 1), ["a", "b"]);
});

test("a drop puts the tab before the target, or last", () => {
  assert.deepEqual(moveTabTo(["a", "b", "c"], "c", "a"), ["c", "a", "b"]);
  assert.deepEqual(moveTabTo(["a", "b", "c"], "a", "c"), ["b", "a", "c"]);
  assert.deepEqual(moveTabTo(["a", "b", "c"], "a", null), ["b", "c", "a"]);
  assert.deepEqual(moveTabTo(["a", "b", "c"], "b", "b"), ["a", "b", "c"]);
});

test("closing a tab goes to its left neighbour, Desktop (null) for the first", () => {
  assert.equal(leftNeighbour(["a", "b", "c"], "b"), "a");
  assert.equal(leftNeighbour(["a", "b", "c"], "a"), null);
  assert.equal(leftNeighbour(["a", "b"], "x"), null);
});

test("the order is kept per environment and survives a failing storage", () => {
  const data = new Map<string, string>();
  const fake = {
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
  };
  const g = globalThis as { localStorage?: unknown };
  const before = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  try {
    Object.defineProperty(globalThis, "localStorage", { value: fake, configurable: true });
    saveOrder("alice", ["b", "a"]);
    assert.deepEqual(loadOrder("alice"), ["b", "a"]);
    assert.deepEqual(loadOrder("research"), []);
    data.set("deskyard.tabs.research", "{not json");
    assert.deepEqual(loadOrder("research"), []);
    data.set("deskyard.tabs.research", JSON.stringify(["ok", 3]));
    assert.deepEqual(loadOrder("research"), ["ok"]);
    Object.defineProperty(globalThis, "localStorage", {
      get() {
        throw new Error("blocked");
      },
      configurable: true,
    });
    assert.deepEqual(loadOrder("alice"), []);
    saveOrder("alice", ["a"]); // doesn't throw
  } finally {
    if (before) Object.defineProperty(globalThis, "localStorage", before);
    else delete g.localStorage;
  }
});

test("sessions vanish from the list unless reopened here meanwhile", () => {
  assert.deepEqual(vanished(["main", "terminal-1"], ["main"], []), ["terminal-1"]);
  assert.deepEqual(vanished(["main"], ["main"], []), []);
  // terminal-2 ended, then + reused the name before the list caught up.
  assert.deepEqual(vanished(["main", "terminal-2"], ["main"], ["terminal-2"]), []);
  assert.deepEqual(vanished([], [], ["x"]), []);
});
