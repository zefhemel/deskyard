import assert from "node:assert/strict";
import { test } from "node:test";
import { RESOURCE_NAME, USERNAME } from "./names.ts";

test("resource names are 2–32 lowercase letters, digits and dashes", () => {
  for (const ok of ["ab", "alice", "a-1", "x".repeat(32)]) assert.ok(RESOURCE_NAME.test(ok), ok);
  for (const bad of ["", "a", "1a", "a-", "Alice", "a_b", "x".repeat(33)]) assert.ok(!RESOURCE_NAME.test(bad), bad);
});

test("usernames may have dots and underscores, and be one letter", () => {
  for (const ok of ["a", "sanne", "a.b_c-d", "x".repeat(32)]) assert.ok(USERNAME.test(ok), ok);
  for (const bad of ["", "1a", "Anna", "a b", "x".repeat(33)]) assert.ok(!USERNAME.test(bad), bad);
});
