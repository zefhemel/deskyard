import assert from "node:assert/strict";
import { test } from "node:test";
import { cliLoginId, parseLinkFragment, safeNext } from "./links.ts";

test("a link's code is the whole fragment when it is one", () => {
  assert.equal(parseLinkFragment("#dyl_ab_cd"), "dyl_ab_cd");
  assert.equal(parseLinkFragment("#dyl_0a1b2c3d_Jx3vT9pLr7-KzA1_mYc5"), "dyl_0a1b2c3d_Jx3vT9pLr7-KzA1_mYc5");
});

test("any other fragment is no code", () => {
  for (const hash of ["", "#", "dyl_ab_cd", "#dyc_ab_cd", "#dyl_ab", "#dyl__cd", "#dyl_ab_cd&x=1", "#dyl_ab_cd ", "#xdyl_ab_cd"]) {
    assert.equal(parseLinkFragment(hash), "", hash);
  }
});

test("a CLI login request's id is the fragment", () => {
  assert.equal(cliLoginId("#k3j9QxZ"), "k3j9QxZ");
  assert.equal(cliLoginId(""), "");
  assert.equal(cliLoginId("#"), "");
  assert.equal(cliLoginId("#a/b"), "");
});

test("only paths on this origin are places to go next", () => {
  assert.equal(safeNext(null), "/");
  assert.equal(safeNext("/cli-login#abc"), "/cli-login#abc");
  assert.equal(safeNext("//evil.example"), "/");
  assert.equal(safeNext("/\\evil.example"), "/");
  assert.equal(safeNext("https://evil.example/"), "/");
});
