import assert from "node:assert/strict";
import { test } from "node:test";
import type { Computer } from "../api.ts";
import { notice, selfFirst } from "./format.ts";

type Seen = Pick<Computer, "state" | "revision" | "appliedRevision" | "error" | "conflict">;
const applied: Seen = { state: "running", revision: 3, appliedRevision: 3, error: "", conflict: "" };

test("an applied computer has nothing to say", () => {
  assert.equal(notice(applied), null);
  // Stopped without a container: it's built on start, which isn't news.
  assert.equal(notice({ ...applied, state: "stopped", appliedRevision: 0 }), null);
});

test("a saved change not yet applied is pending until start", () => {
  assert.deepEqual(notice({ ...applied, state: "stopped", revision: 4 }), {
    kind: "pending",
    text: "Saved changes apply when it starts.",
  });
  assert.equal(notice({ ...applied, revision: 4 })?.kind, "pending");
  assert.match(notice({ ...applied, revision: 4 })?.text ?? "", /restart/i);
});

test("a failed apply shows the error and that the change is still ahead", () => {
  const n = notice({ ...applied, revision: 4, error: "mount source /srv/photos is missing" });
  assert.equal(n?.kind, "error");
  assert.match(n?.text ?? "", /^mount source \/srv\/photos is missing/);
  assert.match(n?.text ?? "", /aren't applied/);
  assert.match(n?.text ?? "", /restart/i);
  assert.match(notice({ ...applied, state: "stopped", revision: 4, error: "boom" })?.text ?? "", /boom\. .*Start it to try again/);
  assert.deepEqual(notice({ ...applied, error: "VPN connection v1 was deleted" }), { kind: "error", text: "VPN connection v1 was deleted" });
});

test("a conflict wins, and busy computers wait for their job", () => {
  assert.deepEqual(notice({ ...applied, error: "x", conflict: "several containers claim this computer" }), {
    kind: "error",
    text: "several containers claim this computer",
  });
  assert.equal(notice({ ...applied, state: "upgrading", revision: 4 }), null);
  assert.equal(notice({ ...applied, state: "creating", revision: 1, appliedRevision: 0, error: "x" }), null);
});

test("you come first among the viewers", () => {
  assert.deepEqual(selfFirst(["ann", "zef", "bo"], "zef"), ["zef", "ann", "bo"]);
  assert.deepEqual(selfFirst(["ann", "bo"], "zef"), ["ann", "bo"]);
});
