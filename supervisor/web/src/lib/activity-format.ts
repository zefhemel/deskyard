// Every user-visible string of the agent activity UI.
import type { ActivityNow, ActivityRecent, AgentPresence } from "../api.ts";
import { fileSize, names, shortDuration } from "./format.ts";

export { shortDuration };

/** The overlay label: the name, or "Claude Code (in this computer)". */
export function agentLabel(a: Pick<AgentPresence, "name" | "inside">): string {
  return a.inside ? `${a.name} (in this computer)` : a.name;
}

/** "deploy-bot · zef's CLI" or "Claude Code · in this computer". */
export function agentTitle(a: AgentPresence): string {
  return a.inside ? `${a.name} · in this computer` : `${a.name} · ${a.owner}'s CLI`;
}

/** What an agent is doing now: "npm test · 2 min", "using the browser", "ports 3000 in · 5173 out". */
export function nowLine(n: ActivityNow, now = Date.now()): string {
  switch (n.kind) {
    case "command":
      return `${n.text ?? "a command"} · ${shortDuration(now - Date.parse(n.since))}`;
    case "browser":
      return "using the browser";
    case "forward":
      return `ports ${n.text ?? ""}`.trim();
  }
}

const screenVerbs: Record<string, string> = {
  click: "clicked",
  "double-click": "double-clicked",
  "triple-click": "triple-clicked",
  drag: "dragged",
  scroll: "scrolled",
  key: "pressed keys",
  "hold-key": "held a key",
  move: "moved the mouse",
  "mouse-down": "pressed the mouse button",
  "mouse-up": "released the mouse button",
};

function times(verb: string, count: number | undefined): string {
  return count && count > 1 ? `${verb} ${count} times` : verb;
}

function screenLine(action: string, count: number | undefined): string {
  if (action === "type") return times("typed", count);
  if (action === "zoom") return times("zoomed in", count);
  if (action === "screenshot") return count && count > 1 ? `took ${count} screenshots` : "took a screenshot";
  const verb = screenVerbs[action];
  return verb ? times(verb, count) : times(action, count);
}

function basename(path: string): string {
  return path.split("/").filter(Boolean).pop() ?? path;
}

/** One "Recently" line: "clicked 14 times", "make · exit 0 · 12 s", "copied a.pdf in · 1.2 MB". */
export function recentLine(r: ActivityRecent): string {
  switch (r.kind) {
    case "screen":
      return screenLine(r.action ?? "", r.count);
    case "command": {
      const parts = [r.text ?? "a command"];
      if (r.disconnected) parts.push("disconnected");
      else if (r.exit !== undefined) parts.push(`exit ${r.exit}`);
      if (r.durationMs !== undefined) parts.push(shortDuration(r.durationMs));
      return parts.join(" · ");
    }
    case "browser":
      return r.durationMs !== undefined ? `used the browser · ${shortDuration(r.durationMs)}` : "used the browser";
    case "copy": {
      const line = `copied ${basename(r.path ?? "a file")} ${r.direction ?? ""}`.trim();
      return r.size !== undefined ? `${line} · ${fileSize(r.size)}` : line;
    }
  }
}

/** The wall tile's badge text: people first, then agents; inside agents always say "(in this computer)". */
export function badgeLabel(viewers: string[], agents: AgentPresence[], long = false): string {
  const people = long
    ? `${names(viewers)} ${viewers.length === 1 ? "is" : "are"} viewing`
    : viewers.length === 1
      ? (viewers[0] ?? "")
      : `${viewers.length} viewing`;
  const bots =
    agents.length === 1
      ? `${agentLabel(agents[0]!)}${long ? " is here" : ""}`
      : `${agents.length} CLI clients${long ? " here" : ""}`;
  return [viewers.length ? people : "", agents.length ? bots : ""].filter(Boolean).join(" · ");
}
