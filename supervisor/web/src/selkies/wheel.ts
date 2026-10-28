/** Preserve fractional trackpad motion before handing discrete notches to X11. */
export class WheelAccumulator {
  private x = 0;
  private y = 0;
  private at = -Infinity;

  take(dx: number, dy: number, at: number): [number, number] {
    if (at - this.at > 250) this.reset();
    this.at = at;
    const accumulate = (carry: number, delta: number) => {
      if (!Number.isFinite(delta)) return carry;
      if (carry * delta < 0) carry = 0;
      return carry + Math.max(-2000, Math.min(2000, delta));
    };
    this.x = accumulate(this.x, dx);
    this.y = accumulate(this.y, dy);
    const x = Math.trunc(this.x / 40) || 0, y = Math.trunc(this.y / 40) || 0;
    this.x -= x * 40;
    this.y -= y * 40;
    return [x, y];
  }

  reset() { this.x = this.y = 0; this.at = -Infinity; }
}

/** Scoped to Selkies' input surface; upstream still owns pointer/mouse state. */
export function installWheelScrolling(element: HTMLElement): () => void {
  const motion = new WheelAccumulator();
  let replaying = false;
  const wheel = (event: WheelEvent) => {
    if (replaying) return;
    // Preserve browser/app pinch-to-zoom and native line/page wheel events.
    if (event.ctrlKey || event.metaKey || event.deltaMode !== 0) {
      motion.reset();
      return;
    }
    event.preventDefault();
    event.stopImmediatePropagation();
    const [x, y] = motion.take(event.deltaX, event.deltaY, event.timeStamp);
    const send = (dx: number, dy: number) => element.dispatchEvent(new WheelEvent("wheel", {
      bubbles: true, cancelable: true, deltaMode: 1, deltaX: dx, deltaY: dy,
      clientX: event.clientX, clientY: event.clientY,
      screenX: event.screenX, screenY: event.screenY,
      shiftKey: event.shiftKey, altKey: event.altKey, buttons: event.buttons,
    }));
    // Some clients interpret a line-mode event as exactly one detent, so emit
    // individual notches rather than silently discarding its magnitude.
    replaying = true;
    try {
      for (let n = 0; n < Math.abs(y); n++) send(0, Math.sign(y));
      for (let n = 0; n < Math.abs(x); n++) send(Math.sign(x), 0);
    } finally { replaying = false; }
  };
  element.addEventListener("wheel", wheel, { capture: true, passive: false });
  return () => element.removeEventListener("wheel", wheel, { capture: true });
}
