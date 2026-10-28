/** Bridge finger scrolling to xterm's wheel path, including tmux mouse reports.
 * Its alternate screen has no local scrollback for native touch panning. */
export function installTouchScroll(element: HTMLElement): () => void {
  const screen = element.querySelector<HTMLElement>(".xterm-screen") ?? element;
  let gesture: { id: number; x: number; y: number; lastY: number; vertical: boolean } | undefined;
  const reset = () => { gesture = undefined; };
  const start = (event: TouchEvent) => {
    reset();
    const touch = event.touches[0];
    if (event.touches.length === 1 && touch)
      gesture = { id: touch.identifier, x: touch.clientX, y: touch.clientY, lastY: touch.clientY, vertical: false };
  };
  const move = (event: TouchEvent) => {
    const touch = event.touches[0];
    if (!gesture) return;
    if (event.touches.length !== 1 || !touch || touch.identifier !== gesture.id) {
      reset();
      return;
    }
    if (!gesture.vertical) {
      const dx = Math.abs(touch.clientX - gesture.x);
      const dy = Math.abs(touch.clientY - gesture.y);
      if (Math.max(dx, dy) < 8) return;
      if (dx >= dy) {
        reset();
        return;
      }
      gesture.vertical = true;
    }
    event.preventDefault();
    const deltaY = gesture.lastY - touch.clientY;
    gesture.lastY = touch.clientY;
    if (!deltaY) return;
    // Start on the screen: normal-buffer scrolling lives on its scrollable
    // ancestor, while tmux mouse reporting lives on the terminal root.
    const wheel = new WheelEvent("wheel", {
      bubbles: true,
      cancelable: true,
      deltaMode: 0,
      deltaY,
      clientX: touch.clientX,
      clientY: touch.clientY,
    });
    // Chromium exposes zero-valued legacy deltas on synthetic WheelEvents.
    // xterm's normal-buffer scroller prefers those over deltaY; make it use
    // the standard pixel deltas instead. Its mouse-report path already does.
    Object.defineProperties(wheel, {
      wheelDeltaY: { value: undefined },
      wheelDeltaX: { value: undefined },
    });
    screen.dispatchEvent(wheel);
  };
  element.addEventListener("touchstart", start, { passive: true });
  element.addEventListener("touchmove", move, { passive: false });
  element.addEventListener("touchend", reset);
  element.addEventListener("touchcancel", reset);
  return () => {
    reset();
    element.removeEventListener("touchstart", start);
    element.removeEventListener("touchmove", move);
    element.removeEventListener("touchend", reset);
    element.removeEventListener("touchcancel", reset);
  };
}
