import { test } from "node:test";
import assert from "node:assert/strict";
import { WheelAccumulator } from "./wheel.ts";

test("fractional trackpad events accumulate without rounding each event away", () => {
  const wheel = new WheelAccumulator();
  let vertical = 0;
  for (let n = 0; n < 320; n++) vertical += wheel.take(0, 0.25, n)[1];
  assert.equal(vertical, 2);
});

test("axes accumulate independently and reversal discards the previous direction's remainder", () => {
  const wheel = new WheelAccumulator();
  assert.deepEqual(wheel.take(20, -30, 0), [0, 0]);
  assert.deepEqual(wheel.take(20, -10, 1), [1, -1]);
  assert.deepEqual(wheel.take(0, 30, 2), [0, 0]);
  assert.deepEqual(wheel.take(0, -40, 3), [0, -1]);
});

test("separate gestures do not inherit old motion, and huge input is bounded", () => {
  const wheel = new WheelAccumulator();
  wheel.take(0, 30, 0);
  assert.deepEqual(wheel.take(0, 10, 300), [0, 0]);
  assert.deepEqual(wheel.take(Infinity, -20000, 301), [0, -50]);
  wheel.reset();
  assert.deepEqual(wheel.take(0, 30, 302), [0, 0]);
});

test("the installed handler preserves remainders while replaying and releases its listener", async () => {
  const { installWheelScrolling } = await import("./wheel.ts");
  const previous = globalThis.WheelEvent;
  class ProbeWheel extends Event {
    deltaMode = 0; deltaX = 0; deltaY = 0; ctrlKey = false; metaKey = false;
    constructor(type: string, init: WheelEventInit) {
      super(type, init);
      Object.assign(this, Object.fromEntries(Object.entries(init).filter(([key]) => !["bubbles", "cancelable", "composed"].includes(key))));
    }
  }
  globalThis.WheelEvent = ProbeWheel as unknown as typeof WheelEvent;
  try {
    const element = new EventTarget() as unknown as HTMLElement;
    const stop = installWheelScrolling(element);
    const seen: [number, number, number][] = [];
    element.addEventListener("wheel", (e) => seen.push([e.deltaMode, e.deltaX, e.deltaY]));
    const send = (deltaX: number, deltaY: number, ctrlKey = false) => element.dispatchEvent(
      new WheelEvent("wheel", { deltaX, deltaY, ctrlKey, cancelable: true }),
    );
    send(20, 55);
    send(20, 25);
    assert.deepEqual(seen, [[1, 0, 1], [1, 0, 1], [1, 1, 0]]);
    send(0, -5, true);
    assert.deepEqual(seen.at(-1), [0, 0, -5]);
    stop();
    send(0, 0.25);
    assert.deepEqual(seen.at(-1), [0, 0, 0.25]);
  } finally { globalThis.WheelEvent = previous; }
});
