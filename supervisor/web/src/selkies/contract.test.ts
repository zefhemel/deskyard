import { test } from "node:test";
import assert from "node:assert/strict";
import { captureGate, shouldSend, displayMessage } from "./contract.ts";

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


test("display policy wins over delayed automatic settings and resize requests", () => {
  const desired = { width: 2560, height: 1600, dpi: 192 };
  assert.equal(displayMessage("r,1280x800", true, desired), "r,2560x1600");
  assert.equal(displayMessage("s,96", true, desired), "s,192");
  assert.equal(displayMessage("r,1280x800,primary", true, desired), "r,2560x1600,primary");
  assert.equal(displayMessage("s,96,primary", true, desired), "s,192,primary");
  const data = displayMessage('SETTINGS,{"scaling_dpi":96,"manual_resolution":false,"encoder":"jpeg"}', true, desired) as string;
  assert.deepEqual(JSON.parse(data.slice(9)), { scaling_dpi: 192, manual_resolution: true, manual_width: 2560, manual_height: 1600, encoder: "jpeg" });
});

test("secondary viewers and initial handshakes cannot apply a competing DPI", () => {
  assert.equal(displayMessage("s,96", false, null), null);
  assert.equal(displayMessage("s,192", null, null), null);
  assert.equal(displayMessage("r,1280x800", false, null), null);
  assert.equal(displayMessage('SETTINGS,{"scaling_dpi":96,"manual_width":1280,"manual_height":800,"manual_resolution":true,"encoder":"jpeg"}', false, null), 'SETTINGS,{"encoder":"jpeg"}');
  assert.equal(displayMessage('SETTINGS,{"scaling_dpi":96,"displayId":"primary"}', null, null), 'SETTINGS,{"displayId":"primary"}');
});
