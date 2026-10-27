// Fake data for the dev-server mock (mock.ts). Never part of the build.
import type { Account, ActivityEvent, AgentPresence, AgentSummary, CatalogImage, Conflict, Credential, Environment } from "../api.ts";

const GB = 1024 ** 3;
const HOUR = 3600 * 1000;
const DAY = 24 * HOUR;
const now = Date.now();
const iso = (msAgo: number) => new Date(now - msAgo).toISOString();

export const FOLDER = "/Users/zef/deskyard";
const PLAIN = { installed: true, official: false } as const;
const LATEST = "ghcr.io/zefhemel/deskyard-debian-xfce:latest";
const BETA = "ghcr.io/zefhemel/deskyard-debian-xfce:beta";

const AGENT_COLORS: Record<string, string> = { "My laptop": "#e0457b", "a terminal": "#d97706", "Work laptop": "#2f9e8f" };

/** A mock agent last active `secsAgo` seconds ago (active means in the last 10). */
function agent(name: string, owner: string, inside: boolean, secsAgo: number): AgentPresence {
  return {
    id: inside ? `inside:${name.toLowerCase().replace(/ /g, "-")}` : `tok-${name.replace(/ /g, "-")}`,
    name,
    owner,
    inside,
    color: AGENT_COLORS[name] ?? "#6b7280",
    active: secsAgo < 10,
    lastActive: iso(secsAgo * 1000),
  };
}

function environment(c: Partial<Environment> & { name: string }): Environment {
  return {
    state: "running",
    power: c.state === "stopped" ? "stopped" : "running",
    revision: 1,
    appliedRevision: 1,
    error: "",
    conflict: "",
    startedAt: iso(HOUR),
    created: iso(30 * DAY),
    job: null,
    folder: `${FOLDER}/home/${c.name}`,
    image: LATEST,
    imageTitle: "Debian XFCE",
    imageVersion: "2026.10.22",
    memoryReclaim: true,
    updateAvailable: null,
    vpn: {state:"disabled",changedAt:"0001-01-01T00:00:00Z",exitIpObservedAt:"0001-01-01T00:00:00Z",desiredRevision:0,appliedRevision:0,idle:false},
    settings: { dockerAccess: false, memoryBytes: 0, nanoCpus: 0, vpnConnectionId: null, lanExceptions: [], mounts: [], volumes: [] },
    // level and access come from the accounts' grants (mock.ts).
    level: "full",
    access: [],
    viewers: [],
    observers: [],
    mics: [],
    terminal: true,
    terminals: [],
    forwards: [],
    agents: [],
    stats: null,
    ...c,
  };
}

export const environments: Environment[] = [
  environment({
    name: "alice",
    startedAt: iso(3 * DAY),
    imageVersion: "2026.10.15",
    updateAvailable: "2026.10.22",
    settings: { dockerAccess: false, memoryBytes: 2 * GB, nanoCpus: 2e9, vpnConnectionId: null, lanExceptions: [], mounts: [], volumes: [{ name: "photos", readOnly: false }] },
    viewers: ["zef", "anna", "sanne"],
    observers: ["sanne"],
    forwards: [
      { token: "My laptop", direction: "in", port: 3000 },
      { token: "My laptop", direction: "out", port: 5173 },
    ],
    agents: [agent("My laptop", "zef", false, 2), agent("a terminal", "", true, 4), agent("Work laptop", "anna", false, 270)],
    terminals: [{ name: "main", clients: 1, command: "bash" }, { name: "terminal-1", clients: 0, command: "htop" }, { name: "build", clients: 2, command: "zsh" }],
    stats: { cpuPercent: 34, memBytes: 1.4 * GB, memLimit: 2 * GB },
  }),
  environment({
    name: "research",
    startedAt: iso(5 * HOUR),
    settings: { dockerAccess: false, memoryBytes: 0, nanoCpus: 0, vpnConnectionId: null, lanExceptions: [], mounts: [], volumes: [{ name: "photos", readOnly: true }] },
    viewers: ["anna"],
    terminals: [{ name: "main", clients: 0, command: "bash" }],
    stats: { cpuPercent: 8, memBytes: 0.9 * GB, memLimit: 16 * GB },
  }),
  environment({
    name: "build-bot",
    startedAt: iso(12 * 60 * 1000),
    settings: { dockerAccess: false, memoryBytes: 4 * GB, nanoCpus: 4e9, vpnConnectionId: null, lanExceptions: [], mounts: [], volumes: [] },
    agents: [agent("a terminal", "", true, 1)],
    stats: { cpuPercent: 91, memBytes: 3.1 * GB, memLimit: 4 * GB },
  }),
  environment({
    name: "notes",
    startedAt: iso(2 * DAY),
    imageVersion: "2026.10.15",
    updateAvailable: "2026.10.22",
    stats: { cpuPercent: 2, memBytes: 0.7 * GB, memLimit: 16 * GB },
  }),
  environment({ name: "data-lab", state: "creating", startedAt: "", imageVersion: "" }),
  // A saved change that applies on the next start.
  environment({ name: "scratch", state: "stopped", startedAt: "", imageVersion: "2026.10.15", updateAvailable: "2026.10.22", revision: 3, appliedRevision: 2 }),
  environment({
    name: "work",
    state: "stopped",
    startedAt: "",
    image: "deskyard-custom/work-signed-in:latest",
    imageTitle: "Work, signed in",
    imageVersion: "2026-10-20 09:14",
    updateAvailable: "2026-10-22 08:30",
    // A change whose apply failed: the old container was put back.
    revision: 5,
    appliedRevision: 4,
    error: "mount source /srv/photos is missing on the engine host",
  }),
  // Defined but without a container: it's built on start.
  environment({ name: "archive", state: "stopped", power: "stopped", startedAt: "", appliedRevision: 0 }),
];

