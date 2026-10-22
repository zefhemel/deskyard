// A fake supervisor API for `npm run dev` (Vite dev-server middleware only:
// `apply: "serve"` keeps it out of `vite build`). It follows supervisor/API.md
// closely enough to click through every page. Sign in as zef (admin) or anna
// (regular) with any password; "wrong" fails.
import { readFileSync, existsSync } from "node:fs";
import type { IncomingMessage, ServerResponse } from "node:http";
import { fileURLToPath } from "node:url";
import type { Plugin } from "vite";
import type { Desktop, CreatedToken, Job } from "../api.ts";
import { accounts, activityOf, desktops, FOLDER, images, processRows, refreshImages, tokens } from "./mock-data.ts";

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
}

function me() {
  return accounts.find((a) => a.username === session);
}

function visible(): Desktop[] {
  const m = me();
  if (!m) return [];
  const list = m.admin ? desktops : desktops.filter((c) => c.access.includes(m.username));
  return list.map((c) => (m.admin ? c : { ...c, access: [] })).sort((a, b) => a.name.localeCompare(b.name));
}

function changed(c: Desktop): void {
  broadcast("desktop", c);
}

function setState(c: Desktop, state: Desktop["state"], later?: Desktop["state"]): void {
  c.state = state;
  if (state === "running") {
    c.startedAt = new Date().toISOString();
    c.stats = { cpuPercent: 5, memBytes: 0.6 * GB, memLimit: c.settings.memoryBytes || 16 * GB };
  }
  if (state === "stopped") {
    c.stats = null;
    c.viewers = [];
    c.mics = [];
  }
  changed(c);
  if (later) setTimeout(() => setState(c, later), 1500);
}

let jobSeq = 1;
function job(messages: string[], after: () => void, desktop?: string): { job: string } {
  const id = `job${jobSeq++}`;
  const steps: Job[] = messages.map((m, i) => ({ status: "running", message: m, progress: (i + 1) / (messages.length + 1), desktop }));
  steps.push({ status: "done", message: "Done", progress: 1, desktop });
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
  return { cpus: 8, memBytes: 16 * GB, running: running.length, total: list.length, memUsed: running.reduce((s, c) => s + (c.stats?.memBytes ?? 0), 0) };
}

// Stats every 5 s and a visitor now and then, so the pages move. Started by
// the dev server only: timers at import time would keep `vite build` alive.
function startTimers(): void {
setInterval(() => {
  const t = new Date().toISOString();
  for (const c of desktops) {
    if (c.state !== "running" || !c.stats) continue;
    const s = c.stats;
    s.cpuPercent = Math.max(0.5, Math.min(99, s.cpuPercent * 0.7 + (s.cpuPercent + (Math.random() - 0.5) * 30) * 0.3));
    s.memBytes = Math.max(0.2 * GB, Math.min(s.memLimit, s.memBytes + (Math.random() - 0.5) * 0.05 * GB));
    broadcast("stats", { name: c.name, ...s, t });
  }
}, 5000);
setInterval(() => {
  const alice = desktops.find((c) => c.name === "alice");
  if (!alice || alice.state !== "running") return;
  alice.viewers = alice.viewers.includes("sam") ? alice.viewers.filter((v) => v !== "sam") : [...alice.viewers, "sam"];
  broadcast("viewers", { name: "alice", viewers: alice.viewers, mics: alice.mics, forwards: alice.forwards, agents: alice.agents });
}, 20000);
// The agents' summaries move on, as they would from the supervisor (once a second at most).
setInterval(() => {
  for (const c of desktops) if (c.agents.length) broadcast("activity", activityOf(c));
}, 5000);
}

