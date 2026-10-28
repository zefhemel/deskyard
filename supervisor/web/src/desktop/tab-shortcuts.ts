/** Physical number-row keys keep working when Shift changes key to punctuation. */
export function tabShortcutIndex(e: Pick<KeyboardEvent, "ctrlKey" | "shiftKey" | "altKey" | "metaKey" | "code" | "isComposing">): number | null {
  if (!e.ctrlKey || !e.shiftKey || e.altKey || e.metaKey || e.isComposing) return null;
  return /^Digit[1-9]$/.test(e.code) ? Number(e.code.slice(-1)) - 1 : null;
}

/** Capture before terminal/desktop input handlers; unused positions pass through. */
export function installTabShortcuts(target: Window, select: (index: number) => boolean): () => void {
  const handle = (e: KeyboardEvent) => {
    const index = tabShortcutIndex(e);
    if (index === null || !select(index)) return;
    e.preventDefault();
    e.stopImmediatePropagation();
  };
  target.addEventListener("keydown", handle, true);
  return () => target.removeEventListener("keydown", handle, true);
}
