import type { BackupItem, ItemKind, PolicyInput, Retention, Schedule } from "../api.ts";

/** Weekdays as Schedule.weekday counts them. */
export const DAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

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

/** Why a policy can't be saved, by field (repository, at, retention); empty when it can. */
export function policyErrors(p: PolicyInput): Partial<Record<"repository" | "at" | "retention", string>> {
  const errs: Partial<Record<"repository" | "at" | "retention", string>> = {};
  if (!p.repository) errs.repository = "Choose a repository.";
  if (!/^\d\d:\d\d$/.test(p.schedule.at)) errs.at = "Pick a time.";
  const retention = retentionErrors(p.retention);
  if (retention) errs.retention = retention;
  return errs;
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

/** What the backups pages call an environment's or a volume's parts and places. */
export interface ItemCopy {
  /** Under "Not backed up": what a backup takes. */
  backsUp: string;
  /** The top of its snapshots' folders. */
  root: string;
  /** Where files restored to a folder land: "in the home folder". */
  restoredInto: string;
  /** What a restore here replaces (a host volume's folder when it has one). */
  replaced: (hostPath?: string) => string;
  newNameLabel: string;
  /** Where its backups are shown. */
  page: string;
  /** The page of a restored copy named `created`. */
  pageOf: (created: string) => string;
  /** What a restored copy is like once made. */
  ready: (created: string) => string;
  /** The environment a backup or restore job runs on, for its tile; none for volumes. */
  jobEnvironment: string | undefined;
}

export function itemCopy(kind: ItemKind, name: string): ItemCopy {
  if (kind === "environment") {
    return {
      backsUp: "Its settings and home folder, to a backup repository.",
      root: "Home",
      restoredInto: "in the home folder",
      replaced: () => `${name}'s home folder`,
      newNameLabel: "New environment's name",
      page: `/environments/${name}?tab=backups`,
      pageOf: (created) => `/environments/${created}`,
      ready: (created) => `${created} is ready, and stopped.`,
      jobEnvironment: name,
    };
  }
  return {
    backsUp: "Its files, to a backup repository.",
    root: name,
    restoredInto: "in the volume",
    replaced: (hostPath) => (hostPath ? `The host folder ${hostPath}` : `The volume ${name}`),
    newNameLabel: "New volume's name",
    page: `/volumes/${name}`,
    pageOf: (created) => `/volumes/${created}`,
    ready: (created) => `${created} is ready.`,
    jobEnvironment: undefined,
  };
}

/** "environment:alice" → ["environment", "alice"]. */
export function splitItem(item: string): [ItemKind, string] {
  const i = item.indexOf(":");
  return [item.slice(0, i) === "environment" ? "environment" : "volume", item.slice(i + 1)];
}

/** Whether a time from the API is set: Go sends its zero time for never. */
export function isTime(iso: string | null | undefined): iso is string {
  return !!iso && !iso.startsWith("0001-");
}

export type BackupStatus = { label: string; kind: "ok" | "plain" | "update" | "danger" };

/** Whether a scheduled backup is overdue: due over an hour ago and not run (nor running). */
export function isOverdue(b: BackupItem, now = Date.now()): boolean {
  return !!b.policy?.enabled && !b.activity && isTime(b.nextRun) && Date.parse(b.nextRun) < now - 3600_000;
}

/** An item's backup status as a tag: failed, overdue (due over an hour ago, not run), left files out, or fine; null without a policy. */
export function backupStatus(b: BackupItem | undefined, now = Date.now()): BackupStatus | null {
  if (!b?.policy) return null;
  const st = b.state;
  if (b.activity) return { label: b.activity[0]!.toUpperCase() + b.activity.slice(1), kind: "update" };
  if (st.error) return { label: "Backup failed", kind: "danger" };
  if (isOverdue(b, now)) return { label: "Backup overdue", kind: "danger" };
  if (st.warnings?.length) return { label: `Backed up, ${st.warnings.length} file${st.warnings.length === 1 ? "" : "s"} left out`, kind: "update" };
  return isTime(st.lastSuccess) ? { label: "Backed up", kind: "ok" } : { label: "Backup pending", kind: "plain" };
}
