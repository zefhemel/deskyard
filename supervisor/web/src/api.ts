// Typed client for the supervisor's HTTP API (supervisor/API.md).

export interface Me {
  username: string;
  admin: boolean;
  /** token: an account without a password, which signs in through login links. */
  kind: "password" | "token";
  /** Made by Share on a computer; shares can't share. */
  share: boolean;
}

/** What an account may do on a computer: everything, or watch its desktop. Admins are full everywhere. */
export type Level = "full" | "observe";
/** An account's grant of a computer. */
export interface Grant {
  computer: string;
  level: Level;
}
/** A computer's grant to an account. */
export interface AccessGrant {
  username: string;
  level: Level;
}
/** The account that made a share. */
export interface Sharer {
  username: string;
  id: string;
}

export interface Host {
  /** Result of the supervisor's complete startup capability probe. */
  memoryReclaim: { enabled: boolean; reason?: string };
  cpus: number;
  memBytes: number;
  running: number;
  total: number;
  memUsed: number;
}

export type ComputerState = "running" | "stopped" | "starting" | "creating" | "upgrading" | "removing";

export interface Stats {
  cpuPercent: number;
  memBytes: number;
  memLimit: number;
}


/** A volume granted to a computer, mounted at path (by default /share/<name>). */
export interface VolumeGrant {
  name: string;
  path: string;
  readOnly: boolean;
}
/** A named volume; computers are those whose definitions grant it. */
export interface Volume {
  name: string;
  created: string;
  kind: "local" | "host";
  /** A host volume's folder on the engine host. */
  hostPath?: string;
  folder: string;
  computers: VolumeGrant[];
}
/** The size of a volume's files, and the disk holding it (absent when unknown). */
export interface VolumeUsage {
  bytes: number;
  /** False when some folders couldn't be read, so bytes is a lower bound. */
  complete: boolean;
  diskFree?: number;
  diskSize?: number;
}

export interface VPNReference { id: string; name: string }
export interface VPNConnection {
  id: string; name: string; provider: "nordvpn"; country: string;
  revision: number; credentialsConfigured: boolean;
}
/** A computer using a connection; its id is its name. */
export interface SavedVPNConnection extends VPNConnection { computers: VPNReference[] }
export interface VPNInput { name: string; provider: "nordvpn"; country: string; username?: string; password?: string }
export interface VPNUpdate extends VPNInput { expectedRevision: number; affectedComputers: string[]; confirmRestart: boolean }
export interface LANException { destination: string; protocol: "tcp" | "udp"; ports: {start: number; end: number}[] }
export interface VPNStatus {
  state: "disabled" | "stopped" | "connecting" | "connected" | "reconnecting" | "blocked";
  reason?: string; changedAt: string; currentExitIp?: string; lastExitIp?: string; exitIpObservedAt: string;
  connectionId?: string; name?: string; provider?: "nordvpn"; country?: string;
  desiredRevision: number; appliedRevision: number; idle: boolean;
}
export interface VPNRollout { connection: VPNConnection; computers: (VPNReference & {error?: string})[] }
export interface ComputerVPNSettings { vpnConnectionId: string | null; lanExceptions: LANException[] }
export interface SettingsPatch extends Partial<ComputerVPNSettings> {
  image?: string; memoryBytes?: number; nanoCpus?: number; dockerAccess?: boolean; volumes?: VolumeGrant[];
}

export interface Settings extends ComputerVPNSettings {
  dockerAccess: boolean;
  volumes: VolumeGrant[];
  memoryBytes: number;
  nanoCpus: number;
}

/** A shared terminal session (a tmux session) in a running computer. */
export interface TerminalSession {
  name: string;
  /** Attached clients: native terminals, the CLI and browsers, this page included. */
  clients: number;
  /** The foreground command of its active pane, e.g. "bash" or "vim". */
  command: string;
}