/** Volumes as in volumes.json; who uses each comes from the environments' grants. */
export const volumes: { name: string; created: string; kind: "local" }[] = [
  { name: "photos", created: iso(20 * DAY), kind: "local" },
  { name: "datasets", created: iso(4 * DAY), kind: "local" },
];

/** Containers of this supervisor's that it can't match to a definition. */
export const conflicts: Conflict[] = [
  { name: "old-lab", kind: "unknown", containers: ["deskyard-old-lab"], message: "deskyard-old-lab claims environment old-lab, which isn't defined" },
];

/** An account as accounts.json holds it: password null for a token account; linkPending comes from `links`. */
export type MockAccount = Omit<Account, "kind" | "linkPending"> & { id: string; password: string | null };

export const accounts: MockAccount[] = [
  { id: "a0000001", username: "zef", admin: true, description: "", grants: [], sharedBy: null, expires: null, created: iso(30 * DAY), lastLogin: iso(0), password: "zef" },
  {
    id: "a0000002", username: "anna", admin: false, description: "", sharedBy: null, expires: null, created: iso(20 * DAY), lastLogin: iso(60 * 1000), password: "anna",
    grants: [{ environment: "alice", level: "full" }, { environment: "notes", level: "observe" }, { environment: "research", level: "full" }],
  },
  { id: "a0000003", username: "sam", admin: false, description: "", grants: [{ environment: "research", level: "full" }], sharedBy: null, expires: null, created: iso(10 * DAY), lastLogin: iso(3 * DAY), password: "sam" },
  { id: "a0000004", username: "ci", admin: false, description: "The build runner", grants: [{ environment: "build-bot", level: "full" }], sharedBy: null, expires: null, created: iso(2 * DAY), lastLogin: iso(HOUR), password: null },
  // Shares: token accounts with one grant, made by Share.
  {
    id: "a0000005", username: "sanne", admin: false, description: "", grants: [{ environment: "alice", level: "observe" }], sharedBy: { username: "zef", id: "a0000001" },
    expires: new Date(now + DAY).toISOString(), created: iso(2 * HOUR), lastLogin: iso(HOUR), password: null,
  },
  {
    id: "a0000006", username: "bob", admin: false, description: "", grants: [{ environment: "research", level: "full" }], sharedBy: { username: "anna", id: "a0000002" },
    expires: null, created: iso(3 * DAY), lastLogin: null, password: null,
  },
];

/**
 * Unused login links: code → username. Open /link#dyl_5a17e0aa_mock-bob-link
 * to sign in as bob (once); the Share dialog and "New login link" make more.
 */
export const links = new Map<string, string>([["dyl_5a17e0aa_mock-bob-link", "bob"]]);

/** Signed-in devices by username (current is worked out per request). */
export const credentials: Record<string, Omit<Credential, "current">[]> = {
  zef: [
    { id: "c3f9a1b2", kind: "cli", name: "deskyard CLI on zef-mbp", created: iso(9 * DAY), lastUsed: iso(2 * 60 * 1000) },
    { id: "c71d0e44", kind: "cli", name: "deskyard CLI on build-box", created: iso(25 * DAY), lastUsed: iso(6 * DAY) },
  ],
  ci: [{ id: "c0be11a9", kind: "cli", name: "deskyard CLI on runner-3", created: iso(2 * DAY), lastUsed: iso(HOUR) }],
};

/** Pending `deskyard login` requests; open /cli-login#mock-request to approve one. */
export const cliLogins = new Map<string, { client: string; ip: string; code: string; created: string; answer: "approved" | "denied" | null; approver: string | null }>([
  ["mock-request", { client: "deskyard CLI on zef-mbp", ip: "192.168.1.23", code: "KXQF-7RTB", created: iso(20 * 1000), answer: null, approver: null }],
]);

