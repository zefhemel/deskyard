import assert from "node:assert/strict";
import { test } from "node:test";
import { parseError } from "./api.ts";

test("an error answer carries its message, environments and known code", () => {
  const e = parseError(409, "Conflict", { error: "in use", environments: [{ id: "a", name: "a" }], code: "inUse" });
  assert.equal(e.status, 409);
  assert.equal(e.message, "in use");
  assert.deepEqual(e.environments, [{ id: "a", name: "a" }]);
  assert.equal(e.code, "inUse");
});

test("an unknown code or a body that isn't JSON falls back", () => {
  assert.equal(parseError(409, "Conflict", { error: "x", code: "other" }).code, "");
  const e = parseError(502, "Bad Gateway", undefined);
  assert.equal(e.message, "502 Bad Gateway");
  assert.equal(e.code, "");
  assert.deepEqual(e.environments, []);
});
