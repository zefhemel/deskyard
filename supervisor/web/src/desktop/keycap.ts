// How a key combination (xdotool syntax, "ctrl+shift+t") is shown on a key cap: "⌃⇧T".

const caps: Record<string, string> = {
  ctrl: "⌃",
  control: "⌃",
  control_l: "⌃",
  control_r: "⌃",
  shift: "⇧",
  shift_l: "⇧",
  shift_r: "⇧",
  alt: "⌥",
  alt_l: "⌥",
  alt_r: "⌥",
  super: "❖",
  super_l: "❖",
  super_r: "❖",
  meta: "❖",
  return: "↵",
  enter: "↵",
  kp_enter: "↵",
  tab: "⇥",
  escape: "Esc",
  esc: "Esc",
  backspace: "⌫",
  delete: "⌦",
  up: "↑",
  down: "↓",
  left: "←",
  right: "→",
  space: "Space",
  home: "Home",
  end: "End",
  prior: "PgUp",
  page_up: "PgUp",
  next: "PgDn",
  page_down: "PgDn",
};

export function keyCap(combo: string): string {
  return combo
    .split("+")
    .filter(Boolean)
    .map((k) => caps[k.toLowerCase()] ?? (k.length === 1 ? k.toUpperCase() : k))
    .join("");
}
