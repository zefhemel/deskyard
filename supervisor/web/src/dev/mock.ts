// A fake supervisor API for `npm run dev` (Vite dev-server middleware only:
// `apply: "serve"` keeps it out of `vite build`). It follows supervisor/API.md
// closely enough to click through every page. Sign in as zef (admin) or anna
// (regular) with any password; "wrong" fails.
import { createHash } from "node:crypto";
import { readFileSync, existsSync } from "node:fs";
import type { IncomingMessage, ServerResponse } from "node:http";
import type { Duplex } from "node:stream";
import { fileURLToPath } from "node:url";
import type { Plugin } from "vite";
import type { CreatedToken, Environment, Job } from "../api.ts";
import { accounts, activityOf, conflicts, environments, FOLDER, images, processRows, refreshImages, tokens, volumes } from "./mock-data.ts";


const GB = 1024 ** 3;
const imgDir = fileURLToPath(new URL("../../../../docs/superpowers/specs/2026-10-21-supervisor-design/img/", import.meta.url));

let session: string | null = "zef";
let failures = 0;
const streams = new Set<ServerResponse>();
const jobs = new Map<string, { steps: Job[]; after: () => void }>();

function json(res: ServerResponse, status: number, body?: unknown): void {
  res.statusCode = status;
  if (body === undefined) {
    res.end();
    return;
  }
  res.setHeader("Content-Type", "application/json");
  res.end(JSON.stringify(body));
}

async function readBody(req: IncomingMessage): Promise<Record<string, unknown>> {
  let data = "";
  for await (const chunk of req) data += String(chunk);
  return data ? (JSON.parse(data) as Record<string, unknown>) : {};
}

function sse(res: ServerResponse): void {
  res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", Connection: "keep-alive" });
  res.write(": hello\n\n");
}

