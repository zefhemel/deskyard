// What this device is and how it's used, for the desktop and terminal tabs.
// Each helper answers its own question; they overlap on purpose (a tablet
// with a keyboard is touch-capable but may still have desktop input).
import { useMemo, useState } from "preact/hooks";
import { useEventListener } from "../lib/use-event-listener.ts";

/**
 * A phone: a screen whose short side is below the 640px at which the bar
 * turns compact. At its own size a desktop doesn't fit there, in either
 * orientation, so a phone starts with the screen locked and the pointer as a
 * trackpad until its viewer chooses otherwise (display-prefs.ts).
 */
export const isPhone = (screen: { width: number; height: number }) => Math.min(screen.width, screen.height) < 640;

/** The device reports touch points (a touchscreen, whatever else it has). */
export const hasTouchPoints = () => navigator.maxTouchPoints > 0;

/** The primary pointer is coarse: a finger rather than a mouse. */
export const COARSE_POINTER = "(pointer: coarse)";
export const coarsePointer = () => matchMedia(COARSE_POINTER).matches;

/** A precise pointer that hovers: a mouse or trackpad, as on a computer. */
export const DESKTOP_INPUT = "(hover: hover) and (pointer: fine)";

/** Whether a media query matches, following its changes. */
export function useMediaQuery(query: string): boolean {
  const list = useMemo(() => matchMedia(query), [query]);
  const [matches, setMatches] = useState(list.matches);
  useEventListener(list, "change", () => setMatches(list.matches));
  return matches;
}

/**
 * Whether this page may be used by touch, so it should offer touch
 * controls: the device has touch points or a coarse pointer, or it has been
 * touched (a touchscreen laptop can report neither).
 */
export function useTouchCapable(): boolean {
  const [touch, setTouch] = useState(() => hasTouchPoints() || coarsePointer());
  useEventListener(window, "touchstart", () => setTouch(true), { passive: true });
  return touch;
}
