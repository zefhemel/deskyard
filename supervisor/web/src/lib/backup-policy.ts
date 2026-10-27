import type { PolicyInput, Retention, Schedule } from "../api.ts";

const DAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

export function describeSchedule(s: Schedule): string {
  switch (s.every) {
    case "hourly":
      return `Hourly at :${(s.at || "00:00").slice(3)}`;
    case "weekly":
      return `Weekly on ${DAYS[s.weekday] ?? "?"} at ${s.at}`;
    default:
      return `Daily at ${s.at}`;
  }
}

const PERIODS: [keyof Retention, string][] = [
  ["hourly", "hourly"],
  ["daily", "daily"],
  ["weekly", "weekly"],
  ["monthly", "monthly"],
  ["yearly", "yearly"],
];

export function describeRetention(r: Retention): string {
  const parts = r.last > 0 ? [`the last ${r.last}`] : [];
  for (const [k, label] of PERIODS) if (r[k] > 0) parts.push(`${r[k]} ${label}`);
  return parts.join(", ");
}

/** Why the retention can't be saved, or null. */
export function retentionErrors(r: Retention): string | null {
  const counts = Object.values(r);
  if (counts.some((n) => !Number.isInteger(n) || n < 0)) return "Counts are whole numbers from 0.";
  if (counts.every((n) => n === 0)) return "Keep at least one snapshot.";
  return null;
}

export const DEFAULT_SCHEDULE: Schedule = { every: "daily", at: "03:00", weekday: 0 };
export const DEFAULT_RETENTION: Retention = { last: 0, hourly: 0, daily: 7, weekly: 4, monthly: 12, yearly: 0 };

/** A new policy: the defaults, and the only repository when there is just one. */
export function policyDefaults(repositories: { name: string }[]): PolicyInput {
  return { repository: repositories.length === 1 ? (repositories[0]?.name ?? "") : "", enabled: true, schedule: { ...DEFAULT_SCHEDULE }, retention: { ...DEFAULT_RETENTION } };
}

export function describeReason(reason: string): string {
  return { scheduled: "Scheduled", manual: "Manual", "pre-restore": "Before a restore" }[reason] ?? "";
}

/** "environment:alice" → ["environment", "alice"]. */
export function splitItem(item: string): ["environment" | "volume", string] {
  const i = item.indexOf(":");
  return [item.slice(0, i) as "environment" | "volume", item.slice(i + 1)];
}

/** Whether a time from the API is set: Go sends its zero time for never. */
export function isTime(iso: string | null | undefined): iso is string {
  return !!iso && !iso.startsWith("0001-");
}
