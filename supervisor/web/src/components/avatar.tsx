import { cx } from "./ui.tsx";

const colors = ["#2457c5", "#1e8a5a", "#9a4fb5", "#b7791f", "#c2410c", "#0e7490"];

/** A fixed color per username. */
export function avatarColor(username: string): string {
  let h = 0;
  for (const ch of username) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return colors[h % colors.length] ?? colors[0]!;
}

/** The ring around an avatar takes the color of what it sits on. */
export function Avatar({ name, ring = "var(--color-surface)", size = 24 }: { name: string; ring?: string; size?: number }) {
  return (
    <span
      title={name}
      class="inline-grid flex-none place-items-center rounded-full border-2 font-bold text-white uppercase"
      style={{ width: `${size}px`, height: `${size}px`, minWidth: `${size}px`, fontSize: `${size * 0.48}px`, background: avatarColor(name), borderColor: ring }}
    >
      {name.slice(0, 1)}
    </span>
  );
}

export function Avatars({ names, ring, size, max = 4 }: { names: string[]; ring?: string; size?: number; max?: number }) {
  return (
    <span class="inline-flex">
      {names.slice(0, max).map((n, i) => (
        <span key={n} class={cx(i > 0 && "-ml-[7px]")}>
          <Avatar name={n} ring={ring} size={size} />
        </span>
      ))}
    </span>
  );
}
