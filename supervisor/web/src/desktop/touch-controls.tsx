import { useEffect, useState } from "preact/hooks";
import { cx } from "../components/ui.tsx";
import { hasTouchPoints } from "./device.ts";
import { visibleViewport } from "./viewport.ts";

/** Follow the unobscured viewport when a phone's software keyboard opens. */
export function useVisibleViewport() {
  const [viewport, setViewport] = useState<{ height: number; top: number; keyboard: boolean } | null>(null);
  useEffect(() => {
    const visual = window.visualViewport;
    if (!visual || !hasTouchPoints()) return;
    const standalone = matchMedia("(display-mode: standalone)").matches && CSS.supports("-webkit-touch-callout", "none");
    const probe = document.createElement("div");
    probe.style.cssText = "position:fixed;visibility:hidden;pointer-events:none;width:0;height:100lvh";
    if (standalone) document.body.append(probe);
    const update = () => setViewport(visibleViewport(visual, window.innerHeight,
      standalone ? probe.getBoundingClientRect().height : window.innerHeight));
    update();
    visual.addEventListener("resize", update);
    visual.addEventListener("scroll", update);
    return () => { visual.removeEventListener("resize", update); visual.removeEventListener("scroll", update); probe.remove(); };
  }, []);
  return viewport;
}

export function TouchControls({ keyboard, trackpad, zoom, onKeyboard, onMode, onFit, onKey }: {
  keyboard: boolean;
  trackpad: boolean;
  zoom: number;
  onKeyboard: () => void;
  onMode: (trackpad: boolean) => void;
  onFit: () => void;
  onKey: (keys: string) => void;
}) {
  const button = "h-11 shrink-0 cursor-pointer rounded px-3";
  return <div aria-label="Touch controls" class="flex h-11 min-w-0 flex-none items-center gap-1 bg-bar px-1 text-xs text-white">
    <button type="button" class={cx(button, keyboard && "bg-bar-hover")} aria-label={keyboard ? "Hide keyboard" : "Show keyboard"}
      title={keyboard ? "Hide keyboard" : "Show keyboard"} aria-pressed={keyboard} onPointerDown={(e) => e.preventDefault()} onClick={onKeyboard}>Keyboard</button>
    <button type="button" class={cx(button, !trackpad && "bg-bar-hover")} aria-pressed={!trackpad} title="Click where you tap; drag to hold the mouse button" onClick={() => onMode(false)}>Tap</button>
    <button type="button" class={cx(button, trackpad && "bg-bar-hover")} aria-pressed={trackpad} title="Move the pointer relatively, like a laptop trackpad" onClick={() => onMode(true)}>Trackpad</button>
    {zoom > 1 && <button type="button" class={button} onClick={onFit}>Fit screen</button>}
    <div class="flex min-w-0 flex-1 items-center overflow-x-auto" aria-label="Extra keys">
      {[{ label: "Esc", key: "Escape" }, { label: "Tab", key: "Tab" }, { label: "Enter", key: "Return" }, { label: "Paste", key: "ctrl+v" }].map((k) =>
        <button key={k.key} type="button" class={button} onClick={() => onKey(k.key)}>{k.label}</button>)}
    </div>
  </div>;
}
