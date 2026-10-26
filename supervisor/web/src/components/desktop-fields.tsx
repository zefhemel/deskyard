// Form controls shared by the New desktop dialog and the desktop page's
// Settings and Access tabs.
import type { Account, CatalogImage } from "../api.ts";
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

/**
 * Which accounts may use a desktop. Admins are listed checked and disabled:
 * they can use every desktop.
 */
export function AccessPicks({
  accounts,
  value,
  onChange,
}: {
  accounts: Account[];
  value: string[];
  onChange: (usernames: string[]) => void;
}) {
  const toggle = (u: string, on: boolean) => onChange(on ? [...value, u] : value.filter((x) => x !== u));
  const admins = accounts.filter((a) => a.admin);
  const regular = accounts.filter((a) => !a.admin);
  return (
    <Picks>
      {admins.map((a) => (
        <Pick key={a.username} checked disabled>
          {a.username} <Tag kind="admin">admin</Tag> <span class="text-muted max-sm:hidden">admins can use every desktop</span>
        </Pick>
      ))}
      {regular.map((a) => (
        <Pick key={a.username} checked={value.includes(a.username)} onChange={(on) => toggle(a.username, on)}>
          {a.username}
        </Pick>
      ))}
      {!regular.length && <div class="px-3 py-2 text-sm text-muted">There are no other accounts yet.</div>}
    </Picks>
  );
}

/** Explicit host-daemon permission shared by desktop creation and settings. */
export function DockerAccessField({ value, onChange }: { value: boolean; onChange: (value: boolean) => void }) {
  return (
    <div class="mb-[18px] flex items-start gap-2.5">
      <input id="docker-access" type="checkbox" class="mt-[3px]" checked={value}
        aria-describedby="docker-access-hint" onChange={(e) => onChange(e.currentTarget.checked)} />
      <div>
        <label for="docker-access" class="font-semibold">Docker daemon access</label>
        <p id="docker-access-hint" class="mt-0.5 text-[13px] text-muted">
          Allows everyone using this desktop to control the host Docker daemon and its containers, with root-equivalent access to the host.
        </p>
      </div>
    </div>
  );
}
