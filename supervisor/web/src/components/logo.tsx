import { useId } from "preact/hooks";

// The Deskyard mark: a stack of three desktops in the UI's blue, the front
// one running. Same drawing as assets/logo/deskyard-mark.svg; the gaps
// between windows are cut out, so it sits on any background.
const LIGHT = ["#a9c3f5", "#6b97ee", "#2f6fe4"] as const;
const DARK = ["#3a4a6a", "#4a6fc0", "#5b8cff"] as const;

function Cut({ x, y }: { x: number; y: number }) {
  return <rect x={x - 1.1} y={y - 1.1} width={31.2} height={23.2} rx={5.1} fill="#000" />;
}

export function Logo({ size = 22, dark = false, class: className }: { size?: number; dark?: boolean; class?: string }) {
  const id = useId();
  const [back, mid, front] = dark ? DARK : LIGHT;
  return (
    <svg width={size} height={size} viewBox="0 0 48 48" class={className} aria-hidden="true">
      <defs>
        <mask id={`${id}-mid`}>
          <rect width="48" height="48" fill="#fff" />
          <Cut x={4} y={19} />
        </mask>
        <mask id={`${id}-back`}>
          <rect width="48" height="48" fill="#fff" />
          <Cut x={9.5} y={11.5} />
          <Cut x={4} y={19} />
        </mask>
      </defs>
      <rect x="15" y="4" width="29" height="21" rx="4" fill={back} mask={`url(#${id}-back)`} />
      <rect x="9.5" y="11.5" width="29" height="21" rx="4" fill={mid} mask={`url(#${id}-mid)`} />
      <rect x="4" y="19" width="29" height="21" rx="4" fill={front} />
      <circle cx="27" cy="34" r="3" fill="#3ccf8e" />
    </svg>
  );
}
