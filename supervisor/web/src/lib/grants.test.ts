import assert from "node:assert/strict";
import { test } from "node:test";
import { grantsLine, levelLabel } from "./grants.ts";

test("levels have labels", () => {
  assert.equal(levelLabel("full"), "Full access");
  assert.equal(levelLabel("observe"), "Observe only");
});

test("grants read as a line", () => {
  assert.equal(grantsLine([]), "");
  assert.equal(grantsLine([{ environment: "alice", level: "observe" }]), "observe alice");
  assert.equal(
    grantsLine([
      { environment: "alice", level: "full" },
      { environment: "notes", level: "observe" },
    ]),
    "use alice and observe notes",
  );
});
