// Where the remote screen is on the page. Everything that depends on the
// streamer (how its client scales and letterboxes the remote screen) is a
// ScreenGeometrySource (Selkies': selkiesScreenGeometry in selkies.ts); the
// activity overlay only uses toViewport.

/** The remote screen's size in pixels and where it is shown, in top-level viewport coordinates. */
export interface ScreenGeometry {
  remoteWidth: number;
  remoteHeight: number;
  displayed: DOMRect;
}

/** The current geometry, or null when there is no screen to draw on (not connected yet). */
export type ScreenGeometrySource = () => ScreenGeometry | null;

/** A remote screen pixel's position on the page; null outside the remote screen. */
export function toViewport(g: ScreenGeometry, x: number, y: number): { left: number; top: number } | null {
  if (!(x >= 0 && y >= 0 && x < g.remoteWidth && y < g.remoteHeight)) return null;
  const { left, top, width, height } = g.displayed;
  return { left: left + (x * width) / g.remoteWidth, top: top + (y * height) / g.remoteHeight };
}
