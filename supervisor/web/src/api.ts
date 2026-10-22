// Typed client for the supervisor's HTTP API (supervisor/API.md).

export interface Me {
  username: string;
  admin: boolean;
}

export interface Host {
  cpus: number;
  memBytes: number;
  running: number;
  total: number;
  memUsed: number;
}

export type DesktopState = "running" | "stopped" | "starting" | "creating" | "upgrading" | "removing";

export interface Stats {
  cpuPercent: number;
  memBytes: number;
  memLimit: number;
}

export interface Settings {
  memoryBytes: number;
  nanoCpus: number;
}

export interface Desktop {
  id: string;
  name: string;
  state: DesktopState;
  startedAt: string;
  folder: string;
  image: string;
  imageTitle: string;
  imageVersion: string;
  updateAvailable: string | null;
  settings: Settings;
  access: string[];
  viewers: string[];
  tokens: number;
  stats: Stats | null;
}

export interface Processes {
  titles: string[];
  rows: (string | number)[][];
}

export interface StatsPoint {
  t: string;
  cpuPercent: number;
  memBytes: number;
}

export interface StatsHistory {
  history: StatsPoint[];
  memLimit: number;
}

export interface NameCheck {
  ok: boolean;
  error?: string;
  folderExists: boolean;
  /** Not in API.md (yet): the folder the desktop's files would live in. */
  folder?: string;
}

export interface Job {
  status: "running" | "done" | "failed";
  message: string;
  progress: number | null;
  error?: string;
  desktop?: string;
}

export interface Account {
  username: string;
  admin: boolean;
  desktops: string[];
  created: string;
  lastLogin: string | null;
}

export interface Token {
  id: string;
  name: string;
  createdBy: string;
  created: string;
  expires: string | null;
  lastUsed: string | null;
  expired: boolean;
}

export interface CreatedToken extends Token {
  token: string;
  remoteAdd: string;
}

export interface CatalogImage {
  ref: string;
  title: string;
  version: string;
  description: string;
  desktops: string[];
  behind: string[];
  checked: string | null;
}

export interface JobRef {
  job: string;
}

// Events on /api/events.
export interface StatsEvent extends Stats {
  name: string;
  t: string;
}
export interface ViewersEvent {
  name: string;
  viewers: string[];
}
export interface RemovedEvent {
  name: string;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

/** Sends the browser to the login page, coming back here afterwards. */
export function goToLogin(): void {
  if (location.pathname === "/login") return;
  const next = location.pathname + location.search;
  location.href = `/login?next=${encodeURIComponent(next)}`;
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: "same-origin", headers: {} };
  if (body !== undefined) {
    init.body = JSON.stringify(body);
    init.headers = { "Content-Type": "application/json" };
  }
  const res = await fetch(path, init);
  if (res.status === 401 && path !== "/api/login") {
    goToLogin();
    throw new ApiError(401, "Signed out");
  }
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`;
    try {
      const data: unknown = await res.json();
      if (data && typeof data === "object" && "error" in data && typeof data.error === "string") message = data.error;
    } catch {
      // not JSON
    }
    throw new ApiError(res.status, message);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

const enc = encodeURIComponent;
const c = (name: string) => `/api/desktops/${enc(name)}`;

export const api = {
  login: (username: string, password: string) => request<Me>("POST", "/api/login", { username, password }),
  logout: () => request<void>("POST", "/api/logout"),
  me: () => request<Me>("GET", "/api/me"),
  changePassword: (current: string, next: string) => request<void>("POST", "/api/me/password", { current, new: next }),
  host: () => request<Host>("GET", "/api/host"),

  desktops: () => request<Desktop[]>("GET", "/api/desktops"),
  desktop: (name: string) => request<Desktop>("GET", c(name)),
  createDesktop: (body: { name: string; image: string; memoryBytes: number; nanoCpus: number; access: string[] }) =>
    request<JobRef>("POST", "/api/desktops", body),
  power: (name: string, action: "start" | "stop" | "restart") => request<void>("POST", `${c(name)}/${action}`),
  removeDesktop: (name: string, files: boolean) => request<void>("DELETE", c(name) + (files ? "?files=1" : "")),
  updateSettings: (name: string, body: { image?: string; memoryBytes?: number; nanoCpus?: number }) =>
    request<JobRef>("PATCH", c(name), body),
  setAccess: (name: string, usernames: string[]) => request<void>("PUT", `${c(name)}/access`, { usernames }),
  processes: (name: string) => request<Processes>("GET", `${c(name)}/processes`),
  stats: (name: string) => request<StatsHistory>("GET", `${c(name)}/stats`),
  keys: (name: string, keys: string) => request<void>("POST", `${c(name)}/keys`, { keys }),
  upgrade: (name: string) => request<JobRef>("POST", `${c(name)}/upgrade`),
  validateName: (name: string) => request<NameCheck>("GET", `/api/validate-name?name=${enc(name)}`),
  screenUrl: (name: string, opts: { full?: boolean; bust?: number } = {}) => {
    const q = new URLSearchParams();
    if (opts.full) q.set("full", "1");
    if (opts.bust !== undefined) q.set("t", String(opts.bust));
    const qs = q.toString();
    return `${c(name)}/screen.png${qs ? `?${qs}` : ""}`;
  },

  accounts: () => request<Account[]>("GET", "/api/accounts"),
  createAccount: (body: { username: string; password: string; admin: boolean; desktops: string[] }) =>
    request<Account>("POST", "/api/accounts", body),
  updateAccount: (username: string, body: { password?: string; admin?: boolean; desktops?: string[] }) =>
    request<Account>("PATCH", `/api/accounts/${enc(username)}`, body),
  deleteAccount: (username: string) => request<void>("DELETE", `/api/accounts/${enc(username)}`),

  tokens: (name: string) => request<Token[]>("GET", `${c(name)}/tokens`),
  createToken: (name: string, body: { name: string; expires: string | null }) =>
    request<CreatedToken>("POST", `${c(name)}/tokens`, body),
  deleteToken: (name: string, id: string) => request<void>("DELETE", `${c(name)}/tokens/${enc(id)}`),

  images: () => request<CatalogImage[]>("GET", "/api/images"),
  addImage: (ref: string) => request<JobRef>("POST", "/api/images", { ref }),
  removeImage: (ref: string) => request<void>("DELETE", `/api/images?ref=${enc(ref)}`),
  checkImages: () => request<JobRef>("POST", "/api/images/check"),
  upgradeImage: (ref: string) => request<JobRef>("POST", "/api/images/upgrade", { ref }),
};

/** Follows a job's event stream until it is done or failed. Returns a function that stops following. */
export function watchJob(id: string, onUpdate: (job: Job) => void): () => void {
  const es = new EventSource(`/api/jobs/${enc(id)}`);
  let finished = false;
  es.onmessage = (e: MessageEvent<string>) => {
    const job = JSON.parse(e.data) as Job;
    onUpdate(job);
    if (job.status !== "running") {
      finished = true;
      es.close();
    }
  };
  es.onerror = () => {
    if (finished) return;
    es.close();
    // The stream ending without a final status: find out whether we were signed out.
    api.me().then(
      () => onUpdate({ status: "failed", message: "", progress: null, error: "Lost the connection to the job" }),
      () => undefined,
    );
  };
  return () => {
    finished = true;
    es.close();
  };
}
