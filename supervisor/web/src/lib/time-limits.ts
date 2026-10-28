// Time limits on shares and accounts, and the expiry they make.

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

/**
 * An account change's `expires`: the picked expiry once changed, while the
 * account can expire; none (null) when it can't (an admin with a password)
 * but had one, as when it becomes one; left out otherwise.
 */
export function expiryChange(o: { canExpire: boolean; changeExpiry: boolean; when: Date | null | undefined; hadExpiry: boolean }): string | null | undefined {
  if (!o.canExpire) return o.hadExpiry ? null : undefined;
  if (!o.changeExpiry || o.when === undefined) return undefined;
  return o.when ? o.when.toISOString() : null;
}

/** A date input's value for a day. */
export function isoDate(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}
