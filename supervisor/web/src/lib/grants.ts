// Access levels and grants, in words.
import type { Grant, Level } from "../api.ts";

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
