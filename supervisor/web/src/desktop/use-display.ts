import { useEffect, useRef, useState } from "preact/hooks";
import type { DeskyardScreen, Display } from "../selkies/screen.ts";

/** A display not known yet: no connection, or not told. */
export const unknownDisplay: Display = { owner: null, width: 0, height: 0, settings: null };

/**
 * The frame's display, as its screen reports it ("deskyard.display"), as
 * state. changed, if given, hears each report with the one before it (on
 * this screen), to act on what changed: a new connection reports owner null.
 */
export function useDisplay(screen: DeskyardScreen | null, changed?: (display: Display, was: Display) => void): Display {
  const [display, setDisplay] = useState(unknownDisplay);
  const latest = useRef(changed);
  latest.current = changed;
  useEffect(() => {
    setDisplay(unknownDisplay);
    if (!screen) return;
    let was = unknownDisplay;
    const report = (d: Display) => {
      setDisplay(d);
      latest.current?.(d, was);
      was = d;
    };
    report(screen.display());
    return screen.on("deskyard.display", report);
  }, [screen]);
  return display;
}
