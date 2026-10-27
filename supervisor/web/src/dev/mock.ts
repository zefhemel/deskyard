// A fake supervisor API for `npm run dev` (Vite dev-server middleware only:
// `apply: "serve"` keeps it out of `vite build`). It follows supervisor/API.md
// closely enough to click through every page. Sign in as zef (admin), anna
// (regular; she observes notes) or sam with any password; "wrong" fails.
// sanne observes alice: sign in as her through a link from "New link" on
// alice's Access tab. /link#dyl_5a17e0aa_mock-bob-link signs in as bob once,
// and /cli-login#mock-request is a pending `deskyard login`.
import { createHash, randomBytes } from "node:crypto";
import { readFileSync, existsSync } from "node:fs";
import type { IncomingMessage, ServerResponse } from "node:http";
import type { Duplex } from "node:stream";
import { fileURLToPath } from "node:url";
import type { Plugin } from "vite";
import type { AccessGrant, Account, Environment, Grant, Job, Level, Share } from "../api.ts";
import { accounts, activityOf, cliLogins, conflicts, credentials, environments, FOLDER, images, links, type MockAccount, processRows, refreshImages, volumes } from "./mock-data.ts";


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
  // One session at a time here: events go out as that account would get them.
  const m = me();
  if (event === "environment" && m) {
    const c = data as Environment;
    if (levelOf(m, c.name) === null) return;
    data = asSeenBy(c, m);
  }
  if (event === "viewers" && m && levelOf(m, (data as { name: string }).name) === "observe") data = { ...(data as object), forwards: [], agents: [] };
  for (const s of streams) s.write(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
  for (const send of eventSockets) send(event, data);
}

function me() {
  const a = accounts.find((a) => a.username === session);
  return a && (!a.expires || Date.parse(a.expires) > Date.now()) ? a : undefined;
}

function meJSON(a: MockAccount) {
  return { username: a.username, admin: a.admin, kind: a.password === null ? "token" : "password", share: a.sharedBy !== null };
}

function accountJSON(a: MockAccount): Account {
  const { password, id: _, ...rest } = a;
  return { ...rest, kind: password === null ? "token" : "password", linkPending: [...links.values()].includes(a.username), grants: [...a.grants].sort((x, y) => x.environment.localeCompare(y.environment)) };
}

/** An account's level on an environment, or null without access. */
function levelOf(a: MockAccount, env: string): Level | null {
  if (a.admin) return "full";
  return a.grants.find((g) => g.environment === env)?.level ?? null;
}

/** The regular accounts granted an environment, as admins see it. */
function accessOf(env: string): AccessGrant[] {
  return accounts
    .filter((a) => !a.admin && !a.sharedBy)
    .flatMap((a) => a.grants.filter((g) => g.environment === env).map((g) => ({ username: a.username, level: g.level })))
    .sort((x, y) => x.username.localeCompare(y.username));
}

const blankVPN: Environment["vpn"] = { state: "disabled", changedAt: "0001-01-01T00:00:00Z", exitIpObservedAt: "0001-01-01T00:00:00Z", desiredRevision: 0, appliedRevision: 0, idle: false };

/** An environment as an account sees it: an observer's has its configuration blanked. */
function asSeenBy(c: Environment, a: MockAccount): Environment {
  const level = levelOf(a, c.name) ?? "full";
  if (level === "observe") {
    return {
      ...c, level, folder: "", memoryReclaim: false, webdav: false, access: [], terminals: [], forwards: [], agents: [], vpn: blankVPN,
      settings: { memoryBytes: 0, nanoCpus: 0, dockerAccess: false, vpnConnectionId: null, lanExceptions: [], volumes: [] },
    };
  }
  return { ...c, level, access: a.admin ? accessOf(c.name) : [] };
}

function visible(): Environment[] {
  const m = me();
  if (!m) return [];
  return environments
    .filter((c) => levelOf(m, c.name) !== null)
    .map((c) => asSeenBy(c, m))
    .sort((a, b) => a.name.localeCompare(b.name));
}

function viewersOf(c: Environment) {
  return { name: c.name, viewers: c.viewers, observers: c.observers, mics: c.mics, forwards: c.forwards, agents: c.agents };
}

const hex = (n: number) => randomBytes(n).toString("hex").slice(0, n);

/** A new login link for an account, replacing its unused one. */
function newLink(req: IncomingMessage, username: string): string {
  for (const [code, u] of links) if (u === username) links.delete(code);
  const code = `dyl_${hex(8)}_${randomBytes(32).toString("base64url")}`;
  links.set(code, username);
  return `http://${req.headers.host ?? "localhost:5173"}/link#${code}`;
}

