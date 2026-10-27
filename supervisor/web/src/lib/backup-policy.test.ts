import { test } from "node:test";
import assert from "node:assert/strict";
import { describeReason, describeRetention, describeSchedule, policyDefaults, retentionErrors } from "./backup-policy.ts";

test("schedules read as sentences", () => {
  assert.equal(describeSchedule({ every: "daily", at: "03:00", weekday: 0 }), "Daily at 03:00");
  assert.equal(describeSchedule({ every: "hourly", at: "00:15", weekday: 0 }), "Hourly at :15");
  assert.equal(describeSchedule({ every: "weekly", at: "03:00", weekday: 0 }), "Weekly on Sunday at 03:00");
  assert.equal(describeSchedule({ every: "weekly", at: "22:30", weekday: 6 }), "Weekly on Saturday at 22:30");
});

test("retention reads as what is kept, and must keep something", () => {
  assert.equal(describeRetention({ last: 0, hourly: 0, daily: 7, weekly: 4, monthly: 12, yearly: 0 }), "7 daily, 4 weekly, 12 monthly");
  assert.equal(describeRetention({ last: 3, hourly: 0, daily: 0, weekly: 0, monthly: 0, yearly: 1 }), "the last 3, 1 yearly");
  assert.equal(retentionErrors({ last: 0, hourly: 0, daily: 0, weekly: 0, monthly: 0, yearly: 0 }), "Keep at least one snapshot.");
  assert.equal(retentionErrors({ last: -1, hourly: 0, daily: 7, weekly: 0, monthly: 0, yearly: 0 }), "Counts are whole numbers from 0.");
  assert.equal(retentionErrors({ last: 0, hourly: 0, daily: 7, weekly: 0, monthly: 0, yearly: 0 }), null);
});

test("a new policy starts from the defaults and the only repository", () => {
  assert.deepEqual(policyDefaults([{ name: "home" }]), {
    repository: "home",
    enabled: true,
    schedule: { every: "daily", at: "03:00", weekday: 0 },
    retention: { last: 0, hourly: 0, daily: 7, weekly: 4, monthly: 12, yearly: 0 },
  });
  assert.equal(policyDefaults([{ name: "a" }, { name: "b" }]).repository, "");
  assert.equal(policyDefaults([]).repository, "");
});

test("snapshot reasons have labels", () => {
  assert.equal(describeReason("pre-restore"), "Before a restore");
  assert.equal(describeReason("manual"), "Manual");
  assert.equal(describeReason("scheduled"), "Scheduled");
  assert.equal(describeReason(""), "");
});

test("Go's zero time means never", async () => {
  const { isTime } = await import("./backup-policy.ts");
  assert.equal(isTime("0001-01-01T00:00:00Z"), false);
  assert.equal(isTime(""), false);
  assert.equal(isTime(null), false);
  assert.equal(isTime("2026-10-27T03:00:00Z"), true);
});

test("an item's backup status: failed, overdue, with files left out, or fine", async () => {
  const { backupStatus } = await import("./backup-policy.ts");
  const now = Date.parse("2026-10-27T12:00:00Z");
  const policy = { item: "volume:v", repository: "r", enabled: true, schedule: { every: "daily" as const, at: "03:00", weekday: 0 }, retention: { last: 0, hourly: 0, daily: 7, weekly: 0, monthly: 0, yearly: 0 }, updated: "" };
  const item = (state: object, extra: object = {}) => ({ item: "volume:v", policy, state: { lastAttempt: "", lastSuccess: "0001-01-01T00:00:00Z", ...state }, activity: "", nextRun: "2026-10-28T03:00:00Z", ...extra });
  assert.equal(backupStatus(undefined, now), null);
  assert.equal(backupStatus({ ...item({}), policy: null }, now), null);
  assert.deepEqual(backupStatus(item({ lastSuccess: "2026-10-27T03:00:00Z" }), now), { label: "Backed up", kind: "ok" });
  assert.deepEqual(backupStatus(item({}), now), { label: "Backup pending", kind: "plain" });
  assert.deepEqual(backupStatus(item({ error: "boom" }), now), { label: "Backup failed", kind: "danger" });
  assert.deepEqual(backupStatus(item({ lastSuccess: "2026-10-27T03:00:00Z", warnings: ["a", "b"] }), now), { label: "Backed up, 2 files left out", kind: "update" });
  // Due over an hour ago and not run: the scheduler isn't keeping up, or the supervisor was down.
  assert.deepEqual(backupStatus(item({ lastSuccess: "2026-10-25T03:00:00Z" }, { nextRun: "2026-10-27T03:00:00Z" }), now), { label: "Backup overdue", kind: "danger" });
  assert.deepEqual(backupStatus(item({ lastSuccess: "2026-10-25T03:00:00Z" }, { nextRun: "2026-10-27T03:00:00Z", activity: "backing up" }), now), { label: "Backing up", kind: "update" });
});
