import type { EnvironmentVPNSettings, LANException, VPNInput, VPNStatus, VPNUpdate, VPNReference, Job, Settings, SettingsPatch, HostMount, VolumeGrant } from "../api.ts";

export interface ConnectionForm {
  name: string;
  country: string;
  username: string;
  password: string;
}
export interface ExceptionRow {
  destination: string;
  protocol: string;
  ports: string;
}

export function connectionInput(form: ConnectionForm, editing: boolean): VPNInput {
  if (!form.name.trim()) throw new Error("Give this connection a name.");
  if (!form.country) throw new Error("Choose a country.");
  if (!editing && (!form.username.trim() || !form.password.trim())) throw new Error("Enter both NordVPN service credentials.");
  return {
    name: form.name.trim(),
    provider: "nordvpn",
    country: form.country,
    ...(form.username.trim() ? { username: form.username.trim() } : {}),
    ...(form.password.trim() ? { password: form.password } : {}),
  };
}

export function connectionUpdate(form: ConnectionForm, preview: { revision: number; environments: VPNReference[] }): VPNUpdate {
  return {
    ...connectionInput(form, true),
    expectedRevision: preview.revision,
    affectedEnvironments: preview.environments.map((d) => d.name),
    confirmRestart: preview.environments.length > 0,
  };
}

export function environmentVPNPatch(original: EnvironmentVPNSettings, selection: string, rules: LANException[]): Partial<EnvironmentVPNSettings> {
  const id = selection || null;
  return {
    ...(id !== original.vpnConnectionId ? { vpnConnectionId: id } : {}),
    ...(exceptionPolicyKey(rules) !== exceptionPolicyKey(original.lanExceptions) ? { lanExceptions: rules } : {}),
  };
}

/** Preliminary form validation; the server also checks reserved networks and effective combined rules. */
export function parseExceptions(rows: ExceptionRow[]): LANException[] {
  return rows.map((row, i) => {
    const fail = (message: string): never => {
      throw new Error(`LAN exception ${i + 1}: ${message}`);
    };
    const match = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})(?:\/(\d{1,2}))?$/.exec(row.destination.trim());
    if (!match) return fail("use a private IPv4 address or CIDR.");
    const octets = match.slice(1, 5).map(Number);
    const bits = match[5] === undefined ? 32 : Number(match[5]);
    const [a = 0, b = 0] = octets;
    const minBits = a === 10 ? 8 : a === 172 && b >= 16 && b <= 31 ? 12 : a === 192 && b === 168 ? 16 : 33;
    if (octets.some((n) => n > 255) || bits < minBits || bits > 32) return fail("only private IPv4 destinations are allowed.");
    const ip = octets.reduce((n, o) => n * 256 + o, 0);
    const network = Math.floor(ip / 2 ** (32 - bits)) * 2 ** (32 - bits);
    const destination = [24, 16, 8, 0].map((shift) => Math.floor(network / 2 ** shift) % 256).join(".") + `/${bits}`;
    if (row.protocol !== "tcp" && row.protocol !== "udp") return fail("choose TCP or UDP.");
    const ports = row.ports.split(",").map((text) => {
      const range = /^(\d+)(?:\s*-\s*(\d+))?$/.exec(text.trim());
      if (!range) return fail("enter ports such as 443, 8000-8002.");
      const start = Number(range[1]),
        end = Number(range[2] ?? range[1]);
      if (start < 1 || end > 65535 || start > end || (start === 1 && end === 65535))
        return fail("ports must be 1–65535, with ascending ranges; all-port rules are not allowed.");
      return { start, end };
    });
    return { destination, protocol: row.protocol, ports };
  });
}

export function exceptionRows(rules: LANException[]): ExceptionRow[] {
  return rules.map((rule) => ({ ...rule, ports: rule.ports.map((p) => (p.end && p.end !== p.start ? `${p.start}-${p.end}` : `${p.start}`)).join(", ") }));
}