/** The environments a redeemed link or approved CLI login reports. */
function environmentsOf(a: MockAccount) {
  return environments
    .filter((c) => levelOf(a, c.name) !== null)
    .map((c) => ({ name: c.name, level: levelOf(a, c.name)! }))
    .sort((x, y) => x.name.localeCompare(y.name));
}

function shareJSON(a: MockAccount): Share {
  const g = a.grants[0]!;
  const env = environments.find((c) => c.name === g.environment);
  return {
    username: a.username, environment: g.environment, level: g.level, sharedBy: a.sharedBy!, expires: a.expires, created: a.created,
    lastLogin: a.lastLogin, lastUsed: a.lastLogin, linkPending: [...links.values()].includes(a.username), connected: env?.viewers.includes(a.username) ?? false,
  };
}

/** Deletes an account with its link, credentials and the shares it made, and closes its viewing. */
function removeAccount(a: MockAccount): void {
  for (const share of accounts.filter((x) => x.sharedBy?.id === a.id)) removeAccount(share);
  const i = accounts.indexOf(a);
  if (i >= 0) accounts.splice(i, 1);
  for (const [code, u] of links) if (u === a.username) links.delete(code);
  delete credentials[a.username];
  for (const c of environments) {
    if (!c.viewers.includes(a.username)) continue;
    c.viewers = c.viewers.filter((v) => v !== a.username);
    c.observers = c.observers.filter((v) => v !== a.username);
    broadcast("viewers", viewersOf(c));
  }
}

/** Ends the shares of an environment made by an account that no longer has full access to it. */
function endOrphanShares(): void {
  for (const share of accounts.filter((x) => x.sharedBy)) {
    const sharer = accounts.find((x) => x.id === share.sharedBy!.id);
    if (!sharer || levelOf(sharer, share.grants[0]!.environment) !== "full") removeAccount(share);
  }
}

function checkGrants(grants: unknown): string | null {
  if (!Array.isArray(grants)) return null;
  const seen = new Set<string>();
  for (const g of grants as Grant[]) {
    if (!environments.some((c) => c.name === g.environment)) return `no environment ${g.environment}`;
    if (g.level !== "full" && g.level !== "observe") return `the level must be full or observe`;
    if (seen.has(g.environment)) return `${g.environment} is listed twice`;
    seen.add(g.environment);
  }
  return null;
}

const USERNAME = /^[a-z][a-z0-9._-]{0,31}$/;
const RESERVED = ["api", "login", "logout", "assets", "static", "healthz", "desktops", "environments", "images", "accounts", "volumes", "link", "cli-login", "account"];

