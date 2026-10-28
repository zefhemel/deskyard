/** Physical number-row keys keep working when Shift changes key to punctuation. */
export function tabShortcutIndex(e: Pick<KeyboardEvent, "ctrlKey" | "shiftKey" | "altKey" | "metaKey" | "code" | "isComposing">): number | null {
  if (!e.ctrlKey || !e.shiftKey || e.altKey || e.metaKey || e.isComposing) return null;
  return /^Digit[1-9]$/.test(e.code) ? Number(e.code.slice(-1)) - 1 : null;
}

/**
 * A keydown listener for the tab shortcuts. Listen with capture, before
 * terminal/desktop input handlers; unused positions pass through.
 */
export function tabShortcutListener(select: (index: number) => boolean): (e: KeyboardEvent) => void {
  return (e) => {
    const index = tabShortcutIndex(e);
    if (index === null || !select(index)) return;
    e.preventDefault();
    e.stopImmediatePropagation();
  };
}
