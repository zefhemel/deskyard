import type { VolumeGrant } from "../api.ts";

/** Where a volume is mounted unless its grant names another path. */
export function defaultGrantPath(name: string): string {
  return `/share/${name}`;
}

/** Whether another grant fits: a volume not yet granted remains. */
export function canAddGrant(grants: VolumeGrant[], volumes: { name: string }[]): boolean {
  return volumes.some((v) => !grants.some((g) => g.name === v.name));
}

/** Adds a read-write grant of the first volume not yet granted, at its default path. */
export function addGrant(grants: VolumeGrant[], volumes: { name: string }[]): VolumeGrant[] {
  const next = volumes.find((v) => !grants.some((g) => g.name === v.name));
  return next ? [...grants, { name: next.name, path: defaultGrantPath(next.name), readOnly: false }] : grants;
}

/** Changes one grant. Choosing another volume moves a grant still at its default path along with it. */
export function updateGrant(grants: VolumeGrant[], index: number, change: Partial<VolumeGrant>): VolumeGrant[] {
  return grants.map((grant, i) => {
    if (i !== index) return grant;
    const next = { ...grant, ...change };
    if (change.name !== undefined && change.path === undefined && grant.path === defaultGrantPath(grant.name)) next.path = defaultGrantPath(change.name);
    return next;
  });
}

export function removeGrant(grants: VolumeGrant[], index: number): VolumeGrant[] {
  return grants.filter((_, i) => i !== index);
}

// As the supervisor's ValidateGrantPath: a mount there would replace part of the desktop's runtime.
const SYSTEM_PATHS = ["/bin", "/boot", "/dev", "/etc", "/lib", "/lib32", "/lib64", "/libx32", "/opt", "/proc", "/run", "/sbin", "/sys", "/usr", "/var"];

function within(path: string, dir: string): boolean {
  return path === dir || path.startsWith(dir + "/");
}

function cleanPath(path: string): string {
  const parts: string[] = [];
  for (const part of path.split("/")) {
    if (part === "" || part === ".") continue;
    if (part === "..") parts.pop();
    else parts.push(part);
  }
  return "/" + parts.join("/");
}

/** Why grant `index`'s path can't be used, or null: the supervisor's rules, so the form can say so before saving. */
export function grantPathError(path: string, grants: VolumeGrant[], index: number): string | null {
  if (!path.startsWith("/") || path.includes("\0")) return "Use an absolute path, such as /share/photos.";
  if (cleanPath(path) !== path) return "Use a clean path: no trailing slash, . or ..";
  if (path === "/") return "A volume can't replace the whole filesystem.";
  if (within(path, "/workspace")) return "Not in the home folder /workspace, which the desktop takes over.";
  const sys = SYSTEM_PATHS.find((dir) => within(path, dir));
  if (sys) return `Not in ${sys}, which the desktop runs from.`;
  const other = grants.find((g, i) => i !== index && (within(path, g.path) || within(g.path, path)));
  if (other) return `This path and ${other.name}'s ${other.path} overlap.`;
  return null;
}
