// The desktop's Display menu (screen size, scaling and quality): the bar's
// Display ▾ on wider screens, and the same items in ⋯ on phones and narrow
// tablets (compactDisplayMenu).
import { Fragment, h } from "preact";
import type { MenuItem } from "../components/menu.tsx";
import type { Size } from "../selkies/screen.ts";
import { displayScale, type Scaling } from "./display-prefs.ts";
import { type PresetId, qualityChoices, type QualityId } from "./selkies.ts";

/** Lock size's presets, in logical pixels. */
export const resolutions: Size[] = [
  { width: 800, height: 600 },
  { width: 1024, height: 768 },
  { width: 1280, height: 720 },
  { width: 1280, height: 800 },
  { width: 1920, height: 1080 },
  { width: 600, height: 900 },
  { width: 900, height: 1200 },
];

const scalings: Scaling[] = ["auto", "1", "2"];

export interface DisplayMenuState {
  /** Whether this page owns the display; null until known. Only the owner sets size and scaling. */
  owner: boolean | null;
  locked: Size | null;
  scaling: Scaling;
  /** The device pixel ratio, for what Auto scaling comes to. */
  ratio: number;
  /** This browser's Quality choice. */
  quality: QualityId;
  /** The preset the display streams with; null when unknown or none of ours. */
  live: PresetId | null;
  onLock: () => void;
  onResolution: (size: Size | null) => void;
  onScaling: (scaling: Scaling) => void;
  onQuality: (id: QualityId) => void;
}

function sizeItems(m: DisplayMenuState): MenuItem[] {
  if (!m.owner) {
    const label = m.owner === false ? "Resolution: controlled by another viewer" : "Resolution: connecting…";
    return [{ label, disabled: true, onSelect: () => {} }];
  }
  return [
    { label: "Lock size", checked: m.locked !== null, onSelect: m.onLock },
    { label: "Resolution: Auto fit", checked: m.locked === null, onSelect: () => m.onResolution(null) },
    ...resolutions.map((size) => ({
      label: `Resolution: ${size.width} × ${size.height}${size.height > size.width ? " (portrait)" : ""}`,
      checked: m.locked?.width === size.width && m.locked?.height === size.height,
      onSelect: () => m.onResolution(size),
    })),
    ...scalings.map((value) => ({
      label: `Scaling: ${value === "auto" ? `Auto (${displayScale(value, m.ratio)}×)` : `${value}×`}`,
      checked: m.scaling === value,
      onSelect: () => m.onScaling(value),
    })),
  ];
}

function qualityItems(m: DisplayMenuState, hints: boolean): MenuItem[] {
  // Auto, while it's this browser's choice; else the preset the display streams with.
  const checked = m.quality === "auto" ? "auto" : m.live;
  return qualityChoices.map((q) => ({
    label: hints && q.hint ? h(Fragment, null, `Quality: ${q.label}`, h("span", { class: "text-muted" }, `, ${q.hint}`)) : `Quality: ${q.label}`,
    checked: q.id === checked,
    onSelect: () => m.onQuality(q.id),
  }));
}

const section = (items: MenuItem[]): MenuItem[] => items.map((item, i) => (i === 0 ? { ...item, separated: true } : item));

/** The Display ▾ menu: size and scaling (or why not), then quality. */
export function displayMenu(m: DisplayMenuState): MenuItem[] {
  return [...sizeItems(m), ...section(qualityItems(m, true))];
}

/**
 * The same for ⋯ on narrow screens, shown there only: shorter quality labels,
 * with between, as a section of its own, the items given (the keys to send).
 */
export function compactDisplayMenu(m: DisplayMenuState, between: MenuItem[]): MenuItem[] {
  return [...section(sizeItems(m)), ...section(between), ...section(qualityItems(m, false))].map((item) => ({ ...item, class: "sm:hidden" }));
}
