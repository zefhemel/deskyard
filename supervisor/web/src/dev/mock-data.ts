// Fake data for the dev-server mock (mock.ts). Never part of the build.
import type { Account, CatalogImage, Desktop, Recipe, Token } from "../api.ts";

const GB = 1024 ** 3;
const HOUR = 3600 * 1000;
const DAY = 24 * HOUR;
const now = Date.now();
const iso = (msAgo: number) => new Date(now - msAgo).toISOString();

export const FOLDER = "/Users/zef/deskyard";
const PLAIN = { custom: false, secrets: false, baseUpdated: false, recipe: null } as const;
const LATEST = "ghcr.io/zefhemel/deskyard-debian-xfce:latest";
const BETA = "ghcr.io/zefhemel/deskyard-debian-xfce:beta";

function desktop(c: Partial<Desktop> & { name: string }): Desktop {
  return {
    id: `${c.name}-id`,
    state: "running",
    startedAt: iso(HOUR),
    created: iso(30 * DAY),
    job: null,
    folder: `${FOLDER}/${c.name}`,
    image: LATEST,
    imageTitle: "Debian XFCE",
    imageVersion: "2026.10.22",
    updateAvailable: null,
    settings: { memoryBytes: 0, nanoCpus: 0 },
    access: [],
    viewers: [],
    tokens: 0,
    stats: null,
    ...c,
  };
}

export const desktops: Desktop[] = [
  desktop({
    name: "alice",
    startedAt: iso(3 * DAY),
    imageVersion: "2026.10.15",
    updateAvailable: "2026.10.22",
    settings: { memoryBytes: 2 * GB, nanoCpus: 2e9 },
    access: ["anna"],
    viewers: ["zef", "anna"],
    tokens: 2,
    stats: { cpuPercent: 34, memBytes: 1.4 * GB, memLimit: 2 * GB },
  }),
  desktop({ name: "research", startedAt: iso(5 * HOUR), access: ["anna", "sam"], viewers: ["anna"], stats: { cpuPercent: 8, memBytes: 0.9 * GB, memLimit: 16 * GB } }),
  desktop({
    name: "build-bot",
    startedAt: iso(12 * 60 * 1000),
    settings: { memoryBytes: 4 * GB, nanoCpus: 4e9 },
    stats: { cpuPercent: 91, memBytes: 3.1 * GB, memLimit: 4 * GB },
  }),
  desktop({
    name: "notes",
    startedAt: iso(2 * DAY),
    imageVersion: "2026.10.15",
    updateAvailable: "2026.10.22",
    access: ["anna"],
    stats: { cpuPercent: 2, memBytes: 0.7 * GB, memLimit: 16 * GB },
  }),
  desktop({ name: "data-lab", state: "creating", startedAt: "", imageVersion: "" }),
  desktop({ name: "scratch", state: "stopped", startedAt: "", imageVersion: "2026.10.15", updateAvailable: "2026.10.22" }),
];

export const accounts: (Account & { password: string })[] = [
  { username: "zef", admin: true, desktops: [], created: iso(30 * DAY), lastLogin: iso(0), password: "zef" },
  { username: "anna", admin: false, desktops: ["alice", "research", "notes"], created: iso(20 * DAY), lastLogin: iso(60 * 1000), password: "anna" },
  { username: "sam", admin: false, desktops: ["research"], created: iso(10 * DAY), lastLogin: iso(3 * DAY), password: "sam" },
  { username: "ci", admin: false, desktops: [], created: iso(2 * DAY), lastLogin: null, password: "ci" },
];

function customImage(slug: string, title: string, description: string, over: Partial<CatalogImage>): CatalogImage {
  return {
    ref: `deskyard-custom/${slug}:latest`,
    title,
    version: "2026-10-20 09:14",
    description,
    desktops: [],
    behind: [],
    checked: null,
    custom: true,
    secrets: false,
    baseUpdated: false,
    recipe: slug,
    ...over,
  };
}

