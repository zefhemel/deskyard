export interface WindowRect {
  id: string;
  parent: string | null;
  x: number;
  y: number;
  width: number;
  height: number;
}

/** Include the selected window and its transient dialog family, clipped to the screen. */
export function windowRegion(windows: WindowRect[], id: string, screenWidth: number, screenHeight: number) {
  const selected = windows.find((w) => w.id === id);
  if (!selected || screenWidth <= 0 || screenHeight <= 0) return null;
  const family = new Set([id]);
  for (let i = 0; i < windows.length; i++) {
    const before = family.size;
    for (const w of windows) if (w.parent && family.has(w.parent)) family.add(w.id);
    if (before === family.size) break;
  }
  const members = windows.filter((w) => family.has(w.id) && [w.x, w.y, w.width, w.height].every(Number.isFinite) && w.width > 0 && w.height > 0);
  if (!members.length) return null;
  const x = Math.max(0, Math.min(...members.map((w) => w.x)));
  const y = Math.max(0, Math.min(...members.map((w) => w.y)));
  const right = Math.min(screenWidth, Math.max(...members.map((w) => w.x + w.width)));
  const bottom = Math.min(screenHeight, Math.max(...members.map((w) => w.y + w.height)));
  return right > x && bottom > y ? { x, y, width: right - x, height: bottom - y } : null;
}
