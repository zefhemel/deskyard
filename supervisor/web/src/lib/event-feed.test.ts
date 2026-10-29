import assert from "node:assert/strict";
import { test } from "node:test";
import { EventFeed, type EventSocket } from "./event-feed.ts";

class Wire implements EventSocket {
  onopen: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  onclose: ((e: CloseEvent) => void) | null = null;
  closed = false;
  close() { this.closed = true; }
  message(data: string) { this.onmessage?.(new MessageEvent("message", { data })); }
}

function setup() {
  const wires: Wire[] = [];
  const retries: (() => void)[] = [];
  const feed = new EventFeed("ws://example.test/api/events", {
    socket: () => { const w = new Wire(); wires.push(w); return w; },
    schedule: (f) => { retries.push(f); return () => { const i = retries.indexOf(f); if (i >= 0) retries.splice(i, 1); }; },
  });
  return { feed, wires, retries };
}

test("reconnect delivers new events and ignores the old socket", () => {
  const { feed, wires, retries } = setup();
  const seen: string[] = [];
  feed.addEventListener("computer", e => seen.push(e.data));
  wires[0]!.message('{"type":"computer","data":{"name":"alice"}}');
  wires[0]!.onclose?.({} as CloseEvent);
  retries.shift()!();
  wires[0]!.message('{"type":"computer","data":{"name":"stale"}}');
  wires[1]!.message('{"type":"computer","data":{"name":"bob"}}');
  assert.deepEqual(seen, ['{"name":"alice"}', '{"name":"bob"}']);
  feed.close();
});

test("closing cancels retries and prevents post-unmount delivery", () => {
  const { feed, wires, retries } = setup();
  const seen: string[] = [];
  feed.addEventListener("screen", e => seen.push(e.data));
  wires[0]!.onclose?.({} as CloseEvent);
  feed.close();
  assert.equal(retries.length, 0);
  wires[0]!.message('{"type":"screen","data":{}}');
  assert.deepEqual(seen, []);
});

test("malformed messages do not dispatch and an active socket closes on cleanup", () => {
  const { feed, wires } = setup();
  const seen: string[] = [];
  feed.addEventListener("screen", e => seen.push(e.data));
  for (const data of ['oops', 'null', '{"data":1}', '{"type":"screen"}']) wires[0]!.message(data);
  wires[0]!.message('{"type":"screen","data":{"action":"click"}}');
  assert.deepEqual(seen, ['{"action":"click"}']);
  feed.close();
  assert.equal(wires[0]!.closed, true);
});

test("failed connections back off to ten seconds and an open resets the delay", () => {
  const wires: Wire[] = [];
  const retries: (() => void)[] = [];
  const delays: number[] = [];
  const feed = new EventFeed("ws://example.test/api/events", {
    socket: () => { const wire = new Wire(); wires.push(wire); return wire; },
    schedule: (f, ms) => { retries.push(f); delays.push(ms); return () => {}; },
  });
  for (let i = 0; i < 8; i++) {
    wires.at(-1)!.onclose?.({} as CloseEvent);
    retries.shift()!();
  }
  assert.deepEqual(delays, [500, 1000, 2000, 4000, 8000, 10000, 10000, 10000]);
  wires.at(-1)!.onopen?.(new Event("open"));
  wires.at(-1)!.onclose?.({} as CloseEvent);
  assert.equal(delays.at(-1), 500);
  feed.close();
});