export const images: CatalogImage[] = [
  {
    ref: LATEST,
    title: "Debian XFCE",
    version: "2026.10.22",
    description: "Debian trixie with XFCE, Chromium, Flatpak, mise and uv",
    desktops: [],
    behind: [],
    checked: iso(2 * HOUR),
    ...PLAIN,
  },
  {
    ref: BETA,
    title: "Debian XFCE, beta",
    version: "2026.10.22-beta",
    description: "Next week's image, for trying changes",
    desktops: [],
    behind: [],
    checked: iso(2 * HOUR),
    ...PLAIN,
  },
  customImage("python-lab", "Python lab", "Python 3.13, uv and JupyterLab", {}),
  customImage("work-signed-in", "Work, signed in", "Chromium and the work apps, signed in to everything", { secrets: true }),
  customImage("video-tools", "Video tools", "Kdenlive and ffmpeg", { baseUpdated: true }),
];

export const recipes: Recipe[] = [
  { slug: "python-lab", name: "Python lab", base: LATEST, mise: ["python@3.13", "uv"], commands: "uv tool install jupyterlab" },
  { slug: "work-signed-in", name: "Work, signed in", base: LATEST, homeFrom: "image", secrets: true, flatpaks: [{ id: "org.mozilla.firefox" }] },
  { slug: "video-tools", name: "Video tools", base: LATEST, flatpaks: [{ id: "org.kde.kdenlive" }], commands: "apt-get update && apt-get install -y ffmpeg" },
];

export const tokens: Record<string, Token[]> = {
  alice: [
    { id: "7f3kq2", name: "claude on zef's laptop", createdBy: "zef", created: iso(10 * DAY), expires: null, lastUsed: iso(2 * 60 * 1000), expired: false },
    { id: "m2q9x8", name: "nightly CI", createdBy: "zef", created: iso(4 * DAY), expires: new Date(now + 26 * DAY).toISOString(), lastUsed: iso(5 * HOUR), expired: false },
    { id: "a81c4d", name: "demo for sam", createdBy: "anna", created: iso(21 * DAY), expires: iso(14 * DAY), lastUsed: iso(14 * DAY), expired: true },
  ],
};

export const processRows: [number, string, number, number, string, string][] = [
  [412, "user", 18.2, 626688, "02:14:07", "/usr/lib/chromium/chromium --type=renderer --enable-features=… https://github.com/…"],
  [3301, "user", 9.6, 253952, "41:02", "node /workspace/.local/share/claude/versions/2.4.1/cli.js"],
  [118, "user", 3.1, 72704, "3-01:12:44", "Xkasmvnc :1 -geometry 1280x800 -depth 24 -AlwaysShared -interface 127.0.0.1"],
  [380, "user", 1.4, 190464, "02:14:09", "/usr/lib/chromium/chromium --remote-debugging-port=9220 --user-data-dir=…"],
  [2915, "user", 0.9, 135168, "01:02:33", "/app/bin/silverbullet --hostname 127.0.0.1 /workspace/Notes"],
  [205, "user", 0.4, 38912, "3-01:12:40", "xfce4-panel --display :1.0 --sm-client-id 2f1c…"],
  [3290, "user", 0.2, 9216, "41:00", "-bash"],
  [96, "caddy", 0.1, 31744, "3-01:12:45", "caddy run --config /etc/caddy/Caddyfile --adapter caddyfile"],
  [1, "root", 0.0, 24576, "3-01:12:46", "/usr/bin/python3 /usr/bin/supervisord -c /etc/supervisor/supervisord.conf"],
];

/** Which desktops use which image, recomputed from the desktops. */
export function refreshImages(): void {
  for (const img of images) {
    const on = desktops.filter((c) => c.image === img.ref);
    img.desktops = on.map((c) => c.name);
    img.behind = on.filter((c) => c.updateAvailable).map((c) => c.name);
  }
}