function customImage(slug: string, title: string, description: string, over: Partial<CatalogImage>): CatalogImage {
  return {
    ref: `deskyard-custom/${slug}:latest`,
    title,
    version: "2026-10-20 09:14",
    description,
    environments: [],
    behind: [],
    checked: null,
    installed: true,
    official: false,
    ...over,
  };
}

export const images: CatalogImage[] = [
  {
    ref: LATEST,
    title: "Debian XFCE",
    version: "2026.10.22",
    description: "Debian trixie with XFCE, everyday apps, Chromium, Flatpak, mise and uv",
    environments: [],
    behind: [],
    checked: iso(2 * HOUR),
    ...PLAIN,
    official: true,
  },
  {
    ref: BETA,
    title: "Debian XFCE, beta",
    version: "2026.10.22-beta",
    description: "Next week's image, for trying changes",
    environments: [],
    behind: [],
    checked: iso(2 * HOUR),
    ...PLAIN,
  },
  customImage("python-lab", "Python lab", "Python 3.13, uv and JupyterLab", {}),
  customImage("work-signed-in", "Work, signed in", "Chromium and the work apps, signed in to everything", {}),
  customImage("video-tools", "Video tools", "Kdenlive and ffmpeg", {}),
];

export const processRows: [number, string, number, number, string, string][] = [
  [412, "user", 18.2, 626688, "02:14:07", "/usr/lib/chromium/chromium --type=renderer --enable-features=… https://github.com/…"],
  [3301, "user", 9.6, 253952, "41:02", "node /workspace/.local/share/claude/versions/2.4.1/cli.js"],
  [131, "user", 7.8, 212992, "3-01:12:41", "/opt/selkies/bin/python3 /opt/selkies/bin/selkies --mode websockets --addr 127.0.0.1 --port 6080"],
  [118, "user", 1.1, 72704, "3-01:12:44", "Xvfb :1 -screen 0 3840x2160x24 +extension RANDR +extension DAMAGE -nolisten tcp"],
  [380, "user", 1.4, 190464, "02:14:09", "/usr/lib/chromium/chromium --remote-debugging-port=9220 --user-data-dir=…"],
  [2915, "user", 0.9, 135168, "01:02:33", "/app/bin/silverbullet --hostname 127.0.0.1 /workspace/Notes"],
  [205, "user", 0.4, 38912, "3-01:12:40", "xfce4-panel --display :1.0 --sm-client-id 2f1c…"],
  [3290, "user", 0.2, 9216, "41:00", "-bash"],
  [96, "caddy", 0.1, 31744, "3-01:12:45", "caddy run --config /etc/caddy/Caddyfile --adapter caddyfile"],
  [1, "root", 0.0, 24576, "3-01:12:46", "/usr/bin/python3 /usr/bin/supervisord -c /etc/supervisor/supervisord.conf"],
];

/** Which environments use which image, recomputed from the environments. */
export function refreshImages(): void {
  for (const img of images) {
    const on = environments.filter((c) => c.image === img.ref);
    img.environments = on.map((c) => c.name);
    img.behind = on.filter((c) => c.updateAvailable).map((c) => c.name);
  }
}

const ago = (secs: number) => iso(secs * 1000);

/** What each mock agent has been doing, newest first. Times are relative to now so the page looks alive. */
function summaryOf(a: AgentPresence): AgentSummary {
  if (a.name === "My laptop") {
    return {
      ...a,
      now: [
        { kind: "command", text: "npm test -- --watch", since: ago(125) },
        { kind: "browser", since: ago(40) },
        { kind: "forward", text: "3000 in · 5173 out", since: ago(600) },
      ],
      recent: [
        { kind: "screen", t: ago(3), action: "click", count: 14 },
        { kind: "screen", t: ago(20), action: "type", count: 3 },
        { kind: "command", t: ago(70), text: "make", exit: 0, durationMs: 12_000 },
        { kind: "command", t: ago(130), text: "sleep 30", disconnected: true, durationMs: 4_000 },
        { kind: "screen", t: ago(200), action: "screenshot", count: 3 },
        { kind: "browser", t: ago(300), durationMs: 240_000 },
        { kind: "copy", t: ago(420), direction: "in", path: "/workspace/report.pdf", size: 1_200_000 },
      ],
    };
  }
  if (a.name === "a terminal") {
    return {
      ...a,
      now: [{ kind: "command", text: "go test ./...", since: ago(18) }],
      recent: [
        { kind: "command", t: ago(60), text: "git status --short", exit: 0, durationMs: 300 },
        { kind: "command", t: ago(300), text: "rg activity supervisor/", exit: 1, durationMs: 2_000 },
      ],
    };
  }
  return { ...a, now: [], recent: [{ kind: "screen", t: ago(280), action: "screenshot", count: 1 }] };
}

export function activityOf(c: Environment): ActivityEvent {
  return { name: c.name, agents: c.agents.map(summaryOf) };
}