function broadcast(event: string, data: unknown): void {
  for (const s of streams) s.write(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
  for (const send of eventSockets) send(event, data);
}

function me() {
  return accounts.find((a) => a.username === session);
}

function visible(): Environment[] {
  const m = me();
  if (!m) return [];
  const list = m.admin ? environments : environments.filter((c) => c.access.includes(m.username));
  return list.map((c) => (m.admin ? c : { ...c, access: [] })).sort((a, b) => a.name.localeCompare(b.name));
}

function changed(c: Environment): void {
  broadcast("environment", c);
}

function setState(c: Environment, state: Environment["state"], later?: Environment["state"]): void {
  c.state = state;
  if (state === "running" || state === "stopped") c.power = state;
  if (state === "running") {
    c.appliedRevision = c.revision;
    c.error = "";
    c.startedAt = new Date().toISOString();
    c.stats = { cpuPercent: 5, memBytes: 0.6 * GB, memLimit: c.settings.memoryBytes || 16 * GB };
  }
  if (state === "stopped") {
    c.stats = null;
    c.viewers = [];
    c.mics = [];
    // Sessions end with the container.
    for (const t of c.terminals) endAttachments(c.name, t.name);
    c.terminals = [];
  }
  changed(c);
  if (later) setTimeout(() => setState(c, later), 1500);
}

let jobSeq = 1;
function job(messages: string[], after: () => void, environment?: string): { job: string } {
  const id = `job${jobSeq++}`;
  const steps: Job[] = messages.map((m, i) => ({ status: "running", message: m, progress: (i + 1) / (messages.length + 1), environment }));
  steps.push({ status: "done", message: "Done", progress: 1, environment });
  jobs.set(id, { steps, after });
  // The job finishes on its own schedule, whether or not anyone follows it.
  setTimeout(after, steps.length * 700);
  return { job: id };
}

function streamJob(res: ServerResponse, id: string): void {
  const j = jobs.get(id);
  if (!j) return json(res, 404, { error: "no such job" });
  sse(res);
  let i = 0;
  const t = setInterval(() => {
    const step = j.steps[i++];
    if (!step) return;
    if (step.status === "done") {
      clearInterval(t);
      res.write(`data: ${JSON.stringify(step)}\n\n`);
      res.end();
      return;
    }
    res.write(`data: ${JSON.stringify(step)}\n\n`);
  }, 700);
  res.on("close", () => clearInterval(t));
}

function host() {
  const list = visible();
  const running = list.filter((c) => c.state === "running");
  return { cpus: 8, memBytes: 16 * GB, running: running.length, total: list.length, memUsed: running.reduce((s, c) => s + (c.stats?.memBytes ?? 0), 0), memoryReclaim: { enabled: true } };
}

function volumeJSON(v: (typeof volumes)[number]) {
  const users = environments
    .flatMap((c) => c.settings.volumes.filter((g) => g.name === v.name).map((g) => ({ name: c.name, readOnly: g.readOnly })))
    .sort((a, b) => a.name.localeCompare(b.name));
  return { ...v, folder: `${FOLDER}/volume/${v.name}`, environments: users };
}

/** The 400 for a grant of a volume that doesn't exist, as the supervisor answers. */
function unknownVolume(grants: unknown): string | null {
  if (!Array.isArray(grants)) return null;
  const missing = (grants as { name: string }[]).find((g) => !volumes.some((v) => v.name === g.name));
  return missing ? `no volume ${missing.name}` : null;
}

// Stats every 5 s and a visitor now and then, so the pages move. Started by
// the dev server only: timers at import time would keep `vite build` alive.
function startTimers(): void {
setInterval(() => {
  const t = new Date().toISOString();
  for (const c of environments) {
    if (c.state !== "running" || !c.stats) continue;
    const s = c.stats;
    s.cpuPercent = Math.max(0.5, Math.min(99, s.cpuPercent * 0.7 + (s.cpuPercent + (Math.random() - 0.5) * 30) * 0.3));
    s.memBytes = Math.max(0.2 * GB, Math.min(s.memLimit, s.memBytes + (Math.random() - 0.5) * 0.05 * GB));
    broadcast("stats", { name: c.name, ...s, t });
  }
}, 5000);
setInterval(() => {
  const alice = environments.find((c) => c.name === "alice");
  if (!alice || alice.state !== "running") return;
  alice.viewers = alice.viewers.includes("sam") ? alice.viewers.filter((v) => v !== "sam") : [...alice.viewers, "sam"];
  broadcast("viewers", { name: "alice", viewers: alice.viewers, mics: alice.mics, forwards: alice.forwards, agents: alice.agents });
}, 20000);
// The agents' summaries move on, as they would from the supervisor (once a second at most).
setInterval(() => {
  for (const c of environments) if (c.agents.length) broadcast("activity", activityOf(c));
}, 5000);
}

function history(c: Environment) {
  const out = [];
  let cpu = c.stats?.cpuPercent ?? 10;
  let mem = c.stats?.memBytes ?? GB;
  const now = Date.now();
  for (let i = 179; i >= 0; i--) {
    cpu = Math.max(1, Math.min(99, cpu * 0.8 + (20 + (Math.random() - 0.5) * 60) * 0.2));
    mem = Math.max(0.3 * GB, mem + (Math.random() - 0.5) * 0.03 * GB);
    out.push({ t: new Date(now - i * 5000).toISOString(), cpuPercent: cpu, memBytes: mem });
  }
  return { history: out, memLimit: c.stats?.memLimit ?? 16 * GB };
}

/**
 * A stand-in for Selkies' client with our additions: window.deskyardScreen
 * (see desktop/selkies.ts), a picture of the desktop and a log of messages.
 */
function fakeScreen(name: string): string {
  return `<!doctype html><html><body style="margin:0;background:#0f151d;height:100vh;overflow:hidden">
<canvas id="videoCanvas" width="1280" height="800" style="display:none"></canvas>
<img id="videoStream" src="/api/environments/${name}/screen.png?full=1" style="position:absolute;inset:0;width:100%;height:100%;object-fit:fill" alt="">
<div id="log" style="position:fixed;left:12px;bottom:12px;font:12px monospace;color:#9fb0c3"></div>
<script>
const log = (t) => (document.getElementById("log").textContent = t);
let state = "connecting";
const listeners = new Set();
const displayListeners = new Set();
let display = { owner: null, width: 0, height: 0, settings: null };
window.deskyardScreen = {
  tab: "devtab",
  post(msg) { log(JSON.stringify(msg)); },
  on(type, fn) {
    if (type === "deskyard.state") { listeners.add(fn); return () => listeners.delete(fn); }
    if (type === "deskyard.display") { displayListeners.add(fn); return () => displayListeners.delete(fn); }
    const l = (e) => e.data && e.data.type === type && fn(e.data);
    addEventListener("message", l);
    return () => removeEventListener("message", l);
  },
  state: () => state,
  display: () => display,
  media: () => media,
  setSound(on) { setMedia({ sound: on, playing: on }); },
  resumeSound() {},
  allowCapture(kind, on) { allowed[kind] = on; },
  setMic() {},
  setOutput() {},
  watchSound() { return () => {}; },
  micLevel: () => (media.mic ? 0.2 + Math.random() * 0.4 : 0),
  video: () => document.getElementById("videoStream"),
};
// Sound and mic: pipelineControl turns the mic on where allowed.
let media = { sound: false, mic: false, playing: false, soundAt: 0 };
const allowed = { microphone: false };
const mediaListeners = new Set();
function setMedia(m) { media = { ...media, ...m }; for (const fn of mediaListeners) fn(media); }
const on = window.deskyardScreen.on;
window.deskyardScreen.on = (type, fn) => {
  if (type === "deskyard.media") { mediaListeners.add(fn); return () => mediaListeners.delete(fn); }
  if (type === "deskyard.alert") return () => {};
  return on(type, fn);
};
const post = window.deskyardScreen.post;
window.deskyardScreen.post = (msg) => {
  post(msg);
  if (msg.type === "pipelineControl" && msg.pipeline in allowed) {
    setTimeout(() => setMedia({ mic: msg.enabled && allowed[msg.pipeline] }), 300);
  }
};
setTimeout(() => {
  state = "connected";
  for (const fn of listeners) fn(state);
  display = { owner: true, width: 1280, height: 800, settings: null };
  for (const fn of displayListeners) fn(display);
}, 600);
setTimeout(() => postMessage({ type: "clipboardContentUpdate", text: "https://github.com/zefhemel/deskyard/pull/42" }, location.origin), 1500);
</script></body></html>`;
}

async function handle(req: IncomingMessage, res: ServerResponse, next: () => void): Promise<void> {
  const url = new URL(req.url ?? "/", "http://localhost");
  const path = url.pathname;
  const method = req.method ?? "GET";

  const screen = /^\/([a-z][a-z0-9-]*)\/screen\/$/.exec(path);
  if (screen?.[1]) {
    res.setHeader("Content-Type", "text/html");
    res.end(fakeScreen(screen[1]));
    return;
  }
  if (!path.startsWith("/api/")) return next();

  if (path === "/api/login" && method === "POST") {
    const b = await readBody(req);
    const a = accounts.find((x) => x.username === b.username);
    if (failures >= 5) {
      failures = 0;
      return json(res, 429, { error: "Too many attempts" });
    }
    if (!a || b.password === "wrong" || !b.password) {
      failures++;
      return json(res, 401, { error: "Wrong username or password" });
    }
    session = a.username;
    return json(res, 200, { username: a.username, admin: a.admin });
  }
  const m = me();
  if (!m) return json(res, 401, { error: "Signed out" });

  if (path === "/api/logout") {
    session = null;
    return json(res, 204);
  }
  if (path === "/api/me" && method === "GET") return json(res, 200, { username: m.username, admin: m.admin });
  if (path === "/api/me/password") return json(res, 204);
  if (path === "/api/host") return json(res, 200, host());
  if (path === "/api/events") {
    sse(res);
    streams.add(res);
    res.on("close", () => streams.delete(res));
    return;
  }

  const jobMatch = /^\/api\/jobs\/([^/]+)$/.exec(path);
  if (jobMatch?.[1]) return streamJob(res, jobMatch[1]);

  if (path === "/api/validate-name") {
    const name = url.searchParams.get("name") ?? "";
    if (environments.some((c) => c.name === name)) return json(res, 200, { ok: false, error: "an environment with that name already exists", folderExists: false });
    if (["api", "login", "logout", "assets", "static", "healthz", "environments", "volumes"].includes(name)) return json(res, 200, { ok: false, error: `${name} is reserved.`, folderExists: false });
    return json(res, 200, { ok: true, folderExists: name === "old-project", folder: `${FOLDER}/home/${name}` });
  }
  if (path === "/api/environments" && method === "GET") return json(res, 200, { environments: visible(), conflicts: m.admin ? conflicts : [] });
  if (path === "/api/environments" && method === "POST") {
    const b = await readBody(req);
    const bad = unknownVolume(b.volumes);
    if (bad) return json(res, 400, { error: bad });
    const name = String(b.name);
    const img = images.find((i) => i.ref === b.image) ?? images[0]!;
    const c: Environment = {
      name, state: "creating", power: "running", revision: 1, appliedRevision: 0, error: "", conflict: "",
      startedAt: "", created: new Date().toISOString(), job: null, folder: `${FOLDER}/home/${name}`, image: img.ref, imageTitle: img.title,
      memoryReclaim: true, imageVersion: img.version, updateAvailable: null, settings: { dockerAccess: b.dockerAccess === true, memoryBytes: Number(b.memoryBytes), nanoCpus: Number(b.nanoCpus), vpnConnectionId: null, lanExceptions: [], mounts: [], volumes: (b.volumes as Environment["settings"]["volumes"]) ?? [] },
      vpn: {state:"disabled",changedAt:"0001-01-01T00:00:00Z",exitIpObservedAt:"0001-01-01T00:00:00Z",desiredRevision:0,appliedRevision:0,idle:false},
      access: (b.access as string[]) ?? [], viewers: [], mics: [], terminal: true, terminals: [], forwards: [], agents: [], tokens: 0, stats: null,
    };
    environments.push(c);
    changed(c);
    return json(res, 202, job(["Pulling image, 120 of 820 MB", "Pulling image, 540 of 820 MB", "Creating the container", "Starting"], () => setState(c, "running"), name));
  }
  if (path.startsWith("/api/volumes") && !m.admin) return json(res, 403, { error: "admins only" });
  if (path === "/api/volumes" && method === "GET") {
    return json(res, 200, { volumes: [...volumes].sort((a, b) => a.name.localeCompare(b.name)).map(volumeJSON) });
  }
  if (path === "/api/volumes" && method === "POST") {
    const name = String((await readBody(req)).name ?? "");
    if (!/^[a-z][a-z0-9-]{0,30}[a-z0-9]$/.test(name)) return json(res, 400, { error: "Use 2–32 lowercase letters, digits and dashes, starting with a letter." });
    if (["api", "login", "logout", "assets", "static", "healthz", "environments", "volumes"].includes(name)) return json(res, 400, { error: `${name} is reserved` });
    if (volumes.some((v) => v.name === name)) return json(res, 409, { error: "a volume with that name already exists" });
    const v = { name, created: new Date().toISOString(), kind: "local" as const };
    volumes.push(v);
    // "old-photos" stands in for a folder kept by an earlier delete.
    return json(res, 201, { volume: volumeJSON(v), adopted: name === "old-photos" });
  }
  const usage = /^\/api\/volumes\/([^/]+)\/usage$/.exec(path);
  if (usage?.[1] && method === "GET") {
    if (!volumes.some((x) => x.name === usage[1])) return json(res, 404, { error: "no such volume" });
    return json(res, 200, { bytes: usage[1] === "photos" ? 18.6 * 1024 ** 3 : 0, complete: true, diskFree: 212 * 1024 ** 3, diskSize: 460 * 1024 ** 3 });
  }
  const vol = /^\/api\/volumes\/([^/]+)$/.exec(path);
  if (vol?.[1] && method === "DELETE") {
    const v = volumes.find((x) => x.name === vol[1]);
    if (!v) return json(res, 404, { error: "no such volume" });
    const users = volumeJSON(v).environments;
    if (users.length) {
      return json(res, 409, { error: `${v.name} is used by ${users.map((u) => u.name).join(", ")}`, environments: users.map((u) => ({ id: u.name, name: u.name })) });
    }
    volumes.splice(volumes.indexOf(v), 1);
    return json(res, 204);
  }
  if (path === "/api/accounts" && method === "GET") {
    return json(res, 200, m.admin ? accounts.map(({ password: _, ...a }) => a) : accounts.map((a) => ({ username: a.username })));
  }
  if (path === "/api/accounts" && method === "POST") {
    const b = await readBody(req);
    const a = { username: String(b.username), password: String(b.password), admin: Boolean(b.admin), environments: (b.environments as string[]) ?? [], created: new Date().toISOString(), lastLogin: null };
    accounts.push(a);
    return json(res, 201, a);
  }
  const acc = /^\/api\/accounts\/([^/]+)$/.exec(path);
  if (acc?.[1]) {
    const a = accounts.find((x) => x.username === acc[1]);
    if (!a) return json(res, 404, { error: "No such account" });
    if (method === "DELETE") {
      if (a.admin && accounts.filter((x) => x.admin).length === 1) return json(res, 409, { error: "That's the last admin." });
      accounts.splice(accounts.indexOf(a), 1);
      return json(res, 204);
    }
    const b = await readBody(req);
    if (b.admin === false && a.admin && accounts.filter((x) => x.admin).length === 1) return json(res, 409, { error: "That would leave no admin." });
    if (typeof b.admin === "boolean") a.admin = b.admin;
    if (Array.isArray(b.environments)) a.environments = b.environments as string[];
    return json(res, 200, a);
  }
  if (path === "/api/images" && method === "GET") {
    refreshImages();
    // Downloaded images have a size and build date, as the engine reports.
    return json(res, 200, images.map((i) => (i.installed ? { size: 1.9 * 1024 ** 3, built: "2026-10-22T06:12:00Z", ...i } : i)));
  }
  if (path === "/api/images" && method === "POST") {
    const b = await readBody(req);
    if (!String(b.ref).includes(":")) return json(res, 400, { error: "Give a reference with a tag." });
    return json(res, 202, job(["Pulling " + String(b.ref), "Checking the image contract"], () => {
      images.push({ installed: true, official: false, ref: String(b.ref), title: "Added image", version: "1.0", description: "Added in the mock", environments: [], behind: [], checked: new Date().toISOString() });
    }));
  }
  if (path === "/api/images/install" && method === "POST") {
    const b = await readBody(req);
    const img = images.find((i) => i.ref === b.ref);
    if (!img) return json(res, 400, { error: "Choose an available image." });
    return json(res, 202, job(["Downloading " + img.ref, "Checking the image contract"], () => {
      img.installed = true;
      img.version = "2026.10.23";
      img.checked = new Date().toISOString();
    }));
  }
  if (path === "/api/images" && method === "DELETE") {
    const i = images.findIndex((x) => x.ref === url.searchParams.get("ref"));
    if (i >= 0) images.splice(i, 1);
    return json(res, 204);
  }
  if (path === "/api/images/check") return json(res, 202, job(["Pulling ghcr.io/zefhemel/deskyard-debian-xfce:latest", "Pulling ghcr.io/zefhemel/deskyard-debian-xfce:beta"], () => images.filter((i) => i.installed).forEach((i) => (i.checked = new Date().toISOString()))));
  if (path === "/api/images/upgrade") {
    const b = await readBody(req);
    return json(res, 202, job(["Upgrading", "Starting"], () => {
      for (const c of environments) if (c.image === b.ref && c.updateAvailable) {
        c.imageVersion = c.updateAvailable;
        c.updateAvailable = null;
        changed(c);
      }
    }));
  }

  const cm = /^\/api\/environments\/([^/]+)(?:\/(.+))?$/.exec(path);
  const c = cm?.[1] ? visible().find((x) => x.name === cm[1]) && environments.find((x) => x.name === cm[1]) : undefined;
  if (!cm || !c) return json(res, 404, { error: "no such environment" });
  const sub = cm[2] ?? "";

  if (sub === "terminal/sessions" && method === "GET") {
    if (c.state !== "running" || !c.terminal) return json(res, 409, { error: "Environment terminal unavailable" });
    return json(res, 200, { sessions: c.terminals });
  }
  if (sub === "terminal/sessions" && method === "POST") {
    if (c.state !== "running" || !c.terminal) return json(res, 409, { error: "Environment terminal unavailable" });
    let n = 1;
    while (c.terminals.some((t) => t.name === `terminal-${n}`)) n++;
    const name = `terminal-${n}`;
    c.terminals = [...c.terminals, { name, clients: 0, command: "bash" }].sort((a, b) => a.name.localeCompare(b.name));
    changed(c);
    return json(res, 201, { name });
  }
  const ts = /^terminal\/sessions\/(.+)$/.exec(sub);
  if (ts && method === "DELETE") {
    const name = decodeURIComponent(ts[1]!);
    if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(name)) return json(res, 400, { error: "invalid session name" });
    if (c.state !== "running" || !c.terminal) return json(res, 409, { error: "Environment terminal unavailable" });
    if (!c.terminals.some((t) => t.name === name)) return json(res, 404, { error: "no such session" });
    endAttachments(c.name, name);
    c.terminals = c.terminals.filter((t) => t.name !== name);
    changed(c);
    return json(res, 204);
  }
  if (sub === "" && method === "GET") return json(res, 200, visible().find((x) => x.name === c.name));
  if (sub === "" && method === "DELETE") {
    environments.splice(environments.indexOf(c), 1);
    broadcast("removed", { name: c.name });
    return json(res, 204);
  }
  if (sub === "" && method === "PATCH") {
    const b = await readBody(req);
    const current = c.settings.volumes;
    const bad = unknownVolume(Array.isArray(b.volumes) ? (b.volumes as { name: string }[]).filter((g) => !current.some((x) => x.name === g.name)) : null);
    if (bad) return json(res, 400, { error: bad });
    c.revision++;
    changed(c);
    return json(res, 202, job(["Recreating the container", "Starting"], () => {
      if (c.state === "running") c.appliedRevision = c.revision;
      if (typeof b.memoryBytes === "number") c.settings.memoryBytes = b.memoryBytes;
      if (typeof b.nanoCpus === "number") c.settings.nanoCpus = b.nanoCpus;
      if (typeof b.dockerAccess === "boolean") c.settings.dockerAccess = b.dockerAccess;
      if (Array.isArray(b.mounts)) c.settings.mounts = b.mounts as Environment["settings"]["mounts"];
      if (Array.isArray(b.volumes)) c.settings.volumes = b.volumes as Environment["settings"]["volumes"];
      if (typeof b.image === "string") c.image = b.image;
      changed(c);
    }, c.name));
  }
  if (sub === "start") return setState(c, "starting", "running"), json(res, 204);
  if (sub === "memory-policy" && method === "PUT") {
    c.memoryReclaim = (await readBody(req)).memoryReclaim === true;
    changed(c);
    return json(res, 204);
  }
  if (sub === "reclaim-memory") {
    if (c.state !== "running") return json(res, 409, { error: "The environment is stopped" });
    const beforeBytes = c.stats?.memBytes ?? 512 * 1024 * 1024;
    const afterBytes = Math.max(0, beforeBytes - 128 * 1024 * 1024);
    if (c.stats) c.stats.memBytes = afterBytes;
    return json(res, 200, { requestedBytes: 128 * 1024 * 1024, beforeBytes, afterBytes,
      swapBeforeBytes: 0, swapAfterBytes: 96 * 1024 * 1024, partial: false });
  }
  if (sub === "stop") return setState(c, "stopped"), json(res, 204);
  if (sub === "restart") return setState(c, "starting", "running"), json(res, 204);
  if (sub === "access") {
    c.access = ((await readBody(req)).usernames as string[]) ?? [];
    changed(c);
    return json(res, 204);
  }
  if (sub === "upgrade") {
    c.revision++;
    setState(c, "upgrading");
    return json(res, 202, job(["Recreating the container on 2026.10.22", "Starting"], () => {
      c.imageVersion = c.updateAvailable ?? c.imageVersion;
      c.updateAvailable = null;
      setState(c, "running");
    }, c.name));
  }
  if (sub === "keys") return json(res, 204);
  if (sub === "mic" && method === "POST") {
    const on = (await readBody(req)).on === true;
    const me = "zef";
    c.mics = on ? [...new Set([...c.mics, me])] : c.mics.filter((m) => m !== me);
    if (on && !c.viewers.includes(me)) c.viewers = [...c.viewers, me];
    changed(c);
    return json(res, 204);
  }
  if (sub === "activity") return json(res, 200, activityOf(c));
  if (sub === "stats") return json(res, 200, history(c));
  if (sub === "folder") return json(res, 200, { path: c.folder, bytes: 3.2 * GB, complete: true });
  if (sub === "processes") {
    return json(res, 200, {
      titles: ["PID", "USER", "%CPU", "RSS", "ELAPSED", "COMMAND"],
      rows: processRows.map((r) => [r[0], r[1], Math.max(0, r[2] + (Math.random() - 0.5) * r[2] * 0.4), r[3], r[4], r[5]]),
    });
  }
  if (sub === "screen.png") {
    if (c.state !== "running") return json(res, 409, { error: `${c.name} is stopped` });
    const file = existsSync(`${imgDir}${c.name}.png`) ? `${imgDir}${c.name}.png` : `${imgDir}alice.png`;
    res.setHeader("Content-Type", "image/png");
    res.end(readFileSync(file));
    return;
  }
  if (sub === "tokens" && method === "GET") return json(res, 200, tokens[c.name] ?? []);
  if (sub === "tokens" && method === "POST") {
    const b = await readBody(req);
    const id = Math.random().toString(36).slice(2, 8);
    const secret = "Jx3vT9pLr7KzA1mYc5HdE0sWb6NfQ8uRg2iVo4Ce";
    const t: CreatedToken = {
      id, name: String(b.name), createdBy: m.username, created: new Date().toISOString(), expires: (b.expires as string | null) ?? null,
      lastUsed: null, expired: false, token: `dyt_${id}_${secret}`,
      remoteAdd: `deskyard remote add ${c.name} https://deskyard.example.com/${c.name} --token dyt_${id}_${secret}`,
    };
    const { token: _t, remoteAdd: _r, ...listed } = t;
    (tokens[c.name] ??= []).push(listed);
    c.tokens++;
    changed(c);
    return json(res, 201, t);
  }
  const tok = /^tokens\/(.+)$/.exec(sub);
  if (tok && method === "DELETE") {
    tokens[c.name] = (tokens[c.name] ?? []).filter((t) => t.id !== tok[1]);
    c.tokens = tokens[c.name]!.filter((t) => !t.expired).length;
    changed(c);
    return json(res, 204);
  }
  return json(res, 404, { error: `mock: no route for ${method} ${path}` });
}

