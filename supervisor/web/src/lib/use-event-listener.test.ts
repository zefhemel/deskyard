import assert from "node:assert/strict";
import test from "node:test";
import { listenFrameWindow } from "./use-event-listener.ts";
import { watchDevicePixelRatio } from "./use-device-pixel-ratio.ts";

const frameWindow = () => Object.assign(new EventTarget(), { document: {} });

test("a frame window listener follows each load of the frame and stops on cleanup", () => {
  const first = frameWindow();
  const frame = Object.assign(new EventTarget(), { contentWindow: first as EventTarget & { document?: unknown } });
  const heard: string[] = [];
  const dispose = listenFrameWindow(frame, "keydown", (e) => heard.push(e.currentTarget === first ? "first" : "second"), { capture: true });
  first.dispatchEvent(new Event("keydown"));
  // A load gives the frame a new window: the listener moves to it.
  const second = frameWindow();
  frame.contentWindow = second;
  frame.dispatchEvent(new Event("load"));
  first.dispatchEvent(new Event("keydown"));
  second.dispatchEvent(new Event("keydown"));
  assert.deepEqual(heard, ["first", "second"]);
  dispose();
  second.dispatchEvent(new Event("keydown"));
  frame.dispatchEvent(new Event("load"));
  second.dispatchEvent(new Event("keydown"));
  assert.deepEqual(heard, ["first", "second"]);
});

test("a cross-origin frame window is skipped, not thrown on", () => {
  const frame = Object.assign(new EventTarget(), {
    get contentWindow() {
      // Object.assign would read the getter; define it instead.
      return Object.defineProperty(new EventTarget(), "document", {
        get(): unknown {
          throw new DOMException("cross-origin", "SecurityError");
        },
      }) as EventTarget & { document?: unknown };
    },
  });
  const dispose = listenFrameWindow(frame, "paste", () => assert.fail("listened cross-origin"));
  frame.dispatchEvent(new Event("load"));
  dispose();
});

test("the pixel ratio watch hears its query and resizes, and cleans both up", () => {
  const queries: { query: string; target: EventTarget }[] = [];
  const w = Object.assign(new EventTarget(), {
    devicePixelRatio: 2,
    matchMedia(query: string) {
      const target = new EventTarget();
      queries.push({ query, target });
      return target;
    },
  });
  let changes = 0;
  const dispose = watchDevicePixelRatio(w, 2, () => changes++);
  assert.equal(queries[0]?.query, "(resolution: 2dppx)");
  queries[0]?.target.dispatchEvent(new Event("change"));
  w.dispatchEvent(new Event("resize"));
  assert.equal(changes, 2);
  dispose();
  queries[0]?.target.dispatchEvent(new Event("change"));
  w.dispatchEvent(new Event("resize"));
  assert.equal(changes, 2);
});
