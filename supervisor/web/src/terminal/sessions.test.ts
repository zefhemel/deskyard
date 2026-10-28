import assert from "node:assert/strict";
import { test } from "node:test";
import { isShell, needsConfirm, others, validSession } from "./sessions.ts";

test("others leaves out this page's own attachment", () => {
  assert.equal(others({ clients: 0 }, false), 0);
  assert.equal(others({ clients: 1 }, true), 0);
  assert.equal(others({ clients: 1 }, false), 1);
  assert.equal(others({ clients: 3 }, true), 2);
  // A list from before this page attached never goes negative.
  assert.equal(others({ clients: 0 }, true), 0);
});

test("shells are recognised, including login shells", () => {
  for (const s of ["bash", "sh", "zsh", "fish", "dash", "-bash", "-zsh"]) assert.equal(isShell(s), true, s);
  for (const s of ["sleep", "vim", "htop", "", "bash2", "python3"]) assert.equal(isShell(s), false, s);
});

test("ending asks first when someone else is attached or a program runs", () => {
  assert.equal(needsConfirm({ clients: 1, command: "bash" }, true), false);
  assert.equal(needsConfirm({ clients: 0, command: "zsh" }, false), false);
  assert.equal(needsConfirm({ clients: 2, command: "bash" }, true), true);
  assert.equal(needsConfirm({ clients: 1, command: "bash" }, false), true);
  assert.equal(needsConfirm({ clients: 1, command: "sleep" }, true), true);
  assert.equal(needsConfirm({ clients: 0, command: "" }, false), true);
});

test("session names: 1-64 letters, digits, underscores or dashes, starting with a letter or digit", () => {
  for (const ok of ["main", "terminal-1", "a", "9_x", "a".repeat(64)]) assert.ok(validSession(ok), ok);
  for (const bad of ["", "-x", "_x", "a b", "a/b", "a".repeat(65)]) assert.ok(!validSession(bad), bad);
});
