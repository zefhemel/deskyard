// Fullscreen the document so the tab bar, pane and portalled menus/dialogs
// stay together. Switching desktop/terminal tabs does not replace this button.
import { useEffect, useRef, useState } from "preact/hooks";
import { notifyError } from "../components/toast.tsx";
import { cx } from "../components/ui.tsx";
import { FullscreenIcon } from "./bar-icons.tsx";
import { barButton, pressed } from "./tabs.tsx";

export function FullscreenButton() {
  const [active, setActive] = useState(() => document.fullscreenElement !== null);
  const [busy, setBusy] = useState(false);
  const owned = useRef(false);
  const mounted = useRef(true);

  useEffect(() => {
    mounted.current = true;
    const changed = () => {
      setActive(document.fullscreenElement !== null);
      if (!document.fullscreenElement) owned.current = false;
    };
    document.addEventListener("fullscreenchange", changed);
    return () => {
      mounted.current = false;
      document.removeEventListener("fullscreenchange", changed);
      // Leaving the computer page should also leave its fullscreen mode.
      if (owned.current && document.fullscreenElement === document.documentElement) {
        void document.exitFullscreen().catch(() => undefined);
      }
    };
  }, []);

  if (!document.fullscreenEnabled) return null;
  const label = active ? "Exit fullscreen" : "Enter fullscreen";
  const toggle = async () => {
    if (busy) return;
    setBusy(true);
    try {
      if (document.fullscreenElement) {
        await document.exitFullscreen();
      } else {
        owned.current = true;
        await document.documentElement.requestFullscreen();
        if (!mounted.current && document.fullscreenElement === document.documentElement) {
          await document.exitFullscreen();
        }
      }
    } catch (error) {
      owned.current = false;
      if (mounted.current) notifyError(error);
    } finally {
      if (mounted.current) setBusy(false);
    }
  };

  return (
    <button type="button" class={cx(barButton, active && pressed)} title={label}
      aria-label={label} aria-pressed={active} disabled={busy} onClick={() => void toggle()}>
      <FullscreenIcon active={active} />
    </button>
  );
}
