// Small pure helpers for grants, shares, login links and CLI login.
import type { Grant, Level } from "../api.ts";

/**
 * The code of a login link, `dyl_<id>_<secret>`, from the /link page's
 * fragment (the browser never sends it to the server); "" for anything else.
 */
export function parseLinkFragment(hash: string): string {
  const m = /^#(dyl_[A-Za-z0-9]+_[A-Za-z0-9_-]+)$/.exec(hash);
  return m?.[1] ?? "";
}

/** The id of a CLI login request from the /cli-login page's fragment; "" for none. */
export function cliLoginId(hash: string): string {
  const m = /^#([A-Za-z0-9_-]{1,128})$/.exec(hash);
  return m?.[1] ?? "";
}

export function levelLabel(level: Level): string {
  return level === "observe" ? "Observe only" : "Full access";
}

/** "use alice and observe notes": what an account's grants let it do. */
export function grantsLine(grants: Grant[]): string {
  const verb = (l: Level) => (l === "observe" ? "observe" : "use");
  const parts = grants.map((g) => `${verb(g.level)} ${g.environment}`);
  if (parts.length <= 1) return parts[0] ?? "";
  return `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`;
}

export type TimeLimit = "none" | "hour" | "day" | "week" | "date";

export const timeLimits: { value: TimeLimit; label: string }[] = [
  { value: "none", label: "None" },
  { value: "hour", label: "1 hour" },
  { value: "day", label: "1 day" },
  { value: "week", label: "1 week" },
  { value: "date", label: "Date…" },
];

const HOUR = 3600 * 1000;

/** When a time limit ends: null for none, undefined while its date is missing. A date lasts to its end, local time. */
export function expiresAt(limit: TimeLimit, date: string, now = Date.now()): Date | null | undefined {
  switch (limit) {
    case "none":
      return null;
    case "hour":
      return new Date(now + HOUR);
    case "day":
      return new Date(now + 24 * HOUR);
    case "week":
      return new Date(now + 7 * 24 * HOUR);
    case "date":
      return date ? new Date(`${date}T23:59:59`) : undefined;
  }
}

/** A date input's value for a day. */
export function isoDate(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/** Only paths on this origin: never send someone elsewhere after signing in. */
export function safeNext(next: string | null): string {
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/\\")) return "/";
  return next;
}
