import type { AgentPresence } from "../api.ts";
import { useLive } from "../live.tsx";
import { selfFirst } from "../lib/format.ts";
import { AgentAvatar } from "./agent-avatar.tsx";
import { cx } from "./ui.tsx";

const colors = ["#2457c5", "#1e8a5a", "#9a4fb5", "#b7791f", "#c2410c", "#0e7490"];

/** A fixed color per username. */
export function avatarColor(username: string): string {
  let h = 0;
  for (const ch of username) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return colors[h % colors.length] ?? colors[0]!;
}

export function MicIcon({ class: extra, size = 14 }: { class?: string; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true" class={extra}>
      <rect x="5.5" y="1.5" width="5" height="8.5" rx="2.5" fill="currentColor" stroke="none" />
      <path d="M3.5 7.5a4.5 4.5 0 0 0 9 0M8 12v2.5" />
    </svg>
  );
}

/**
 * The ring around an avatar takes the color of what it sits on. mic: a red
 * badge, while that person's mic is on in the desktop.
 */
export function Avatar({
  name,
  ring = "var(--color-surface)",
  size = 24,
  mic,
}: {
  name: string;
  ring?: string;
  size?: number;
  mic?: boolean;
}) {
  const badge = Math.max(12, Math.round(size * 0.55));
  return (
    <span
      data-avatar={name}
      title={mic ? `${name}, mic on` : name}
      class="relative inline-grid flex-none place-items-center rounded-full border-2 font-bold text-white uppercase"
      style={{ width: `${size}px`, height: `${size}px`, minWidth: `${size}px`, fontSize: `${size * 0.48}px`, background: avatarColor(name), borderColor: ring }}
    >
      {name.slice(0, 1)}
      {mic && (
        <span
          data-mic-badge
          role="img"
          aria-label="mic on"
          class="absolute z-10 grid place-items-center rounded-full border-[1.5px] bg-[#e5484d] text-white"
          style={{ width: `${badge}px`, height: `${badge}px`, right: `${-badge / 2.6}px`, bottom: `${-badge / 2.6}px`, borderColor: ring }}
        >
          <MicIcon size={badge - 5} />
        </span>
      )}
    </span>
  );
}

/**
 * People first, then agents (when `agents` is given), overlapping; `max`
 * counts both. People whose mic is on (`mics`) come first, with a badge.
 */
export function Avatars({
  names,
  mics = [],
  agents = [],
  ring = "var(--color-surface)",
  size = 24,
  max = 4,
}: {
  names: string[];
  mics?: string[];
  agents?: AgentPresence[];
  ring?: string;
  size?: number;
  max?: number;
}) {
  const { me } = useLive();
  const live = (n: string) => mics.includes(n);
  // You first, then whoever has a mic on.
  const people = selfFirst([...names.filter(live), ...names.filter((n) => !live(n))], me.username).slice(0, max);
  const bots = agents.slice(0, Math.max(0, max - people.length));
  const hidden = names.length + agents.length - people.length - bots.length;
  return (
    <span class="inline-flex">
      {people.map((n, i) => (
        <span key={n} class={cx(i > 0 && "-ml-[7px]")}>
          <Avatar name={n} ring={ring} size={size} mic={mics.includes(n)} />
        </span>
      ))}
      {bots.map((a, i) => (
        <span key={a.id} class={cx(people.length + i > 0 && "-ml-[7px]")}>
          <AgentAvatar agent={a} ring={ring} size={size} />
        </span>
      ))}
      {hidden > 0 && (
        <span
          class="-ml-[7px] inline-grid flex-none place-items-center rounded-full border-2 bg-[#4a596c] font-bold text-white"
          style={{ height: `${size}px`, minWidth: `${size}px`, padding: "0 3px", fontSize: `${size * 0.42}px`, borderColor: ring }}
        >
          +{hidden}
        </span>
      )}
    </span>
  );
}
