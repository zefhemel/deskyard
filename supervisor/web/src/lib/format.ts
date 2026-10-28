import type { Environment, Settings } from "../api.ts";

const GB = 1024 ** 3;

/** "1.4 GB", "512 MB" */
export function bytes(n: number): string {
  if (n >= GB) return `${trim(n / GB)} GB`;
  if (n >= 1024 ** 2) return `${Math.round(n / 1024 ** 2)} MB`;
  if (n >= 1024) return `${Math.round(n / 1024)} KB`;
  return `${n} B`;
}

/** One decimal, without a trailing ".0". */
export function trim(n: number): string {
  return n >= 10 ? String(Math.round(n)) : n.toFixed(1).replace(/\.0$/, "");
}

export function gb(n: number): string {
  return trim(n / GB);
}

/** "1.4 of 2 GB" or "0.9 GB" when there is no limit. */
export function memoryOf(used: number, limit: number | null): string {
  return limit ? `${gb(used)} of ${gb(limit)} GB` : `${gb(used)} GB`;
}

const units: [number, string][] = [
  [86400, "day"],
  [3600, "hour"],
  [60, "minute"],
];

/** "3 days", "5 hours", "12 minutes", "a few seconds" */
export function duration(seconds: number): string {
  for (const [size, unit] of units) {
    if (seconds >= size) {
      const n = Math.floor(seconds / size);
      return `${n} ${unit}${n === 1 ? "" : "s"}`;
    }
  }
  return "a few seconds";
}

export function since(iso: string, now = Date.now()): string {
  return duration(Math.max(0, (now - Date.parse(iso)) / 1000));
}

/** "2 minutes ago", "Today, 03:12", "8 October" */
export function ago(iso: string | null, now = Date.now()): string {
  if (!iso) return "Never";
  const t = Date.parse(iso);
  const s = (now - t) / 1000;
  if (s < 60) return "Just now";
  if (s < 3600) return `${duration(s)} ago`;
  const d = new Date(t);
  if (sameDay(d, new Date(now))) return `Today, ${time(d)}`;
  if (s < 7 * 86400) return `${duration(s)} ago`;
  return day(iso);
}

function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

function time(d: Date): string {
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false });
}

/** "12 October" (with the year when it isn't this year) */
export function day(iso: string): string {
  const d = new Date(iso);
  const opts: Intl.DateTimeFormatOptions = { day: "numeric", month: "long" };
  if (d.getFullYear() !== new Date().getFullYear()) opts.year = "numeric";
  return d.toLocaleDateString("en-GB", opts);
}

/** "28 Oct 2026, 14:05" in the browser's locale: a snapshot's or a run's time. */
export function dateTime(iso: string): string {
  return new Date(iso).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

/** "in 26 days" */
export function until(iso: string, now = Date.now()): string {
  return `in ${duration(Math.max(0, (Date.parse(iso) - now) / 1000))}`;
}

/** The status line of an environment: "Running for 3 days", "Stopped", "Creating". */
export function statusLine(c: Environment, now = Date.now()): string {
  switch (c.state) {
    case "running":
      return c.startedAt ? `Running for ${since(c.startedAt, now)}` : "Running";
    case "stopped":
      return "Stopped";
    case "starting":
      return "Starting";
    case "creating":
      return "Creating";
    case "upgrading":
      return "Upgrading";
    case "removing":
      return "Removing";
  }
}

export type StatusKind = "run" | "busy" | "off";

export function statusKind(c: Pick<Environment, "state">): StatusKind {
  if (c.state === "running") return "run";
  if (c.state === "stopped") return "off";
  return "busy";
}

export interface Notice {
  kind: "error" | "pending";
  text: string;
}

/**
 * What an environment's tile and page say beyond its state: a container
 * conflict, why its last change failed, or a saved change (revision ahead of
 * the container's) that isn't applied yet. Busy environments wait for their job.
 */
export function notice(c: Pick<Environment, "state" | "revision" | "appliedRevision" | "error" | "conflict">): Notice | null {
  if (c.conflict) return { kind: "error", text: c.conflict };
  if (statusKind(c) === "busy") return null;
  // Without a container there is nothing to be behind: start builds it.
  const ahead = c.appliedRevision > 0 && c.revision > c.appliedRevision;
  const retry = c.state === "running" ? "restart" : "start";
  if (c.error) {
    return { kind: "error", text: ahead ? `${sentence(c.error)} Saved changes aren't applied; the previous container is kept. ${cap(retry)} it to try again.` : c.error };
  }
  if (ahead) {
    return { kind: "pending", text: c.state === "running" ? "Saved changes aren't applied yet; restart to apply them." : "Saved changes apply when it starts." };
  }
  return null;
}

function cap(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function sentence(s: string): string {
  return /[.!?]$/.test(s) ? s : `${s}.`;
}

/** "2 GB memory, 2 CPUs", "No limits" */
export function limits(s: Settings): string {
  const parts: string[] = [];
  if (s.memoryBytes) parts.push(`${gb(s.memoryBytes)} GB memory`);
  if (s.nanoCpus) {
    const n = trim(s.nanoCpus / 1e9);
    parts.push(`${n} CPU${n === "1" ? "" : "s"}`);
  }
  return parts.length ? parts.join(", ") : "No limits";
}

/** "zef", "zef and anna", "zef, anna and sam" */
/** Viewer names with yourself first, the rest in their given order. */
export function selfFirst(list: string[], self: string): string[] {
  return list.includes(self) ? [self, ...list.filter((n) => n !== self)] : list;
}

export function names(list: string[]): string {
  if (list.length <= 1) return list.join("");
  return `${list.slice(0, -1).join(", ")} and ${list[list.length - 1]}`;
}

export function plural(n: number, word: string, many = `${word}s`): string {
  return `${n} ${n === 1 ? word : many}`;
}

/** "12 s", "4 min", "1 h 30 min" (for commands and browser sessions). */
export function shortDuration(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} min`;
  return `${Math.floor(m / 60)} h ${m % 60} min`;
}

/** A file's size the way file managers show it (powers of 1000): "1.2 MB", "340 KB". */
export function fileSize(n: number): string {
  if (n >= 1e9) return `${trim(n / 1e9)} GB`;
  if (n >= 1e6) return `${trim(n / 1e6)} MB`;
  if (n >= 1e3) return `${Math.round(n / 1e3)} KB`;
  return `${n} B`;
}
