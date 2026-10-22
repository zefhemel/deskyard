import assert from "node:assert/strict";
import { test } from "node:test";
import { keyCap } from "./keycap.ts";

test("key caps", () => {
  const cases: [string, string][] = [
    ["ctrl+l", "⌃L"],
    ["ctrl+shift+t", "⌃⇧T"],
    ["alt+Tab", "⌥⇥"],
    ["super", "❖"],
    ["Return", "↵"],
    ["Escape", "Esc"],
    ["BackSpace", "⌫"],
    ["Delete", "⌦"],
    ["Up", "↑"],
    ["F5", "F5"],
    ["a", "A"],
  ];
  for (const [combo, cap] of cases) assert.equal(keyCap(combo), cap, combo);
});