export function vpnPresentation(vpn: VPNStatus) {
  const directTransition = !vpn.connectionId && vpn.state === "blocked";
  const labels = {
    disabled: "No VPN",
    stopped: "VPN stopped",
    connecting: "VPN connecting",
    connected: "VPN connected",
    reconnecting: "VPN reconnecting",
    blocked: "VPN blocked",
  };
  const detail = [
    vpn.reason,
    vpn.idle ? "Idle: the gateway stays running and may use a provider connection." : undefined,
    !directTransition && vpn.desiredRevision !== vpn.appliedRevision
      ? `Selected revision ${vpn.desiredRevision}; applied revision ${vpn.appliedRevision}. Retry start or restart to apply the selected connection.`
      : undefined,
  ]
    .filter(Boolean)
    .join(" ");
  const observed = vpn.exitIpObservedAt && !vpn.exitIpObservedAt.startsWith("0001-") && !Number.isNaN(Date.parse(vpn.exitIpObservedAt));
  return {
    label: directTransition ? "Network transition interrupted" : vpn.state === "stopped" && vpn.idle ? "VPN idle" : labels[vpn.state],
    notice: directTransition
      ? "The network transition has not finished. Recovery will retry your selected network settings."
      : ["blocked", "connecting", "reconnecting"].includes(vpn.state)
        ? "Internet access stays blocked until the VPN connects."
        : undefined,
    detail,
    currentIP: vpn.state === "connected" ? vpn.currentExitIp : undefined,
    history:
      vpn.state !== "connected" && vpn.lastExitIp && observed
        ? `Last observed exit IP ${vpn.lastExitIp} at ${new Date(vpn.exitIpObservedAt).toLocaleString()}`
        : undefined,
  };
}

/** Candidate result provenance shared by the test form and its job renderer. */
export class CandidateTestSession {
  private submittedForm = "";
  start<T>(form: ConnectionForm, editing: boolean, begin: (input: VPNInput) => Promise<T>): Promise<T> {
    const input = connectionInput(form, editing);
    this.submittedForm = JSON.stringify(form);
    return begin(input);
  }
  result(form: ConnectionForm, job: Job | null): Job | null {
    return this.submittedForm === JSON.stringify(form) ? job : null;
  }
}

export interface EnvironmentSettingsDraft {
  dockerAccess?: boolean;
  image: string;
  memoryBytes: number;
  nanoCpus: number;
  vpnConnectionId: string;
  lan: ExceptionRow[];
  mounts?: HostMount[];
  volumes?: VolumeGrant[];
}
type SavedEnvironmentSettings = { image: string; settings: Settings };
function environmentSettingsPatch(saved: SavedEnvironmentSettings, draft: EnvironmentSettingsDraft): SettingsPatch {
  const rules =
    !draft.vpnConnectionId || JSON.stringify(draft.lan) === JSON.stringify(exceptionRows(saved.settings.lanExceptions))
      ? saved.settings.lanExceptions
      : parseExceptions(draft.lan);
  const patch: SettingsPatch = environmentVPNPatch(saved.settings, draft.vpnConnectionId, rules);
  if (draft.image !== saved.image) patch.image = draft.image;
  if (draft.memoryBytes !== saved.settings.memoryBytes) patch.memoryBytes = draft.memoryBytes;
  if (draft.nanoCpus !== saved.settings.nanoCpus) patch.nanoCpus = draft.nanoCpus;
  if (draft.dockerAccess !== undefined && draft.dockerAccess !== (saved.settings.dockerAccess ?? false)) patch.dockerAccess = draft.dockerAccess;
  if (draft.mounts !== undefined && JSON.stringify(draft.mounts) !== JSON.stringify(saved.settings.mounts ?? [])) patch.mounts = draft.mounts;
  if (draft.volumes !== undefined && JSON.stringify(draft.volumes) !== JSON.stringify(saved.settings.volumes ?? [])) patch.volumes = draft.volumes;
  return patch;
}
export function environmentSettingsChanged(saved: SavedEnvironmentSettings, draft: EnvironmentSettingsDraft): boolean {
  try {
    return Object.keys(environmentSettingsPatch(saved, draft)).length > 0;
  } catch {
    return true;
  } // An invalid visible draft still needs correction or Undo.
}
export function saveEnvironmentSettings<T>(
  saved: SavedEnvironmentSettings,
  draft: EnvironmentSettingsDraft,
  begin: (patch: SettingsPatch) => Promise<T>,
): Promise<T> | null {
  const patch = environmentSettingsPatch(saved, draft);
  return Object.keys(patch).length ? begin(patch) : null;
}

/** Compare policy rather than editable spelling, rule order or split/duplicate ports. */
function exceptionPolicyKey(rules: LANException[]): string {
  const grouped = new Map<string, LANException>();
  for (const rule of parseExceptions(exceptionRows(rules))) {
    const key = `${rule.destination}:${rule.protocol}`;
    const existing = grouped.get(key);
    if (existing) existing.ports.push(...rule.ports);
    else grouped.set(key, rule);
  }
  return JSON.stringify(
    [...grouped.entries()]
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([, rule]) => {
        const ports: LANException["ports"] = [];
        for (const port of rule.ports.sort((a, b) => a.start - b.start || a.end - b.end)) {
          const last = ports.at(-1);
          if (last && port.start <= last.end + 1) last.end = Math.max(last.end, port.end);
          else ports.push({ ...port });
        }
        return { ...rule, ports };
      }),
  );
}
