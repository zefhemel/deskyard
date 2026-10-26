import type { VolumeGrant } from "../api.ts";

/** The supervisor's limit on an environment's grants. */
export const MAX_GRANTS = 16;

/** Whether another grant fits: under the limit, with a volume not yet granted. */
export function canAddGrant(grants: VolumeGrant[], volumes: { name: string }[]): boolean {
  return grants.length < MAX_GRANTS && volumes.some((v) => !grants.some((g) => g.name === v.name));
}

/** Adds a read-write grant of the first volume not yet granted. */
export function addGrant(grants: VolumeGrant[], volumes: { name: string }[]): VolumeGrant[] {
  const next = volumes.find((v) => !grants.some((g) => g.name === v.name));
  return next ? [...grants, { name: next.name, readOnly: false }] : grants;
}

export function updateGrant(grants: VolumeGrant[], index: number, change: Partial<VolumeGrant>): VolumeGrant[] {
  return grants.map((grant, i) => (i === index ? { ...grant, ...change } : grant));
}

export function removeGrant(grants: VolumeGrant[], index: number): VolumeGrant[] {
  return grants.filter((_, i) => i !== index);
}
