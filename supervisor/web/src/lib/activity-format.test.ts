import assert from "node:assert/strict";
import { test } from "node:test";
import type { AgentPresence } from "../api.ts";
import { agentLabel, agentTitle, nowLine, recentLine, shortDuration } from "./activity-format.ts";

const base = { id: "x", color: "#123456", active: false, lastActive: "2026-10-22T10:00:00Z" };
const bot: AgentPresence = { ...base, name: "deploy-bot", owner: "zef", inside: false };
const claude: AgentPresence = { ...base, name: "Claude Code", owner: "", inside: true };

test("agent titles and labels", () => {
  assert.equal(agentTitle(bot), "deploy-bot · zef's agent");
  assert.equal(agentTitle(claude), "Claude Code · in this desktop");
  assert.equal(agentLabel(claude), "Claude Code (in this desktop)");
  assert.equal(agentLabel(bot), "deploy-bot");
});

test("what is running now", () => {
  const t0 = Date.parse("2026-10-22T10:00:00Z");
  const since = new Date(t0).toISOString();
  assert.equal(nowLine({ kind: "command", text: "npm test", since }, t0 + 125_000), "npm test · 2 min");
  assert.equal(nowLine({ kind: "command", text: "npm test", since }, t0 + 5_000), "npm test · 5 s");
  assert.equal(nowLine({ kind: "browser", since }), "using the browser");
  assert.equal(nowLine({ kind: "forward", text: "3000 in · 5173 out", since }), "ports 3000 in · 5173 out");
});

test("screen actions", () => {
  const screen = (action: string, count?: number) => recentLine({ kind: "screen", t: "", action, ...(count === undefined ? {} : { count }) });
  assert.equal(screen("click", 14), "clicked 14 times");
  assert.equal(screen("click", 1), "clicked");
  assert.equal(screen("click"), "clicked");
  assert.equal(screen("type", 3), "typed 3 times");
  assert.equal(screen("screenshot", 3), "took 3 screenshots");
  assert.equal(screen("screenshot", 1), "took a screenshot");
  assert.equal(screen("double-click"), "double-clicked");
  assert.equal(screen("triple-click"), "triple-clicked");
  assert.equal(screen("drag"), "dragged");
  assert.equal(screen("scroll"), "scrolled");
  assert.equal(screen("key"), "pressed keys");
  assert.equal(screen("hold-key"), "held a key");
  assert.equal(screen("move"), "moved the mouse");
  assert.equal(screen("mouse-down"), "pressed the mouse button");
  assert.equal(screen("mouse-up"), "released the mouse button");
  assert.equal(screen("zoom"), "zoomed in");
  assert.equal(screen("zoom", 3), "zoomed in 3 times");
});

test("commands, browser and copies", () => {
  assert.equal(recentLine({ kind: "command", t: "", text: "make", exit: 0, durationMs: 12_000 }), "make · exit 0 · 12 s");
  assert.equal(recentLine({ kind: "command", t: "", text: "sleep 30", disconnected: true, durationMs: 4_000 }), "sleep 30 · disconnected · 4 s");
  assert.equal(recentLine({ kind: "browser", t: "", durationMs: 240_000 }), "used the browser · 4 min");
  assert.equal(recentLine({ kind: "copy", t: "", direction: "in", path: "/workspace/a.pdf", size: 1_200_000 }), "copied a.pdf in · 1.2 MB");
  assert.equal(recentLine({ kind: "copy", t: "", direction: "out", path: "b.txt" }), "copied b.txt out");
});

test("short durations", () => {
  assert.equal(shortDuration(0), "0 s");
  assert.equal(shortDuration(59_900), "59 s");
  assert.equal(shortDuration(60_000), "1 min");
  assert.equal(shortDuration(3_599_000), "59 min");
  assert.equal(shortDuration(3_600_000), "1 h 0 min");
  assert.equal(shortDuration(5_400_000), "1 h 30 min");
});
