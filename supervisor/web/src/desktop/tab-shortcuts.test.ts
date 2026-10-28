import assert from "node:assert/strict";
import test from "node:test";
import { tabShortcutIndex, tabShortcutListener } from "./tab-shortcuts.ts";
const chord = { ctrlKey: true, shiftKey: true, altKey: false, metaKey: false, code: "Digit1", isComposing: false };
test("number-row shortcuts use physical digits across shifted layouts", () => {
  assert.equal(tabShortcutIndex(chord), 0);
  assert.equal(tabShortcutIndex({ ...chord, code: "Digit9" }), 8);
  for (const change of [{ ctrlKey: false }, { shiftKey: false }, { altKey: true }, { metaKey: true }, { isComposing: true }, { code: "Digit0" }, { code: "Numpad1" }, { code: "KeyA" }]) assert.equal(tabShortcutIndex({ ...chord, ...change }), null);
});
test("handled tabs stop downstream input; absent tabs pass through", () => {
  const target = new EventTarget();
  const selected: number[] = [];
  const listener = tabShortcutListener((index) => {
    if (index > 1) return false;
    selected.push(index);
    return true;
  }) as EventListener;
  target.addEventListener("keydown", listener, { capture: true });
  let forwarded = 0;
  target.addEventListener("keydown", () => forwarded++);
  const press = (code: string) => {
    const e = new Event("keydown", { cancelable: true });
    Object.assign(e, { ...chord, code });
    target.dispatchEvent(e);
    return e.defaultPrevented;
  };
  assert.equal(press("Digit2"), true);
  assert.deepEqual(selected, [1]);
  assert.equal(forwarded, 0);
  assert.equal(press("Digit9"), false);
  assert.equal(forwarded, 1);
});
