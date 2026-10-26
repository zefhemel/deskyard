import { test } from "node:test";
import assert from "node:assert/strict";
import { captureGate, shouldSend } from "./contract.ts";

test("only the adapter controls shared video and per-viewer audio subscriptions", () => {
  assert.equal(shouldSend("STOP_VIDEO"), false);
  assert.equal(shouldSend("START_AUDIO"), false);
  assert.equal(shouldSend("STOP_AUDIO"), false);
  assert.equal(shouldSend("REQUEST_KEYFRAME"), true);
  assert.equal(shouldSend(new ArrayBuffer(2)), true);
});

test("capture consent belongs to the current connection, including pending permission requests", async () => {
  const gate = captureGate();
  gate.allow("microphone", true);
  let stopped = false;
  let release!: (stream: MediaStream) => void;
  const stream = {
    getTracks: () => [
      {
        stop() {
          stopped = true;
        },
      },
    ],
  } as unknown as MediaStream;
  const pending = gate.capture(
    { audio: true },
    () =>
      new Promise((resolve) => {
        release = resolve;
      }),
  );
  gate.reset();
  assert.equal(gate.allowed("microphone"), false);
  release(stream);
  await assert.rejects(pending, { name: "NotAllowedError" });
  assert.equal(stopped, true);
  await assert.rejects(
    gate.capture({ audio: true }, async () => stream),
    { name: "NotAllowedError" },
  );
});

test("video is never captured, even with the mic allowed", async () => {
  const gate = captureGate();
  gate.allow("microphone", true);
  const stream = { getTracks: () => [] } as unknown as MediaStream;
  await assert.rejects(gate.capture({ video: true }, async () => stream), { name: "NotAllowedError" });
  await assert.rejects(gate.capture({ audio: true, video: true }, async () => stream), { name: "NotAllowedError" });
  assert.equal(await gate.capture({ audio: true }, async () => stream), stream);
});
