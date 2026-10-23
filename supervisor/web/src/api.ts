// Typed client for the supervisor's HTTP API (supervisor/API.md).

export interface Me {
  username: string;
  admin: boolean;
}

export interface Host {
  /** Result of the supervisor's complete startup capability probe. */
  memoryReclaim: { enabled: boolean; reason?: string };
  cpus: number;
  memBytes: number;
  running: number;
  total: number;
  memUsed: number;
  /** The host's virtual cameras; null when the engine isn't on this machine. */
  cameras: HostCamera[] | null;
}

/** A host's virtual camera (v4l2loopback) and the desktop that has it, if any. */
export interface HostCamera {
  device: string;
  taken: boolean;
  /** Named only when this account can see that desktop. */
  desktop?: string;
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
  /** The host's virtual camera this desktop has to itself; "" for none. */
  camera: string;
}

export interface Desktop {
  /** Image supports named shared terminals. */
  terminal: boolean;
  autoReclaim: boolean;
  id: string;
  name: string;
  state: DesktopState;
  startedAt: string;
  created: string | null;
  /** The running job (create, upgrade, settings change), if any. */
  job: string | null;
  folder: string;
  image: string;
  imageTitle: string;
  imageVersion: string;
  /** The image's starting home holds secrets: desktops made from it start signed in as whoever made it. */
  imageSecrets: boolean;
  updateAvailable: string | null;
  settings: Settings;
  access: string[];
  viewers: string[];
  /** Viewers with a mic on (per stream; gone when the stream closes). */
  mics: string[];
  /** Viewers with a camera on (per stream, as mics). */
  cams: string[];
  /** Whether the running desktop has its virtual camera (settings.camera). */
  webcam: boolean;
  /** Port mappings agents have set up with `deskyard forward`; port is the desktop's. */
  forwards: Forward[];
  /** Agents at work in the desktop: from outside (a token) or inside (a program). */
  agents: AgentPresence[];
  tokens: number;
  stats: Stats | null;
}

export interface Processes {
  titles: string[];
  rows: (string | number)[][];
}

export interface Tool {
  tool: string;
  version: string;
  source: "desktop" | "image";
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
  /** The folder the desktop's files would live in (when the name is valid). */
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
  installed: boolean;
  official: boolean;
  ref: string;
  title: string;
  version: string;
  description: string;
  desktops: string[];
  behind: string[];
  checked: string | null;
  /** Built here from a recipe (see `recipe`). */
  custom: boolean;
  /** Its starting home holds secrets: desktops made from it are signed in as whoever made it. */
  secrets: boolean;
  /** Its base has a newer image in the catalog than the one it was built on. */
  baseUpdated: boolean;
  recipe: string | null;
}

export type Remote = { name: string; repoFile: string } | { name: string; url: string; gpgKey: string };
export type Flatpak = { id: string; remote?: string } | { ref: string };

export interface Recipe {
  slug: string;
  name: string;
  base: string;
  mise?: string[];
  remotes?: Remote[];
  flatpaks?: Flatpak[];
  commands?: string;
  homeFrom?: "image";
  secrets?: boolean;
}

export interface FolderSize {
  path: string;
  bytes: number;
  /** False when some directories couldn't be read, so bytes is a lower bound. */
  complete: boolean;
}

export interface FileEntry {
  name: string;
  dir: boolean;
  size: number;
  modified: string;
}

export interface FileListing {
  /** The folder's real path in the desktop, always /workspace or below. */
  path: string;
  entries: FileEntry[];
  /** True when the folder has more than the 2,000 newest entries shown. */
  truncated: boolean;
}

export interface Uploaded {
  path: string;
  bytes: number;
}

export type HomeGroupName = "tools" | "flatpak" | "settings" | "dotfiles" | "browser" | "other";

/** One group of a desktop's home folder, for an image made from it. */
export interface HomeGroup {
  name: HomeGroupName;
  /** What the group copies: its files without its secrets. */
  bytes: number;
  paths: string[];
  /** Left out unless ticked one by one. */
  secrets: string[];
}

