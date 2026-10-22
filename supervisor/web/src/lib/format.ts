import type { Desktop, Settings, Tool } from "../api.ts";

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

/** "in 26 days" */
export function until(iso: string, now = Date.now()): string {
  return `in ${duration(Math.max(0, (Date.parse(iso) - now) / 1000))}`;
}

/** The status line of a desktop: "Running for 3 days", "Stopped", "Creating". */
export function statusLine(c: Desktop, now = Date.now()): string {
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

export function statusKind(c: Desktop): StatusKind {
  if (c.state === "running") return "run";
  if (c.state === "stopped") return "off";
  return "busy";
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
export function names(list: string[]): string {
  if (list.length <= 1) return list.join("");
  return `${list.slice(0, -1).join(", ")} and ${list[list.length - 1]}`;
}

export function plural(n: number, word: string, many = `${word}s`): string {
  return `${n} ${n === 1 ? word : many}`;
}

/** mise tools as "node 24.22 (this desktop) · 24.30 in the image, cowsay 1.6 (image)". */
export function toolsLine(tools: Tool[]): string {
  const byTool = new Map<string, { desktop: string[]; image: string[] }>();
  for (const t of tools) {
    const e = byTool.get(t.tool) ?? { desktop: [], image: [] };
    byTool.set(t.tool, e);
    e[t.source === "image" ? "image" : "desktop"].push(t.version);
  }
  return [...byTool]
    .map(([name, { desktop, image }]) => {
      const d = desktop.join(", ");
      const i = image.join(", ");
      if (d && i) return `${name} ${d} (this desktop) · ${i} in the image`;
      return `${name} ${d || i} (${d ? "this desktop" : "image"})`;
    })
    .join(", ");
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
