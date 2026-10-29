// Form controls shared by the New computer dialog, the details page's
// Settings and Access tabs and the account dialog.
import type { ComponentChildren } from "preact";
import type { AccessGrant, Account, CatalogImage, Level } from "../api.ts";
import { gb, trim } from "../lib/format.ts";
import { Pick, Picks, type SegOption, Tag, cx } from "./ui.tsx";

const GB = 1024 ** 3;

export function memoryOptions(current: number): SegOption<number>[] {
  const opts: SegOption<number>[] = [{ value: 0, label: "No limit" }, ...[1, 2, 4, 8].map((n) => ({ value: n * GB, label: `${n} GB` }))];
  if (!opts.some((o) => o.value === current)) opts.push({ value: current, label: `${gb(current)} GB` });
  return opts;
}

export function cpuOptions(current: number): SegOption<number>[] {
  const opts: SegOption<number>[] = [{ value: 0, label: "No limit" }, ...[1, 2, 4].map((n) => ({ value: n * 1e9, label: String(n) }))];
  if (!opts.some((o) => o.value === current)) opts.push({ value: current, label: trim(current / 1e9) });
  return opts;
}

export function ImagePick({ images, value, onChange }: { images: CatalogImage[]; value: string; onChange: (ref: string) => void }) {
  if (!images.length) return <p class="text-sm text-muted">The image catalog is empty. Add an image on the Images page first.</p>;
  return (
    <div class="grid gap-2" role="radiogroup">
      {images.map((img) => (
        <label
          key={img.ref}
          class={cx(
            "flex cursor-pointer items-start gap-2.5 rounded-lg border bg-white px-3 py-2.5 text-sm",
            img.ref === value ? "border-accent shadow-[inset_0_0_0_1px_var(--color-accent)]" : "border-line",
          )}
        >
          <input type="radio" name="image" class="mt-[3px]" checked={img.ref === value} onChange={() => onChange(img.ref)} />
          <span class="min-w-0">
            <b class="font-semibold">{img.title || img.ref}</b> <span class="text-muted">{img.version}</span>
            <p class="mt-0.5 text-[13px] text-muted">
              {img.description} <span class="mono [overflow-wrap:anywhere]">{img.ref}</span>
            </p>
            {!img.installed && <p class="mt-0.5 text-[13px] text-muted">Downloads on first use</p>}
          </span>
        </label>
      ))}
    </div>
  );
}

/** A name picked with a level: an account on a computer, or a computer for an account. */
export interface LevelPick {
  name: string;
  level: Level;
}

/**
 * A checkbox per name and, once checked, its level. `children` come first
 * (fixed rows, such as admins); `empty` shows when there are no names.
 */
export function LevelPicks({
  names,
  value,
  onChange,
  empty,
  children,
}: {
  names: string[];
  value: LevelPick[];
  onChange: (picks: LevelPick[]) => void;
  empty: ComponentChildren;
  children?: ComponentChildren;
}) {
  const toggle = (name: string, on: boolean) =>
    onChange(on ? [...value, { name, level: "full" }] : value.filter((p) => p.name !== name));
  const setLevel = (name: string, level: Level) => onChange(value.map((p) => (p.name === name ? { name, level } : p)));
  return (
    <Picks>
      {children}
      {names.map((n) => {
        const picked = value.find((p) => p.name === n);
        return (
          <div key={n} class="flex items-center gap-2.5 border-b border-hairline pr-2 last:border-b-0">
            <label class="flex min-w-0 flex-1 items-center gap-2.5 px-3 py-2 text-sm">
              <input type="checkbox" checked={!!picked} onChange={(e) => toggle(n, e.currentTarget.checked)} />
              <span class="truncate">{n}</span>
            </label>
            {picked && (
              <select
                aria-label={`Level for ${n}`}
                class="h-[30px]! w-auto! flex-none py-0! text-[13px]"
                value={picked.level}
                onChange={(e) => setLevel(n, e.currentTarget.value === "observe" ? "observe" : "full")}
              >
                <option value="full">Full access</option>
                <option value="observe">Observe only</option>
              </select>
            )}
          </div>
        );
      })}
      {!names.length && <div class="px-3 py-2 text-sm text-muted">{empty}</div>}
    </Picks>
  );
}

/**
 * Which accounts may use a computer, and how. Admins are listed checked
 * and disabled: they can use every computer. Shares are left out: they are
 * managed under Shares on the Access tab.
 */
export function AccessPicks({
  accounts,
  value,
  onChange,
}: {
  accounts: Account[];
  value: AccessGrant[];
  onChange: (grants: AccessGrant[]) => void;
}) {
  const admins = accounts.filter((a) => a.admin);
  const regular = accounts.filter((a) => !a.admin && !a.sharedBy);
  return (
    <LevelPicks
      names={regular.map((a) => a.username)}
      value={value.map((g) => ({ name: g.username, level: g.level }))}
      onChange={(picks) => onChange(picks.map((p) => ({ username: p.name, level: p.level })))}
      empty="There are no other accounts yet."
    >
      {admins.map((a) => (
        <Pick key={a.username} checked disabled>
          {a.username} <Tag kind="admin">admin</Tag> <span class="text-muted max-sm:hidden">admins can use every environment</span>
        </Pick>
      ))}
    </LevelPicks>
  );
}

/** Explicit host-daemon permission shared by computer creation and settings. */
export function DockerAccessField({ value, onChange }: { value: boolean; onChange: (value: boolean) => void }) {
  return (
    <div class="mb-[18px] flex items-start gap-2.5">
      <input id="docker-access" type="checkbox" class="mt-[3px]" checked={value}
        aria-describedby="docker-access-hint" onChange={(e) => onChange(e.currentTarget.checked)} />
      <div>
        <label for="docker-access" class="font-semibold">Docker daemon access</label>
        <p id="docker-access-hint" class="mt-0.5 text-[13px] text-muted">
          Allows everyone using this environment to control the host Docker daemon and its containers, with root-equivalent access to the host.
        </p>
      </div>
    </div>
  );
}
