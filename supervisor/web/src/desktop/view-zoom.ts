/** Bound local magnification and preserve the content point beneath a pinch. */
export function zoomAt(current: number, factor: number, point: { x: number; y: number }, scroll: { left: number; top: number }) {
  const zoom = Math.min(4, Math.max(1, current * factor));
  const ratio = zoom / current;
  return { zoom,
    left: zoom === 1 ? 0 : Math.max(0, (scroll.left + point.x) * ratio - point.x),
    top: zoom === 1 ? 0 : Math.max(0, (scroll.top + point.y) * ratio - point.y) };
}