export interface Computer {
  /** Image supports named shared terminals. */
  terminal: boolean;
  /** Its shared terminal sessions: empty when stopped or without terminal support. */
  terminals: TerminalSession[];
  /** Reclaim memory automatically while nobody is viewing; saves without restarting. */
  memoryReclaim: boolean;
  /** Its home and local volumes are served over WebDAV at /<name>/dav/. */
  webdav: boolean;
  /** The computer's identity everywhere: there is no separate ID. */
  name: string;
  /** Observed; power is the desired state. */
  state: ComputerState;
  power: "running" | "stopped";
  /** The definition's revision, counting container-affecting changes. */
  revision: number;
  /** The revision the container was built from; 0 without a container. */
  appliedRevision: number;
  /** Why the last change couldn't be applied; "" when it could. */
  error: string;
  /** Why a container claiming this name isn't matched to it; "" when none. */
  conflict: string;
  startedAt: string;
  created: string | null;
  /** The running job (create, upgrade, settings change), if any. */
  job: string | null;
  folder: string;
  image: string;
  imageTitle: string;
  imageVersion: string;
  updateAvailable: string | null;
  settings: Settings;
  vpn: VPNStatus;
  /** The signed-in account's level here. An observer's computer has its configuration blanked. */
  level: Level;
  /** The regular accounts granted it (admins only; shares are listed by its shares). */
  access: AccessGrant[];
  viewers: string[];
  /** The viewers watching at observe level. */
  observers: string[];
  /** Viewers with a mic on (per stream; gone when the stream closes). */
  mics: string[];
  /** Port mappings agents have set up with `deskyard forward`; port is the computer's. */
  forwards: Forward[];
  /** Agents at work in the computer: from outside (a CLI credential) or inside (a program). */
  agents: AgentPresence[];
  stats: Stats | null;
}

/**
 * A container of this supervisor's that it can't match to a definition:
 * one claiming an undefined name, several claiming one, or one claiming it
 * under another container name. Only admins are told.
 */
export interface Conflict {
  name: string;
  kind: "unknown" | "duplicate" | "misnamed";
  containers: string[];
  message: string;
}

export interface ComputerList {
  computers: Computer[];
  conflicts: Conflict[];
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
  /** The folder the computer's files would live in (when the name is valid). */
  folder?: string;
}

export interface Job {
  status: "running" | "done" | "failed";
  message: string;
  progress: number | null;
  error?: string;
  computer?: string;
  result?: VPNStatus | VPNRollout | RestoreResult;
}

export interface Account {
  username: string;
  kind: "password" | "token";
  admin: boolean;
  description: string;
  grants: Grant[];
  /** Set on shares only. */
  sharedBy: Sharer | null;
  expires: string | null;
  /** It has an unused login link. */
  linkPending: boolean;
  created: string;
  lastLogin: string | null;
}

export interface AccountChange {
  password?: string;
  admin?: boolean;
  description?: string;
  grants?: Grant[];
  expires?: string | null;
}

/** A token account with one grant, made by Share on a computer. */
export interface Share {
  username: string;
  computer: string;
  level: Level;
  sharedBy: Sharer;
  expires: string | null;
  created: string;
  /** The last redemption of a link. */
  lastLogin: string | null;
  /** The last redemption, CLI request or browser request (since the server started). */
  lastUsed: string | null;
  linkPending: boolean;
  /** It has a desktop, terminal or CLI connection open on the computer now. */
  connected: boolean;
}

/** What a login link signs in as, before it is used. */
export interface LinkInfo {
  username: string;
  description: string;
  grants: Grant[];
  sharedBy: Sharer | null;
  expires: string | null;
}

/** A signed-in client of an account: a CLI, or the account's WebDAV password. */
export interface Credential {
  id: string;
  kind: "cli" | "webdav";
  name: string;
  created: string;
  lastUsed: string | null;
  /** The credential making this request. */
  current: boolean;
}

/** A pending `deskyard login`, for its approval page. */
export interface CliLoginRequest {
  client: string;
  ip: string;
  code: string;
  created: string;
}

export interface CatalogImage {
  installed: boolean;
  official: boolean;
  ref: string;
  title: string;
  version: string;
  description: string;
  computers: string[];
  behind: string[];
  checked: string | null;
  /** Bytes on disk, once downloaded. */
  size?: number;
  /** When the image was built, once downloaded. */
  built?: string;
}

export interface FolderSize {
  path: string;
  bytes: number;
  /** False when some directories couldn't be read, so bytes is a lower bound. */
  complete: boolean;
  /** The disk holding the folder (absent when unknown). */
  diskFree?: number;
  diskSize?: number;
}

