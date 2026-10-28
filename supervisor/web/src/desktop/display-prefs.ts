import type { Size } from "../selkies/screen.ts";

/**
 * A phone (device.ts's isPhone) starts with the screen locked at phoneSize
 * and the pointer as a trackpad until its viewer chooses otherwise. Tablets
 * and computers start with Auto fit and Tap.
 *
 * The smallest 16:9 preset: a phone's landscape shape, and a usable desktop. */
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

/** Auto uses integer desktop scaling; browser zoom participates in the signal. */
export type Scaling = "auto" | "1" | "2";
export function parseScaling(value: string | null): Scaling {
  return value === "1" || value === "2" ? value : "auto";
}
export function displayScale(value: Scaling, ratio: number): 1 | 2 {
  return value === "2" || (value === "auto" && ratio >= 1.5) ? 2 : 1;
}

/** Presets are logical pixels. Fit within Xvfb's framebuffer, preserving aspect. */
export function scaledSize(size: Size, scale: number): Size {
  const fit = Math.min(scale, 3840 / size.width, 2160 / size.height);
  const even = (n: number) => Math.max(2, Math.floor(n / 2) * 2);
  return { width: even(size.width * fit), height: even(size.height * fit) };
}