function history(c: Desktop) {
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
<img id="videoStream" src="/api/desktops/${name}/screen.png?full=1" style="position:absolute;inset:0;width:100%;height:100%;object-fit:fill" alt="">
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
  setCamera() {},
  setMic() {},
  setOutput() {},
  micLevel: () => (media.mic ? 0.2 + Math.random() * 0.4 : 0),
  video: () => document.getElementById("videoStream"),
};
// Sound, mic and webcam: pipelineControl turns a capture on where allowed.
let media = { sound: false, mic: false, webcam: false, playing: false, soundAt: 0 };
const allowed = { microphone: false, webcam: false };
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
    setTimeout(() => setMedia({ [msg.pipeline === "microphone" ? "mic" : "webcam"]: msg.enabled && allowed[msg.pipeline] }), 300);
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
    if (desktops.some((c) => c.name === name)) return json(res, 200, { ok: false, error: `There's already a desktop called ${name}.`, folderExists: true });
    if (["api", "login", "logout", "assets", "static", "healthz"].includes(name)) return json(res, 200, { ok: false, error: `${name} is reserved.`, folderExists: false });
    return json(res, 200, { ok: true, folderExists: name === "old-project", folder: `${FOLDER}/${name}` });
  }
  if (path === "/api/desktops" && method === "GET") return json(res, 200, visible());
  if (path === "/api/desktops" && method === "POST") {
    const b = await readBody(req);
    const name = String(b.name);
    const img = images.find((i) => i.ref === b.image) ?? images[0]!;
    const c: Desktop = {
      id: `${name}-id`, name, state: "creating", startedAt: "", created: new Date().toISOString(), job: null, folder: `${FOLDER}/${name}`, image: img.ref, imageTitle: img.title,
      imageVersion: img.version, updateAvailable: null, settings: { memoryBytes: Number(b.memoryBytes), nanoCpus: Number(b.nanoCpus) },
      access: (b.access as string[]) ?? [], viewers: [], mics: [], webcam: false, forwards: [], agents: [], tokens: 0, stats: null,
    };
    desktops.push(c);
    changed(c);
    return json(res, 202, job(["Pulling image, 120 of 820 MB", "Pulling image, 540 of 820 MB", "Creating the container", "Starting"], () => setState(c, "running"), name));
  }
  if (path === "/api/accounts" && method === "GET") {
    return json(res, 200, m.admin ? accounts.map(({ password: _, ...a }) => a) : accounts.map((a) => ({ username: a.username })));
  }
  if (path === "/api/accounts" && method === "POST") {
    const b = await readBody(req);
    const a = { username: String(b.username), password: String(b.password), admin: Boolean(b.admin), desktops: (b.desktops as string[]) ?? [], created: new Date().toISOString(), lastLogin: null };
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
    if (Array.isArray(b.desktops)) a.desktops = b.desktops as string[];
    return json(res, 200, a);
  }
  if (path === "/api/images" && method === "GET") {
    refreshImages();
    return json(res, 200, images);
  }
  if (path === "/api/images" && method === "POST") {
    const b = await readBody(req);
    if (!String(b.ref).includes(":")) return json(res, 400, { error: "Give a reference with a tag." });
    return json(res, 202, job(["Pulling " + String(b.ref), "Checking the image contract"], () => {
      images.push({ ref: String(b.ref), title: "Custom image", version: "1.0", description: "Added in the mock", desktops: [], behind: [], checked: new Date().toISOString() });
    }));
  }
  if (path === "/api/images" && method === "DELETE") {
    const i = images.findIndex((x) => x.ref === url.searchParams.get("ref"));
    if (i >= 0) images.splice(i, 1);
    return json(res, 204);
  }
  if (path === "/api/images/check") return json(res, 202, job(["Pulling ghcr.io/zefhemel/deskyard-debian-xfce:latest", "Pulling ghcr.io/zefhemel/deskyard-debian-xfce:beta"], () => images.forEach((i) => (i.checked = new Date().toISOString()))));
  if (path === "/api/images/upgrade") {
    const b = await readBody(req);
    return json(res, 202, job(["Upgrading", "Starting"], () => {
      for (const c of desktops) if (c.image === b.ref && c.updateAvailable) {
        c.imageVersion = c.updateAvailable;
        c.updateAvailable = null;
        changed(c);
      }
    }));
  }

  const cm = /^\/api\/desktops\/([^/]+)(?:\/(.+))?$/.exec(path);
  const c = cm?.[1] ? visible().find((x) => x.name === cm[1]) && desktops.find((x) => x.name === cm[1]) : undefined;
  if (!cm || !c) return json(res, 404, { error: "No such desktop" });
  const sub = cm[2] ?? "";

  if (sub === "" && method === "GET") return json(res, 200, visible().find((x) => x.name === c.name));
  if (sub === "" && method === "DELETE") {
    desktops.splice(desktops.indexOf(c), 1);
    broadcast("removed", { name: c.name });
    return json(res, 204);
  }
  if (sub === "" && method === "PATCH") {
    const b = await readBody(req);
    return json(res, 202, job(["Recreating the container", "Starting"], () => {
      if (typeof b.memoryBytes === "number") c.settings.memoryBytes = b.memoryBytes;
      if (typeof b.nanoCpus === "number") c.settings.nanoCpus = b.nanoCpus;
      if (typeof b.image === "string") c.image = b.image;
      changed(c);
    }, c.name));
  }
  if (sub === "start") return setState(c, "starting", "running"), json(res, 204);
  if (sub === "stop") return setState(c, "stopped"), json(res, 204);
  if (sub === "restart") return setState(c, "starting", "running"), json(res, 204);
  if (sub === "access") {
    c.access = ((await readBody(req)).usernames as string[]) ?? [];
    changed(c);
    return json(res, 204);
  }
  if (sub === "upgrade") {
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
      remoteAdd: `deskyard remote add ${c.name} https://desktops.example.com/${c.name} --token dyt_${id}_${secret}`,
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

export function mockApi(): Plugin {
  return {
    name: "supervisor-mock-api",
    apply: "serve",
    configureServer(server) {
      if (process.env.DESKYARD_API) return; // proxying to a real supervisor instead
      startTimers();
      server.middlewares.use((req, res, next) => {
        handle(req, res, next).catch((e: unknown) => json(res, 500, { error: String(e) }));
      });
    },
  };
}