export interface Uploaded {
  path: string;
  bytes: number;
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
  /** The name of the client credential that set it up (its wire name is token). */
  token: string;
  /** in: this machine's port appears in the computer; out: the computer's port appears there. */
  direction: "in" | "out";
  port: number;
}
export interface ViewersEvent {
  name: string;
  viewers: string[];
  observers: string[];
  mics: string[];
  forwards: Forward[];
  agents: AgentPresence[];
}

// Agent activity: who is at work in a computer and what they did in the last 15 minutes.
export interface AgentPresence {
  id: string;
  name: string;
  /** The credential's account; empty for agents inside the computer. */
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
/** A screen action an agent made, from GET /api/computers/{name}/screen-events. */
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
    /** The computers a VPN or volume conflict names. */
    readonly computers: VPNReference[] = [],
    /** What a storage conflict is: needsKey, wrongKey, inUse or busy. */
    readonly code = "",
  ) {
    super(message);
  }
}

/**
 * Sends the browser to the login page, coming back here afterwards. The
 * fragment comes along (the CLI login page's request id is in it), except on
 * the link page, whose fragment is a secret that mustn't reach a query string.
 */
export function goToLogin(): void {
  if (location.pathname === "/login") return;
  const next = location.pathname + location.search + (location.pathname === "/link" ? "" : location.hash);
  location.href = `/login?next=${encodeURIComponent(next)}`;
}

/** Routes that answer 401 without meaning "sign in first". */
const no401Redirect = new Set(["/api/login", "/api/links/inspect", "/api/links/redeem"]);

