/** Minimum: a 44px helper row plus a readable 24px terminal row. */
export function terminalLayout(height: number, safeTop = 0, safeBottom = 0): "full" | "compact" | "ultra" | "insufficient" {
  if (height < 68) return "insufficient";
  // Header50 + padded Reopen50 + helper44/top4/bottom inset + host24/padding4.
  const compactMinimum = 50 + 50 + 44 + 4 + Math.max(4, safeBottom) + 28 + safeTop;
  if (height < compactMinimum) return "ultra";
  return height < 300 ? "compact" : "full";
}
