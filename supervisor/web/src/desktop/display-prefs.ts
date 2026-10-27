import type { Size } from "./selkies.ts";

/**
 * A phone: a screen whose short side is below the 640px at which the bar
 * turns compact. At its own size a desktop doesn't fit there, in either
 * orientation, so a phone starts with the screen locked at phoneSize and the
 * pointer as a trackpad until its viewer chooses otherwise. Tablets and
 * computers start with Auto fit and Tap.
 */
export const isPhone = (screen: { width: number; height: number }) => Math.min(screen.width, screen.height) < 640;

/** The smallest 16:9 preset: a phone's landscape shape, and a usable desktop. */
export const phoneSize: Size = { width: 1280, height: 720 };

/** Stored for a chosen Auto fit, so that a phone's default doesn't replace it. */
export const AUTO_FIT = "auto";

/** The remote screen this browser asks for: a stored choice, else the default. */
export function initialLock(saved: string | null, phone: boolean): Size | null {
  if (saved === AUTO_FIT) return null;
  return (saved !== null && parseSize(saved)) || (phone ? phoneSize : null);
}

/** Whether touch starts as a trackpad: a stored choice, else the default. */
export function initialTrackpad(saved: string | null, phone: boolean): boolean {
  return saved === "true" || saved === "false" ? saved === "true" : phone;
}

export function parseSize(v: string): Size | null {
  try {
    const s = JSON.parse(v) as Partial<Size>;
    return typeof s.width === "number" && typeof s.height === "number" && s.width > 0 && s.height > 0 ? { width: s.width, height: s.height } : null;
  } catch {
    return null;
  }
}
