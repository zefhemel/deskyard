import assert from "node:assert/strict";
import { after, before, test } from "node:test";
import { installTouchScroll } from "./touch-scroll.ts";

// Node supplies EventTarget; only the browser's wheel-event data needs a shim.
class Wheel extends Event {
  deltaY: number;
  clientX: number;
  clientY: number;
  constructor(type: string, init: WheelEventInit) {
    super(type, init);
    this.deltaY = init.deltaY ?? 0;
    this.clientX = init.clientX ?? 0;
    this.clientY = init.clientY ?? 0;
  }
}
const originalWheel = globalThis.WheelEvent;
before(() => { globalThis.WheelEvent = Wheel as unknown as typeof WheelEvent; });
after(() => { globalThis.WheelEvent = originalWheel; });

function setup() {
  const element = new EventTarget() as HTMLElement;
  element.querySelector = () => null;
  const reports: Wheel[] = [];
  element.addEventListener("wheel", e => reports.push(e as Wheel));
  const dispose = installTouchScroll(element);
  const touch = (type: string, points: number[][]) => {
    const event = new Event(type, { cancelable: true });
    Object.assign(event, { touches: points.map(([x, y, id = 1]) => ({ clientX: x, clientY: y, identifier: id })) });
    element.dispatchEvent(event);
    return event;
  };
  return { element, reports, touch, dispose };
}

test("vertical swipes send incremental wheel pixels in natural direction", () => {
  const { reports, touch } = setup();
  assert.equal(touch("touchstart", [[100, 200]]).defaultPrevented, false);
  assert.equal(touch("touchmove", [[101, 204]]).defaultPrevented, false);
  assert.equal(reports.length, 0);
  assert.equal(touch("touchmove", [[102, 220]]).defaultPrevented, true);
  touch("touchmove", [[103, 236]]);
  touch("touchmove", [[103, 224]]);
  assert.deepEqual(reports.map(e => e.deltaY), [-20, -16, 12]);
  assert.equal(reports[0]?.clientX, 102);
  assert.equal(reports[0]?.clientY, 220);
});

test("taps, horizontal movement and multitouch never turn into terminal input", () => {
  const { reports, touch } = setup();
  touch("touchstart", [[100, 200]]);
  touch("touchend", []);
  touch("touchstart", [[100, 200]]);
  assert.equal(touch("touchmove", [[130, 202]]).defaultPrevented, false);
  touch("touchmove", [[135, 260]]);
  touch("touchend", []);
  touch("touchstart", [[100, 200]]);
  touch("touchstart", [[100, 200], [150, 200, 2]]);
  touch("touchmove", [[100, 250]]);
  assert.equal(reports.length, 0);
});

test("cancellation, changed fingers and disposal cannot leave a scrolling gesture", () => {
  const { reports, touch, dispose } = setup();
  touch("touchstart", [[100, 200]]);
  touch("touchcancel", []);
  touch("touchmove", [[100, 250]]);
  touch("touchstart", [[100, 200]]);
  touch("touchmove", [[100, 250, 2]]);
  touch("touchstart", [[100, 200]]);
  dispose();
  touch("touchmove", [[100, 250]]);
  assert.equal(reports.length, 0);
});

test("a new gesture starts from its own origin after the previous one ends", () => {
  const { reports, touch } = setup();
  touch("touchstart", [[100, 200]]);
  touch("touchmove", [[100, 224]]);
  touch("touchend", []);
  touch("touchmove", [[100, 280]]);
  touch("touchstart", [[100, 300]]);
  touch("touchmove", [[100, 280]]);
  assert.deepEqual(reports.map(e => e.deltaY), [-24, 20]);
});
