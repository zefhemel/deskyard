import assert from "node:assert/strict";
import test from "node:test";
import type { ComponentChildren } from "preact";
import type { MenuItem } from "../components/menu.tsx";
import { compactDisplayMenu, displayMenu, type DisplayMenuState } from "./display-menu.ts";

/** A label's text, as a screen reader would read it. */
function text(label: ComponentChildren): string {
  if (label === null || label === undefined || typeof label === "boolean") return "";
  if (typeof label === "string" || typeof label === "number") return String(label);
  if (Array.isArray(label)) return label.map(text).join("");
  return text((label as { props: { children?: ComponentChildren } }).props.children);
}
const labels = (items: MenuItem[]) => items.map((i) => text(i.label));
const checked = (items: MenuItem[]) => items.filter((i) => i.checked).map((i) => text(i.label));

function state(change: Partial<DisplayMenuState> = {}): DisplayMenuState & { calls: unknown[] } {
  const calls: unknown[] = [];
  return {
    owner: true,
    locked: null,
    scaling: "auto",
    ratio: 2,
    quality: "auto",
    live: "medium",
    onLock: () => calls.push("lock"),
    onResolution: (size) => calls.push(size),
    onScaling: (s) => calls.push(`scaling ${s}`),
    onQuality: (q) => calls.push(`quality ${q}`),
    calls,
    ...change,
  };
}

test("the owner's menu: Lock size, resolutions, scaling, then quality with hints", () => {
  const items = displayMenu(state({ locked: { width: 1280, height: 800 } }));
  const all = labels(items);
  assert.equal(all[0], "Lock size");
  assert.ok(all.includes("Resolution: 600 × 900 (portrait)"));
  assert.ok(all.includes("Scaling: Auto (2×)"));
  assert.ok(all.includes("Quality: Low, for slow connections"));
  assert.deepEqual(checked(items), ["Lock size", "Resolution: 1280 × 800", "Scaling: Auto (2×)", "Quality: Auto, adapts to this browser and connection"]);
  const quality = items.findIndex((i) => text(i.label).startsWith("Quality:"));
  assert.equal(items[quality]?.separated, true);
  assert.equal(items.filter((i) => i.separated).length, 1);
});

test("a chosen preset is checked by what the display streams with", () => {
  assert.deepEqual(checked(displayMenu(state({ quality: "high", live: "low" }))).filter((l) => l.startsWith("Quality")), ["Quality: Low, for slow connections"]);
  assert.deepEqual(checked(displayMenu(state({ quality: "high", live: null }))).filter((l) => l.startsWith("Quality")), []);
});

test("beside the owner, or before knowing, size and scaling are a disabled note", () => {
  for (const [owner, note] of [[false, "Resolution: controlled by another viewer"], [null, "Resolution: connecting…"]] as const) {
    for (const items of [displayMenu(state({ owner })), compactDisplayMenu(state({ owner }), [])]) {
      const sizes = items.filter((i) => !text(i.label).startsWith("Quality:"));
      assert.deepEqual(labels(sizes), [note]);
      assert.equal(sizes[0]?.disabled, true);
    }
  }
});

test("the compact menu has the same items, plain quality labels and the keys between, shown on narrow screens only", () => {
  const s = state();
  const keys: MenuItem[] = [{ label: "Send Esc", onSelect: () => s.calls.push("esc") }, { label: "Send F11", onSelect: () => {} }];
  const full = displayMenu(s);
  const compact = compactDisplayMenu(s, keys);
  const plain = (l: string) => l.replace(/, .*$/, "");
  assert.deepEqual(labels(compact).filter((l) => !l.startsWith("Send ")), labels(full).map((l) => (l.startsWith("Quality:") ? plain(l) : l)));
  assert.ok(labels(compact).includes("Quality: Lossless"));
  assert.ok(compact.every((i) => i.class === "sm:hidden"));
  // Three sections: size, keys, quality.
  assert.deepEqual(compact.filter((i) => i.separated).map((i) => text(i.label)), ["Lock size", "Send Esc", "Quality: Auto"]);
  for (const item of compact) item.onSelect();
  assert.deepEqual(s.calls.slice(0, 2), ["lock", null]);
  assert.ok(s.calls.includes("esc") && s.calls.includes("scaling 2") && s.calls.includes("quality lossless"));
});
