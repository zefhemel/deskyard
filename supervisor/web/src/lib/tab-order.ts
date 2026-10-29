// The order of a computer's terminal tabs, kept per viewer in browser
// storage. Desktop is always first and isn't part of the order.

const key = (computer: string) => `deskyard.tabs.${computer}`;

/** Stored order first, then sessions it doesn't know sorted by name; gone sessions are dropped. */
export function orderTabs(sessions: string[], stored: string[]): string[] {
  const live = new Set(sessions);
  const known = [...new Set(stored)].filter((s) => live.has(s));
  const seen = new Set(known);
  const fresh = sessions.filter((s) => !seen.has(s)).sort((a, b) => a.localeCompare(b));
  return [...known, ...[...new Set(fresh)]];
}

/** Moves a tab one place left (-1) or right (1). */
export function moveTab(order: string[], name: string, delta: -1 | 1): string[] {
  const i = order.indexOf(name);
  const j = i + delta;
  if (i < 0 || j < 0 || j >= order.length) return order;
  const next = [...order];
  next[i] = order[j]!;
  next[j] = name;
  return next;
}

/** Moves a tab to just before another, or to the end when `before` is null. */
export function moveTabTo(order: string[], name: string, before: string | null): string[] {
  if (name === before || !order.includes(name)) return order;
  const rest = order.filter((s) => s !== name);
  const at = before === null ? -1 : rest.indexOf(before);
  if (at < 0) return [...rest, name];
  return [...rest.slice(0, at), name, ...rest.slice(at)];
}

/** The tab to show after closing `name`: its left neighbour, or null for Desktop. */
export function leftNeighbour(order: string[], name: string): string | null {
  const i = order.indexOf(name);
  return i > 0 ? order[i - 1]! : null;
}

export function loadOrder(env: string): string[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(key(env)) ?? "[]");
    return Array.isArray(value) ? value.filter((s): s is string => typeof s === "string") : [];
  } catch {
    return [];
  }
}

export function saveOrder(env: string, order: string[]): void {
  try {
    localStorage.setItem(key(env), JSON.stringify(order));
  } catch {
    // private mode or blocked storage: not remembered
  }
}

/**
 * Sessions seen in the list that it no longer shows: their tabs close. A
 * session opened here again meanwhile (pending, e.g. + reusing an ended
 * name) is left alone, since a late list may predate it.
 */
export function vanished(seen: Iterable<string>, listed: string[], pending: string[]): string[] {
  return [...seen].filter((s) => !listed.includes(s) && !pending.includes(s));
}
