// The browser's device pixel ratio, kept current: it changes with browser
// zoom and when the window moves to a screen of another density.
import { useEffect, useState } from "preact/hooks";

interface RatioWindow extends EventTarget {
  readonly devicePixelRatio: number;
  matchMedia(query: string): EventTarget;
}

export const devicePixelRatioOf = (w: Pick<RatioWindow, "devicePixelRatio">) => w.devicePixelRatio || 1;

/**
 * Calls changed when the ratio may have moved from ratio: a media query for
 * exactly this ratio stops matching, or the window resizes (which browser zoom
 * also fires). Returns the cleanup; watch again with the new ratio.
 */
export function watchDevicePixelRatio(w: RatioWindow, ratio: number, changed: () => void): () => void {
  const query = w.matchMedia(`(resolution: ${ratio}dppx)`);
  query.addEventListener("change", changed);
  w.addEventListener("resize", changed);
  return () => {
    query.removeEventListener("change", changed);
    w.removeEventListener("resize", changed);
  };
}

export function useDevicePixelRatio(): number {
  const [ratio, setRatio] = useState(() => devicePixelRatioOf(window));
  useEffect(() => watchDevicePixelRatio(window, ratio, () => setRatio(devicePixelRatioOf(window))), [ratio]);
  return ratio;
}
