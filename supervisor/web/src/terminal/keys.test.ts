import assert from "node:assert/strict";
import { test } from "node:test";
import { encodeKey, OneShotModifiers, binaryBytes } from "./keys.ts";
test("terminal key bytes and modifier combinations", () => {
  assert.equal(encodeKey("c", { ctrl: true, alt: false }), "\x03");
  assert.equal(encodeKey("D", { ctrl: true, alt: false }), "\x04");
  assert.equal(encodeKey("Escape"), "\x1b");
  assert.equal(encodeKey("Tab"), "\t");
  assert.equal(encodeKey("ArrowUp"), "\x1b[A");
  assert.equal(encodeKey("ArrowLeft", { ctrl: true, alt: true }), "\x1b[1;7D");
  assert.equal(encodeKey("x", { ctrl: false, alt: true }), "\x1bx");
  assert.equal(encodeKey("PageUp"), "\x1b[5~");
});
test("one shot applies to native single key, leaves paste/IME intact and resets", () => {
  const m = new OneShotModifiers();
  m.toggle("ctrl");
  assert.equal(m.consume("c"), "\x03");
  assert.equal(m.consume("c"), "c");
  m.toggle("alt");
  assert.equal(m.consume("x"), "\x1bx");
  m.toggle("ctrl");
  assert.equal(m.consume("hello\n"), "hello\n");
  assert.deepEqual(m.value, { ctrl: false, alt: false });
  m.toggle("alt");
  m.reset();
  assert.equal(m.consume("a"), "a");
  assert.deepEqual([...binaryBytes("\x80\xff")], [128, 255]);
});
test("native arrows use one-shot modifiers while Unicode commits stay intact", () => {
  const m = new OneShotModifiers();
  m.toggle("ctrl");
  assert.equal(m.consume("\x1b[A"), "\x1b[1;5A");
  m.toggle("alt");
  assert.equal(m.consume("日"), "日");
  assert.deepEqual(m.value, { ctrl: false, alt: false });
  assert.equal(encodeKey("ArrowUp", undefined, true), "\x1bOA");
  assert.equal(encodeKey("Home", undefined, true), "\x1bOH");
  assert.equal(
    encodeKey("ArrowDown", { ctrl: true, alt: false }, true),
    "\x1b[1;5B",
  );
});
test("composition commits are text even when they contain a single ASCII character", () => {
  const m = new OneShotModifiers();
  m.toggle("alt");
  assert.equal(m.consume("a", true), "a");
  assert.deepEqual(m.value, { ctrl: false, alt: false });
});
test("owned tmux history uses its standard copy-mode key", () => {
  assert.equal(encodeKey("History"), "\x02[");
});