async function request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const init: RequestInit = { method, credentials: "same-origin", headers: {}, signal };
  if (body !== undefined) {
    init.body = JSON.stringify(body);
    init.headers = { "Content-Type": "application/json" };
  }
  const res = await fetch(path, init);
  if (res.status === 401 && !no401Redirect.has(path)) {
    goToLogin();
    throw new ApiError(401, "Signed out");
  }
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`;
    let computers: VPNReference[] = [];
    let code = "";
    try {
      const data: unknown = await res.json();
      if (data && typeof data === "object") {
        if ("error" in data && typeof data.error === "string") message = data.error;
        if ("computers" in data && Array.isArray(data.computers)) computers = data.computers as VPNReference[];
        if ("code" in data && typeof data.code === "string") code = data.code;
      }
    } catch {
      // not JSON
    }
    throw new ApiError(res.status, message, computers, code);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

const enc = encodeURIComponent;
const c = (name: string) => `/api/computers/${enc(name)}`;

export interface MemoryReclaim {
  requestedBytes: number;
  beforeBytes: number;
  afterBytes: number;
  swapBeforeBytes: number;
  swapAfterBytes: number;
  partial: boolean;
}

export type RemoteType = "s3" | "b2" | "sftp" | "webdav";

/** A storage remote: an rclone connection, without its secrets. */
export interface Remote {
  name: string;
  type: RemoteType;
  created: string;
  root?: string;
  provider?: string;
  endpoint?: string;
  region?: string;
  accessKeyId?: string;
  keyId?: string;
  host?: string;
  port?: number;
  user?: string;
  hostKey?: string;
  url?: string;
  vendor?: string;
  /** Which secrets are stored. */
  secretsSet: string[];
  /** The backup repositories on it. */
  repositories: string[];
}
export type RemoteInput = Omit<Remote, "created" | "secretsSet" | "repositories">;
/** Write-only: left out to keep the stored value. */
export interface RemoteSecrets {
  secretAccessKey?: string;
  applicationKey?: string;
  password?: string;
  privateKey?: string;
}
export interface Repository {
  name: string;
  remote: string;
  folder: string;
  created: string;
}
export interface Schedule {
  every: "hourly" | "daily" | "weekly";
  at: string;
  weekday: number;
}
export interface Retention {
  last: number;
  hourly: number;
  daily: number;
  weekly: number;
  monthly: number;
  yearly: number;
}
export interface PolicyInput {
  repository: string;
  enabled: boolean;
  schedule: Schedule;
  retention: Retention;
}
export interface Policy extends PolicyInput {
  item: string;
  updated: string;
}
export interface RunState {
  lastAttempt: string;
  lastSuccess: string;
  snapshot?: string;
  error?: string;
  /** Files the last snapshot left out because they couldn't be read. */
  warnings?: string[];
}
export type ItemKind = "computer" | "volume";
/** A computer's or volume's backups: its policy (null without one), last run and what it's doing. */
export interface BackupItem {
  item: string;
  policy: Policy | null;
  state: RunState;
  activity: string;
  nextRun: string | null;
}
export interface Snapshot {
  id: string;
  short_id: string;
  time: string;
  hostname: string;
  tags: string[];
  summary?: { data_added: number; total_bytes_processed: number };
  repository: string;
  reason: string;
}
export interface SnapshotEntry {
  name: string;
  path: string;
  type: string;
  size: number;
  mtime: string;
}
export interface CatalogItem {
  repository: string;
  host: string;
  item: string;
  snapshots: number;
  latest: string;
  latestId: string;
  local: boolean;
}
export interface BackupsOverview {
  repositories: Repository[];
  items: BackupItem[];
  host: string;
  catalog?: CatalogItem[];
  catalogErrors?: Record<string, string>;
}
export interface RestoreRequest {
  snapshot: string;
  repository?: string;
  mode: "here" | "new" | "folder";
  newName?: string;
  newImage?: string;
  backupFirst?: boolean;
  paths?: string[];
}
export interface RestoreResult {
  created?: string;
  dropped?: string[];
  folder?: string;
}

const b = (kind: ItemKind, name: string) => `/api/backups/${kind}/${enc(name)}`;

export const api = {
  login: (username: string, password: string) => request<Me>("POST", "/api/login", { username, password }),
  logout: () => request<void>("POST", "/api/logout"),
  me: () => request<Me>("GET", "/api/me"),
  /** The signed-in account, or null when signed out (without going to the login page). */
  whoami: async (): Promise<Me | null> => {
    const res = await fetch("/api/me", { credentials: "same-origin" });
    if (res.status === 401) return null;
    if (!res.ok) throw new ApiError(res.status, `${res.status} ${res.statusText}`);
    return (await res.json()) as Me;
  },
  changePassword: (current: string, next: string) => request<void>("POST", "/api/me/password", { current, new: next }),
  /** Ends every browser session of the account; this one is re-issued. */
  signOutEverywhere: () => request<void>("POST", "/api/me/sign-out-everywhere"),
  credentials: () => request<Credential[]>("GET", "/api/me/credentials"),
  revokeCredential: (id: string) => request<void>("DELETE", `/api/me/credentials/${enc(id)}`),
  /** Makes the account's WebDAV password (replacing any), shown only in this answer. */
  createWebDAVPassword: () => request<{ password: string }>("POST", "/api/me/webdav-password"),
  /** Removes the WebDAV password; WebDAV takes the account password again. */
  removeWebDAVPassword: () => request<void>("DELETE", "/api/me/webdav-password"),
  host: () => request<Host>("GET", "/api/host"),

  computers: () => request<ComputerList>("GET", "/api/computers"),
  computer: (name: string) => request<Computer>("GET", c(name)),
  terminalSessions: (name: string, signal?: AbortSignal) =>
    request<{ sessions: TerminalSession[] }>("GET", `${c(name)}/terminal/sessions`, undefined, signal),
  /** Starts the next free terminal-N, detached; the tab then opens it. */
  createTerminal: (name: string) => request<{ name: string }>("POST", `${c(name)}/terminal/sessions`),
  /** Ends a session for everyone attached to it. */
  renameTerminal: (name: string, session: string, next: string) => request<{ name: string }>("PATCH", `${c(name)}/terminal/sessions/${enc(session)}`, { name: next }),

  endTerminal: (name: string, session: string) => request<void>("DELETE", `${c(name)}/terminal/sessions/${enc(session)}`),
  createComputer: (body: { name: string; image: string; memoryBytes: number; nanoCpus: number; access: AccessGrant[]; dockerAccess: boolean; volumes?: VolumeGrant[] } & ComputerVPNSettings) =>
    request<JobRef>("POST", "/api/computers", body),
  power: (name: string, action: "start" | "stop" | "restart") => request<void>("POST", `${c(name)}/${action}`),
  setMemoryPolicy: (name: string, memoryReclaim: boolean) => request<void>("PUT", `${c(name)}/memory-policy`, { memoryReclaim }),
  setWebDAV: (name: string, webdav: boolean) => request<void>("PUT", `${c(name)}/webdav`, { webdav }),
  reclaimMemory: (name: string) => request<MemoryReclaim>("POST", `${c(name)}/reclaim-memory`),
  removeComputer: (name: string, files: boolean) => request<void>("DELETE", c(name) + (files ? "?files=1" : "")),
  updateSettings: (name: string, body: SettingsPatch) =>
    request<JobRef>("PATCH", c(name), body),
  setAccess: (name: string, grants: AccessGrant[]) => request<void>("PUT", `${c(name)}/access`, { grants }),
  processes: (name: string) => request<Processes>("GET", `${c(name)}/processes`),
  activity: (name: string) => request<ActivityEvent>("GET", `${c(name)}/activity`),
  stats: (name: string) => request<StatsHistory>("GET", `${c(name)}/stats`),
  folder: (name: string) => request<FolderSize>("GET", `${c(name)}/folder`),
  keys: (name: string, keys: string) => request<void>("POST", `${c(name)}/keys`, { keys }),
  /**
   * This page's mic badge: tab is the frame's tab id, as on its stream; seq
   * numbers the page's reports, so a late one never undoes a newer.
   */
  capture: (name: string, kind: "mic", on: boolean, tab: string, seq: number) =>
    request<void>("POST", `${c(name)}/${kind}`, { on, tab, seq }),
  upgrade: (name: string) => request<JobRef>("POST", `${c(name)}/upgrade`),
  validateName: (name: string) => request<NameCheck>("GET", `/api/validate-name?name=${enc(name)}`),
  screenUrl: (name: string, opts: { full?: boolean; bust?: number } = {}) => {
    const q = new URLSearchParams();
    if (opts.full) q.set("full", "1");
    if (opts.bust !== undefined) q.set("t", String(opts.bust));
    const qs = q.toString();
    return `${c(name)}/screen.png${qs ? `?${qs}` : ""}`;
  },

  vpnConnections: () => request<{connections: SavedVPNConnection[]; countries: string[]}>("GET", "/api/vpn/connections"),
  createVPN: (body: VPNInput) => request<VPNConnection>("POST", "/api/vpn/connections", body),
  updateVPN: (id: string, body: VPNUpdate) => request<JobRef>("PUT", `/api/vpn/connections/${enc(id)}`, body),
  deleteVPN: (id: string) => request<void>("DELETE", `/api/vpn/connections/${enc(id)}`),
  testVPN: (body: VPNInput & {connectionId?: string}) => request<JobRef>("POST", "/api/vpn/test", body),

  accounts: () => request<Account[]>("GET", "/api/accounts"),
  /** Without a password it is a token account, which signs in through a login link. */
  createAccount: (body: AccountChange & { username: string }) => request<Account>("POST", "/api/accounts", body),
  updateAccount: (username: string, body: AccountChange) => request<Account>("PATCH", `/api/accounts/${enc(username)}`, body),
  deleteAccount: (username: string) => request<void>("DELETE", `/api/accounts/${enc(username)}`),
  /** A new login link for a token account, replacing its unused one. */
  accountLink: (username: string) => request<{ link: string }>("POST", `/api/accounts/${enc(username)}/link`),
  accountCredentials: (username: string) => request<Credential[]>("GET", `/api/accounts/${enc(username)}/credentials`),
  revokeAccountCredential: (username: string, id: string) =>
    request<void>("DELETE", `/api/accounts/${enc(username)}/credentials/${enc(id)}`),

  shares: (name: string) => request<Share[]>("GET", `${c(name)}/shares`),
  /** The link is only ever shown here. */
  createShare: (name: string, body: { username: string; level: Level; expires: string | null }) =>
    request<{ share: Share; link: string }>("POST", `${c(name)}/shares`, body),
  newShareLink: (username: string) => request<{ link: string }>("POST", `/api/shares/${enc(username)}/link`),
  revokeShare: (username: string) => request<void>("DELETE", `/api/shares/${enc(username)}`),

  /** What a link's code signs in as; uses nothing up. */
  inspectLink: (link: string) => request<LinkInfo>("POST", "/api/links/inspect", { link }),
  /** Uses the link up and signs this browser in as its account. */
  redeemLink: (link: string) => request<{ username: string; next: string }>("POST", "/api/links/redeem", { link, client: "browser" }),

  cliLogin: (id: string) => request<CliLoginRequest>("GET", `/api/cli-login/${enc(id)}`),
  approveCliLogin: (id: string) => request<void>("POST", `/api/cli-login/${enc(id)}/approve`),
  denyCliLogin: (id: string) => request<void>("POST", `/api/cli-login/${enc(id)}/deny`),

  images: () => request<CatalogImage[]>("GET", "/api/images"),
  addImage: (ref: string) => request<JobRef>("POST", "/api/images", { ref }),
  installImage: (ref: string) => request<JobRef>("POST", "/api/images/install", { ref }),
  removeImage: (ref: string) => request<void>("DELETE", `/api/images?ref=${enc(ref)}`),
  checkImages: () => request<JobRef>("POST", "/api/images/check"),
  upgradeImage: (ref: string) => request<JobRef>("POST", "/api/images/upgrade", { ref }),

  volumes: () => request<{ volumes: Volume[] }>("GET", "/api/volumes"),
  createVolume: (name: string, hostPath?: string) => request<{ volume: Volume; adopted: boolean }>("POST", "/api/volumes", hostPath ? { name, hostPath } : { name }),
  volumeUsage: (name: string) => request<VolumeUsage>("GET", `/api/volumes/${enc(name)}/usage`),
  deleteVolume: (name: string, deleteFiles: boolean) => request<void>("DELETE", `/api/volumes/${enc(name)}?deleteFiles=${deleteFiles}`),

  remotes: () => request<{ remotes: Remote[]; s3Providers: string[]; webdavVendors: string[] }>("GET", "/api/remotes"),
  createRemote: (remote: RemoteInput, secrets: RemoteSecrets) => request<Remote>("POST", "/api/remotes", { remote, secrets }),
  updateRemote: (name: string, remote: RemoteInput, secrets: RemoteSecrets) => request<Remote>("PUT", `/api/remotes/${enc(name)}`, { remote, secrets }),
  deleteRemote: (name: string) => request<void>("DELETE", `/api/remotes/${enc(name)}`),
  testRemote: (remote: RemoteInput, secrets: RemoteSecrets, saved?: string) =>
    request<void>("POST", saved ? `/api/remotes/${enc(saved)}/test` : "/api/remotes/test", { remote, secrets }),
  backups: (catalog = false) => request<BackupsOverview>("GET", `/api/backups${catalog ? "?catalog=1" : ""}`),
  createRepository: (body: { name: string; remote: string; folder: string; recoveryKey?: string }) =>
    request<{ repository: Repository; recoveryKey?: string }>("POST", "/api/backups/repositories", body),
  removeRepository: (name: string) => request<void>("DELETE", `/api/backups/repositories/${enc(name)}`),
  recoveryKey: (name: string) => request<{ recoveryKey: string }>("GET", `/api/backups/repositories/${enc(name)}/recovery-key`),
  backup: (kind: ItemKind, name: string) => request<BackupItem>("GET", b(kind, name)),
  putPolicy: (kind: ItemKind, name: string, policy: PolicyInput) => request<BackupItem>("PUT", b(kind, name), policy),
  deletePolicy: (kind: ItemKind, name: string) => request<void>("DELETE", b(kind, name)),
  runBackup: (kind: ItemKind, name: string) => request<JobRef>("POST", `${b(kind, name)}/run`),
  snapshots: (kind: ItemKind, name: string) => request<{ snapshots: Snapshot[] }>("GET", `${b(kind, name)}/snapshots`),
  snapshotFiles: (kind: ItemKind, name: string, repository: string, id: string, path: string) =>
    request<{ entries: SnapshotEntry[] }>("GET", `${b(kind, name)}/snapshots/${enc(id)}/files?repository=${enc(repository)}&path=${enc(path)}`),
  snapshotDownloadURL: (kind: ItemKind, name: string, repository: string, id: string, path: string, dir: boolean) =>
    `${b(kind, name)}/snapshots/${enc(id)}/download?repository=${enc(repository)}&path=${enc(path)}${dir ? "&dir=1" : ""}`,
  restore: (kind: ItemKind, name: string, body: RestoreRequest) => request<JobRef>("POST", `${b(kind, name)}/restore`, body),

  cancelJob: (id: string) => request<void>("POST", `/api/jobs/${enc(id)}/cancel`),
};

/** Uploads one file to the destination (default ~/Desktop), reporting progress as 0..1. */
export function uploadFile(computer: string, file: File, onProgress: (fraction: number) => void, destination?: string): Promise<Uploaded> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${c(computer)}/upload?name=${enc(file.name)}${destination ? `&path=${enc(destination)}` : ""}`);
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