// A pretend shared terminal over a hand-rolled WebSocket (no dependency):
// join-only unless ?create=1, {"ended":true} for a missing or ended session,
// and an echoing prompt. Enough to click through the environment page's tabs.
const attachments = new Map<string, Set<(ended: boolean) => void>>();

function endAttachments(env: string, session: string): void {
  for (const end of attachments.get(`${env}/${session}`) ?? []) end(true);
}

function frame(opcode: number, payload: Buffer): Buffer {
  const n = payload.length;
  const head = n < 126 ? Buffer.from([0x80 | opcode, n]) : n < 65536 ? Buffer.from([0x80 | opcode, 126, n >> 8, n & 255]) : Buffer.concat([Buffer.from([0x80 | opcode, 127, 0, 0, 0, 0]), Buffer.from([(n >>> 24) & 255, (n >> 16) & 255, (n >> 8) & 255, n & 255])]);
  return Buffer.concat([head, payload]);
}

function accept(socket: Duplex, key: string): void {
  const hash = createHash("sha1").update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`).digest("base64");
  socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${hash}\r\n\r\n`);
}

// The UI's live events (/api/events) as the supervisor sends them over a
// WebSocket: {"type","data"} text frames. (Chromium queues a page's other
// WebSockets behind one that never finishes its handshake.)
const eventSockets = new Set<(type: string, data: unknown) => void>();
function eventSocket(req: IncomingMessage, socket: Duplex): void {
  const key = req.headers["sec-websocket-key"];
  if (!key) return void socket.destroy();
  accept(socket, key);
  const send = (type: string, data: unknown) => socket.write(frame(1, Buffer.from(JSON.stringify({ type, data }))));
  eventSockets.add(send);
  const drop = () => eventSockets.delete(send);
  socket.on("data", (chunk: Buffer) => {
    if ((chunk[0]! & 15) === 8) socket.end(frame(8, Buffer.alloc(0)));
  });
  socket.on("close", drop);
  socket.on("error", drop);
}