/** The routes that need no session: link sign-in and the CLI's side of `deskyard login`. */
async function signedOut(req: IncomingMessage, res: ServerResponse, path: string, method: string): Promise<boolean> {
  if (method !== "POST") return false;
  if (path === "/api/links/inspect" || path === "/api/links/redeem") {
    const b = await readBody(req);
    const username = links.get(String(b.link));
    const a = accounts.find((x) => x.username === username);
    if (!a) {
      json(res, 410, { error: "this link was already used or replaced" });
      return true;
    }
    if (path === "/api/links/inspect") {
      const { username, description, grants, sharedBy, expires } = accountJSON(a);
      json(res, 200, { username, description, grants, sharedBy, expires });
      return true;
    }
    if (b.client === "cli" && !a.admin && !a.grants.some((g) => g.level === "full")) {
      json(res, 403, { error: "observe-only access is for the browser" });
      return true;
    }
    links.delete(String(b.link));
    a.lastLogin = new Date().toISOString();
    if (b.client === "cli") {
      const id = hex(8);
      (credentials[a.username] ??= []).unshift({ id, kind: "cli", name: String(b.name ?? "deskyard CLI"), created: a.lastLogin, lastUsed: null });
      json(res, 200, { credential: `dyc_${id}_${randomBytes(32).toString("base64url")}`, username: a.username, environments: environmentsOf(a) });
      return true;
    }
    session = a.username;
    const only = !a.admin && a.grants.length === 1 ? a.grants[0]!.environment : null;
    json(res, 200, { username: a.username, next: only ? `/${only}/` : "/" });
    return true;
  }
  if (path === "/api/cli-login") {
    const client = String((await readBody(req)).client ?? "");
    if (!client || client.length > 100) {
      json(res, 400, { error: "a client name is required" });
      return true;
    }
    const id = hex(16);
    const letters = "BCDFGHJKLMNPQRSTVWXZ";
    const code = Array.from(randomBytes(8), (x) => letters[x % letters.length]).join("");
    cliLogins.set(id, { client, ip: "127.0.0.1", code: `${code.slice(0, 4)}-${code.slice(4)}`, created: new Date().toISOString(), answer: null, approver: null });
    json(res, 200, { id, code: cliLogins.get(id)!.code, approveURL: `http://${req.headers.host}/cli-login#${id}`, pollSecret: "mock", interval: 2, expiresIn: 600 });
    return true;
  }
  const poll = /^\/api\/cli-login\/([^/]+)\/token$/.exec(path);
  if (poll?.[1]) {
    const r = cliLogins.get(poll[1]);
    if (!r) json(res, 410, { error: "this login request expired or was already used; run deskyard login again" });
    else if (r.answer === null) json(res, 202, { status: "pending" });
    else {
      cliLogins.delete(poll[1]);
      const a = accounts.find((x) => x.username === r.approver);
      if (r.answer === "denied") json(res, 403, { error: "denied" });
      else if (!a) json(res, 410, { error: "this login request expired or was already used; run deskyard login again" });
      else {
        const id = hex(8);
        (credentials[a.username] ??= []).unshift({ id, kind: "cli", name: r.client, created: new Date().toISOString(), lastUsed: null });
        json(res, 200, { credential: `dyc_${id}_${randomBytes(32).toString("base64url")}`, username: a.username, environments: environmentsOf(a) });
      }
    }
    return true;
  }
  return false;
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
    c.observers = [];
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
  alice.viewers = alice.viewers.includes("anna") ? alice.viewers.filter((v) => v !== "anna") : [...alice.viewers, "anna"].sort();
  broadcast("viewers", viewersOf(alice));
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
    // Token accounts have no password: they sign in with login links.
    if (!a || a.password === null || b.password === "wrong" || !b.password) {
      failures++;
      return json(res, 401, { error: "wrong username or password" });
    }
    session = a.username;
    a.lastLogin = new Date().toISOString();
    return json(res, 200, meJSON(a));
  }
  if (await signedOut(req, res, path, method)) return;
  const m = me();
  if (!m) return json(res, 401, { error: "sign in first" });

  if (path === "/api/logout") {
    session = null;
    return json(res, 204);
  }
  if (path === "/api/me" && method === "GET") return json(res, 200, meJSON(m));
  if (path === "/api/me/password") return m.password === null ? json(res, 400, { error: "token accounts have no password" }) : json(res, 204);
  if (path === "/api/me/sign-out-everywhere" && method === "POST") {
    credentials[m.username] = (credentials[m.username] ?? []).filter((d) => d.kind !== "webdav");
    return json(res, 204);
  }
  if (path === "/api/me/webdav-password") {
    const others = (credentials[m.username] ?? []).filter((d) => d.kind !== "webdav");
    if (method === "DELETE") return (credentials[m.username] = others), json(res, 204);
    if (method === "POST") {
      const id = Math.random().toString(16).slice(2, 10);
      credentials[m.username] = [{ id, kind: "webdav", name: "WebDAV password", created: new Date().toISOString(), lastUsed: null }, ...others];
      return json(res, 201, { password: `dyc_${id}_mock-webdav-password` });
    }
  }
  if (path === "/api/me/credentials" && method === "GET") return json(res, 200, (credentials[m.username] ?? []).map((d) => ({ ...d, current: false })));
  const myCred = /^\/api\/me\/credentials\/([^/]+)$/.exec(path);
  if (myCred?.[1] && method === "DELETE") {
    const list = credentials[m.username] ?? [];
    if (!list.some((d) => d.id === myCred[1])) return json(res, 404, { error: "no such credential" });
    credentials[m.username] = list.filter((d) => d.id !== myCred[1]);
    return json(res, 204);
  }
  const cli = /^\/api\/cli-login\/([^/]+)(?:\/(approve|deny))?$/.exec(path);
  if (cli?.[1]) {
    const r = cliLogins.get(cli[1]);
    if (!r || r.answer !== null) return json(res, 404, { error: "no such login request" });
    if (!cli[2]) return json(res, 200, { client: r.client, ip: r.ip, code: r.code, created: r.created });
    if (cli[2] === "approve" && !m.admin && !m.grants.some((g) => g.level === "full")) return json(res, 403, { error: "observe-only access is for the browser" });
    r.answer = cli[2] === "approve" ? "approved" : "denied";
    r.approver = m.username;
    return json(res, 204);
  }
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
    if (RESERVED.includes(name)) return json(res, 200, { ok: false, error: `${name} is reserved.`, folderExists: false });
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
      memoryReclaim: true, webdav: false, imageVersion: img.version, updateAvailable: null, settings: { dockerAccess: b.dockerAccess === true, memoryBytes: Number(b.memoryBytes), nanoCpus: Number(b.nanoCpus), vpnConnectionId: null, lanExceptions: [], volumes: (b.volumes as Environment["settings"]["volumes"]) ?? [] },
      vpn: blankVPN, level: "full", access: [], viewers: [], observers: [], mics: [], terminal: true, terminals: [], forwards: [], agents: [], stats: null,
    };
    for (const g of (b.access as AccessGrant[]) ?? []) accounts.find((a) => a.username === g.username)?.grants.push({ environment: name, level: g.level });
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
    if (RESERVED.includes(name)) return json(res, 400, { error: `${name} is reserved` });
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
    return json(res, 200, m.admin ? accounts.map(accountJSON).sort((x, y) => x.username.localeCompare(y.username)) : accounts.filter((a) => a === m || (!m.sharedBy && (!a.sharedBy || a.sharedBy.id === m.id))).map((a) => ({ username: a.username })));
  }
  if (path.startsWith("/api/accounts") && !m.admin) return json(res, 403, { error: "admins only" });
  if (path === "/api/accounts" && method === "POST") {
    const b = await readBody(req);
    const username = String(b.username ?? "");
    if (!USERNAME.test(username)) return json(res, 400, { error: "invalid username" });
    if (accounts.some((x) => x.username === username)) return json(res, 409, { error: "an account with that name already exists" });
    const bad = checkGrants(b.grants);
    if (bad) return json(res, 400, { error: bad });
    const password = typeof b.password === "string" && b.password ? b.password : null;
    if (b.admin === true && password && b.expires) return json(res, 400, { error: "admins with a password can't expire" });
    const a: MockAccount = {
      id: hex(8), username, password, admin: b.admin === true, description: String(b.description ?? ""), grants: (b.grants as Grant[]) ?? [],
      sharedBy: null, expires: (b.expires as string | null) ?? null, created: new Date().toISOString(), lastLogin: null,
    };
    accounts.push(a);
    return json(res, 201, accountJSON(a));
  }
  const accLink = /^\/api\/accounts\/([^/]+)\/link$/.exec(path);
  if (accLink?.[1] && method === "POST") {
    const a = accounts.find((x) => x.username === accLink[1]);
    if (!a) return json(res, 404, { error: "no such account" });
    if (a.password !== null) return json(res, 400, { error: "password accounts sign in with their password" });
    return json(res, 200, { link: newLink(req, a.username) });
  }
  const accCreds = /^\/api\/accounts\/([^/]+)\/credentials(?:\/([^/]+))?$/.exec(path);
  if (accCreds?.[1]) {
    const a = accounts.find((x) => x.username === accCreds[1]);
    if (!a) return json(res, 404, { error: "no such account" });
    const list = credentials[a.username] ?? [];
    if (!accCreds[2]) return json(res, 200, list.map((d) => ({ ...d, current: false })));
    if (!list.some((d) => d.id === accCreds[2])) return json(res, 404, { error: "no such credential" });
    credentials[a.username] = list.filter((d) => d.id !== accCreds[2]);
    return json(res, 204);
  }
  const acc = /^\/api\/accounts\/([^/]+)$/.exec(path);
  if (acc?.[1]) {
    const a = accounts.find((x) => x.username === acc[1]);
    if (!a) return json(res, 404, { error: "no such account" });
    const passwordAdmins = accounts.filter((x) => x.admin && x.password !== null);
    const lastAdmin = a.admin && a.password !== null && passwordAdmins.length === 1;
    const guard = "the last admin with a password can't be deleted, made a regular account or lose the password";
    if (method === "DELETE") {
      if (lastAdmin) return json(res, 409, { error: guard });
      removeAccount(a);
      endOrphanShares();
      return json(res, 204);
    }
    const b = await readBody(req);
    if (a.sharedBy && (b.admin !== undefined || b.grants !== undefined)) return json(res, 400, { error: "a share's access is set when it is shared" });
    if (b.admin === false && lastAdmin) return json(res, 409, { error: guard });
    if (typeof b.password === "string" && a.password === null) return json(res, 400, { error: "token accounts have no password" });
    const bad = checkGrants(b.grants);
    if (bad) return json(res, 400, { error: bad });
    const admin = typeof b.admin === "boolean" ? b.admin : a.admin;
    const expires = b.expires !== undefined ? (b.expires as string | null) : a.expires;
    if (admin && a.password !== null && expires) return json(res, 400, { error: "admins with a password can't expire" });
    a.admin = admin;
    a.expires = expires;
    if (typeof b.description === "string") a.description = b.description;
    if (typeof b.password === "string" && b.password) a.password = b.password;
    if (Array.isArray(b.grants)) a.grants = b.grants as Grant[];
    endOrphanShares();
    for (const c of environments) changed(c);
    return json(res, 200, accountJSON(a));
  }
  const shareMatch = /^\/api\/shares\/([^/]+)(\/link)?$/.exec(path);
  if (shareMatch?.[1]) {
    const a = accounts.find((x) => x.username === shareMatch[1] && x.sharedBy);
    if (!a) return json(res, 404, { error: "no such share" });
    if (!m.admin && a.sharedBy!.id !== m.id) return json(res, 403, { error: "only the person who shared it or an admin can do that" });
    if (shareMatch[2] && method === "POST") return json(res, 200, { link: newLink(req, a.username) });
    if (!shareMatch[2] && method === "DELETE") {
      removeAccount(a);
      return json(res, 204);
    }
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
  // Observers may watch, and nothing else.
  if (levelOf(m, c.name) === "observe" && !(method === "GET" && ["", "stats", "screen.png", "screen-events"].includes(sub))) {
    return json(res, 403, { error: "observe-only access can't do that" });
  }

  if (sub === "shares" && method === "GET") {
    return json(res, 200, accounts.filter((a) => a.sharedBy && a.grants[0]?.environment === c.name).map(shareJSON).sort((x, y) => x.username.localeCompare(y.username)));
  }
  if (sub === "shares" && method === "POST") {
    if (m.sharedBy) return json(res, 403, { error: "shares can't share" });
    const b = await readBody(req);
    const username = String(b.username ?? "");
    if (!USERNAME.test(username)) return json(res, 400, { error: "invalid username" });
    if (b.level !== "full" && b.level !== "observe") return json(res, 400, { error: "the level must be full or observe" });
    if (b.expires && Date.parse(String(b.expires)) <= Date.now()) return json(res, 400, { error: "the expiry must be in the future" });
    if (accounts.some((x) => x.username === username)) return json(res, 409, { error: "an account with that name already exists" });
    const a: MockAccount = {
      id: hex(8), username, password: null, admin: false, description: "", grants: [{ environment: c.name, level: b.level }],
      sharedBy: { username: m.username, id: m.id }, expires: (b.expires as string | null) ?? null, created: new Date().toISOString(), lastLogin: null,
    };
    accounts.push(a);
    const link = newLink(req, username);
    return json(res, 201, { share: shareJSON(a), link });
  }

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
    for (const a of [...accounts]) {
      if (a.sharedBy && a.grants[0]?.environment === c.name) removeAccount(a);
      else a.grants = a.grants.filter((g) => g.environment !== c.name);
    }
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
      if (Array.isArray(b.volumes)) c.settings.volumes = b.volumes as Environment["settings"]["volumes"];
      if (typeof b.image === "string") c.image = b.image;
      changed(c);
    }, c.name));
  }
  if (sub === "start") return setState(c, "starting", "running"), json(res, 204);
  if (sub === "webdav" && method === "PUT") {
    c.webdav = (await readBody(req)).webdav === true;
    changed(c);
    return json(res, 204);
  }
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
    const grants = ((await readBody(req)).grants as AccessGrant[]) ?? [];
    for (const g of grants) {
      const a = accounts.find((x) => x.username === g.username);
      if (!a) return json(res, 400, { error: `no account ${g.username}` });
      if (a.sharedBy) return json(res, 400, { error: `${g.username} is a share; change it under Shares on the Access tab` });
    }
    for (const a of accounts) {
      if (a.admin || a.sharedBy) continue;
      const g = grants.find((x) => x.username === a.username);
      a.grants = [...a.grants.filter((x) => x.environment !== c.name), ...(g ? [{ environment: c.name, level: g.level }] : [])];
    }
    endOrphanShares();
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
    const me = m.username;
    c.mics = on ? [...new Set([...c.mics, me])] : c.mics.filter((x) => x !== me);
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
