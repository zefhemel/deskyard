import assert from "node:assert/strict";
import { test } from "node:test";
import { terminalLayout } from "./viewport.ts";

test("visible viewport budget keeps a readable row and 44px controls or exposes fallback", () => {
  assert.equal(terminalLayout(404), "full");
  assert.equal(terminalLayout(190), "compact");
  assert.equal(terminalLayout(100), "ultra");
  assert.equal(terminalLayout(68), "ultra");
  assert.equal(terminalLayout(67), "insufficient");
  assert.equal(terminalLayout(36), "insufficient");
});

// The threshold includes padded Reopen50 + header50 + host/padding28 +
// helper row44, top4 and the actual bottom inset (minimum4).
test("compact budget includes padded Reopen and measured safe areas", () => {
  assert.equal(terminalLayout(174), "ultra");
  assert.equal(terminalLayout(179), "ultra");
  assert.equal(terminalLayout(180), "compact");
  assert.equal(terminalLayout(195, 0, 20), "ultra");
  assert.equal(terminalLayout(196, 0, 20), "compact");
  assert.equal(terminalLayout(215, 20, 20), "ultra");
  assert.equal(terminalLayout(216, 20, 20), "compact");
  assert.equal(terminalLayout(68, 20, 20), "ultra");
  assert.equal(terminalLayout(36, 20, 20), "insufficient");
});