function mockTerminal(req: IncomingMessage, socket: Duplex): void {
  const url = new URL(req.url ?? "/", "http://mock");
  const m = /^\/api\/environments\/([^/]+)\/terminal$/.exec(url.pathname);
  if (url.pathname === "/api/events") return eventSocket(req, socket);
  if (!m) return; // Vite's own sockets
  const key = req.headers["sec-websocket-key"];
  const c = environments.find((x) => x.name === m[1]);
  const session = url.searchParams.get("session") ?? "";
  if (!key || !c) return void socket.destroy();
  accept(socket, key);
  const text = (v: unknown) => socket.write(frame(1, Buffer.from(JSON.stringify(v))));
  const out = (s: string) => socket.write(frame(2, Buffer.from(s)));
  const close = (code: number) => {
    socket.end(frame(8, Buffer.from([code >> 8, code & 255])));
  };
  const id = `${c.name}/${session}`;
  let attached = false;
  const detach = () => {
    if (!attached) return;
    attached = false;
    attachments.get(id)?.delete(end);
    const t = c.terminals.find((x) => x.name === session);
    if (t) {
      t.clients = Math.max(0, t.clients - 1);
      changed(c);
    }
  };
  const end = (ended: boolean) => {
    detach();
    if (ended) text({ ended: true });
    close(1000);
  };
  let buf = Buffer.alloc(0);
  socket.on("data", (chunk: Buffer) => {
    buf = Buffer.concat([buf, chunk]);
    for (;;) {
      if (buf.length < 2) return;
      const opcode = buf[0]! & 15;
      let len = buf[1]! & 127;
      let at = 2;
      if (len === 126) [len, at] = [buf.readUInt16BE(2), 4];
      else if (len === 127) [len, at] = [Number(buf.readBigUInt64BE(2)), 10];
      const masked = (buf[1]! & 128) !== 0;
      const mask = masked ? buf.subarray(at, at + 4) : null;
      if (masked) at += 4;
      if (buf.length < at + len) return;
      const data = Buffer.from(buf.subarray(at, at + len));
      if (mask) for (let i = 0; i < data.length; i++) data[i]! ^= mask[i % 4]!;
      buf = buf.subarray(at + len);
      if (opcode === 8) return end(false);
      if (opcode === 9) socket.write(frame(10, data));
      if (opcode === 1 && !attached) {
        // The first message is the size: attach now.
        let t = c.terminals.find((x) => x.name === session);
        if (!t && url.searchParams.get("create") === "1" && c.state === "running") {
          t = { name: session, clients: 0, command: "bash" };
          c.terminals = [...c.terminals, t].sort((a, b) => a.name.localeCompare(b.name));
        }
        if (!t || c.state !== "running") return end(true);
        attached = true;
        t.clients++;
        (attachments.get(id) ?? attachments.set(id, new Set()).get(id)!).add(end);
        changed(c);
        text({ ready: true, generation: "mock" });
        out(`\x1b[2mMock shell for ${c.name}, session ${session}. Typing echoes; there is no real shell here.\x1b[0m\r\n$ `);
      }
      if (opcode === 2 && attached) out(data.toString().replace(/\r/g, "\r\n$ "));
    }
  });
  socket.on("close", detach);
  socket.on("error", detach);
}

export function mockApi(): Plugin {
  return {
    name: "supervisor-mock-api",
    apply: "serve",
    configureServer(server) {
      if (process.env.DESKYARD_API) return; // proxying to a real supervisor instead
      startTimers();
      server.httpServer?.on("upgrade", mockTerminal);
      server.middlewares.use((req, res, next) => {
        handle(req, res, next).catch((e: unknown) => json(res, 500, { error: String(e) }));
      });
    },
  };
}