export interface ImageScan {
  groups: HomeGroup[];
  /** The desktop's own Flatpak apps, installed again system-wide in the image. */
  flatpaks: { id: string; remote: string }[];
  remotes: { name: string; url: string }[];
  /** Everything that could be copied, secrets included. */
  totalBytes: number;
  /** What the image would leave out, and why. */
  notes: string[];
}

export interface ImageFromDesktop {
  name: string;
  slug: string;
  groups: HomeGroupName[];
  /** Secrets to include, as the scan lists them. */
  include: string[];
  /** Also save this recipe on the new image once it's built, to add tools on top later. */
  recipe?: { slug: string; name: string };
}

export interface JobRef {
  job: string;
}

// Events on /api/events.
export interface StatsEvent extends Stats {
  name: string;
  t: string;
}
export interface Forward {
  /** The name of the agent token that set it up. */
  token: string;
  /** in: this machine's port appears in the desktop; out: the desktop's port appears there. */
  direction: "in" | "out";
  port: number;
}
export interface ViewersEvent {
  name: string;
  viewers: string[];
  mics: string[];
  cams: string[];
  forwards: Forward[];
  agents: AgentPresence[];
}

// Agent activity: who is at work in a desktop and what they did in the last 15 minutes.
export interface AgentPresence {
  id: string;
  name: string;
  /** The account that made the token; empty for agents inside the desktop. */
  owner: string;
  inside: boolean;
  color: string;
  /** Something happened in the last 10 seconds. */
  active: boolean;
  lastActive: string;
}
export interface ActivityNow {
  kind: "command" | "browser" | "forward";
  text?: string;
  since: string;
}
export interface ActivityRecent {
  kind: "command" | "screen" | "browser" | "copy";
  t: string;
  text?: string;
  exit?: number;
  disconnected?: boolean;
  durationMs?: number;
  action?: string;
  count?: number;
  direction?: "in" | "out";
  path?: string;
  size?: number;
}
export interface AgentSummary extends AgentPresence {
  now: ActivityNow[];
  /** Newest first. */
  recent: ActivityRecent[];
}
export interface ActivityEvent {
  name: string;
  agents: AgentSummary[];
}
/** A screen action an agent made, from GET /api/desktops/{name}/screen-events. */
export interface ScreenEvent {
  agent: Pick<AgentPresence, "id" | "name" | "color" | "inside">;
  t: string;
  action: "click" | "double-click" | "triple-click" | "drag" | "scroll" | "key" | "hold-key" | "type";
  /** [x, y], or [x1, y1, x2, y2] for drag. */
  pos?: number[];
  button?: string;
  dir?: "up" | "down" | "left" | "right";
  n?: number;
  keys?: string[];
  /** type: how many characters, never the text. */
  chars?: number;
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

async function request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const init: RequestInit = { method, credentials: "same-origin", headers: {}, signal };
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

export interface MemoryReclaim {
  requestedBytes: number;
  beforeBytes: number;
  afterBytes: number;
  swapBeforeBytes: number;
  swapAfterBytes: number;
  partial: boolean;
}

export const api = {
  login: (username: string, password: string) => request<Me>("POST", "/api/login", { username, password }),
  logout: () => request<void>("POST", "/api/logout"),
  me: () => request<Me>("GET", "/api/me"),
  changePassword: (current: string, next: string) => request<void>("POST", "/api/me/password", { current, new: next }),
  host: () => request<Host>("GET", "/api/host"),

  desktops: () => request<Desktop[]>("GET", "/api/desktops"),
  desktop: (name: string) => request<Desktop>("GET", c(name)),
  terminalSessions: (name: string, signal?: AbortSignal) =>
    request<{ sessions: { name: string }[] }>("GET", `${c(name)}/terminal/sessions`, undefined, signal),
  createDesktop: (body: { name: string; image: string; memoryBytes: number; nanoCpus: number; access: string[] }) =>
    request<JobRef>("POST", "/api/desktops", body),
  power: (name: string, action: "start" | "stop" | "restart") => request<void>("POST", `${c(name)}/${action}`),
  setMemoryPolicy: (name: string, autoReclaim: boolean) => request<void>("PUT", `${c(name)}/memory-policy`, { autoReclaim }),
  reclaimMemory: (name: string) => request<MemoryReclaim>("POST", `${c(name)}/reclaim-memory`),
  removeDesktop: (name: string, files: boolean) => request<void>("DELETE", c(name) + (files ? "?files=1" : "")),
  updateSettings: (name: string, body: { image?: string; memoryBytes?: number; nanoCpus?: number; camera?: string; confirmSecrets?: boolean }) =>
    request<JobRef>("PATCH", c(name), body),
  setAccess: (name: string, usernames: string[]) => request<void>("PUT", `${c(name)}/access`, { usernames }),
  processes: (name: string) => request<Processes>("GET", `${c(name)}/processes`),
  tools: (name: string) => request<Tool[]>("GET", `${c(name)}/tools`),
  activity: (name: string) => request<ActivityEvent>("GET", `${c(name)}/activity`),
  stats: (name: string) => request<StatsHistory>("GET", `${c(name)}/stats`),
  folder: (name: string) => request<FolderSize>("GET", `${c(name)}/folder`),
  imageScan: (name: string) => request<ImageScan>("GET", `${c(name)}/image-scan`),
  imageFromDesktop: (name: string, body: ImageFromDesktop) => request<JobRef>("POST", `${c(name)}/image`, body),
  files: (name: string, path: string) => request<FileListing>("GET", `${c(name)}/files?path=${enc(path)}`),
  /** A file, or a folder as a zip; the browser downloads it. */
  downloadUrl: (name: string, path: string) => `${c(name)}/download?path=${enc(path)}`,
  keys: (name: string, keys: string) => request<void>("POST", `${c(name)}/keys`, { keys }),
  /**
   * This page's mic or camera badge: tab is the frame's tab id, as on its
   * stream; seq numbers the page's reports, so a late one never undoes a newer.
   */
  capture: (name: string, kind: "mic" | "cam", on: boolean, tab: string, seq: number) =>
    request<void>("POST", `${c(name)}/${kind}`, { on, tab, seq }),
  /** `confirmSecrets`: the admin confirmed that the desktop's accounts may use the secrets of the image it moves onto. */
  upgrade: (name: string, confirmSecrets = false) => request<JobRef>("POST", `${c(name)}/upgrade`, confirmSecrets ? { confirmSecrets } : undefined),
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
  installImage: (ref: string) => request<JobRef>("POST", "/api/images/install", { ref }),
  removeImage: (ref: string) => request<void>("DELETE", `/api/images?ref=${enc(ref)}`),
  checkImages: () => request<JobRef>("POST", "/api/images/check"),
  upgradeImage: (ref: string, confirmSecrets = false) => request<JobRef>("POST", "/api/images/upgrade", { ref, confirmSecrets }),

  recipes: () => request<Recipe[]>("GET", "/api/recipes"),
  createRecipe: (recipe: Recipe) => request<JobRef>("POST", "/api/recipes", recipe),
  updateRecipe: (recipe: Recipe) => request<JobRef>("PUT", `/api/recipes/${enc(recipe.slug)}`, recipe),
  removeRecipe: (slug: string) => request<void>("DELETE", `/api/recipes/${enc(slug)}`),
  rebuildRecipe: (slug: string) => request<JobRef>("POST", `/api/recipes/${enc(slug)}/rebuild`),
  cancelJob: (id: string) => request<void>("POST", `/api/jobs/${enc(id)}/cancel`),
};

/** Uploads one file into the desktop's ~/Desktop, reporting progress as 0..1. */
export function uploadFile(desktop: string, file: File, onProgress: (fraction: number) => void): Promise<Uploaded> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${c(desktop)}/upload?name=${enc(file.name)}`);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && e.total > 0) onProgress(e.loaded / e.total);
    };
    xhr.onerror = () => reject(new ApiError(0, "The upload was interrupted"));
    xhr.onload = () => {
      if (xhr.status === 401) {
        goToLogin();
        reject(new ApiError(401, "Signed out"));
        return;
      }
      let data: unknown;
      try {
        data = JSON.parse(xhr.responseText);
      } catch {
        // not JSON
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(data as Uploaded);
        return;
      }
      const message = data && typeof data === "object" && "error" in data && typeof data.error === "string" ? data.error : `${xhr.status} ${xhr.statusText}`;
      reject(new ApiError(xhr.status, message));
    };
    xhr.send(file);
  });
}

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
