import type { ComponentChildren } from "preact";
import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import type { AppWindow } from "../api.ts";
import type { DeskyardScreen } from "./selkies.ts";
import { zoomAt } from "./view-zoom.ts";
import { windowRegion } from "./window-view.ts";

/** Transform the whole client so its own screen/input coordinate mapping stays intact. */
export function WindowViewport({ focus, windows, readable, children, screen, frame, zoom, onZoom }: {
  focus: { id: string; width: number; height: number } | null;
  windows: AppWindow[];
  readable: boolean;
  children: ComponentChildren;
  screen: DeskyardScreen | null;
  frame: { current: HTMLIFrameElement | null };
  zoom: number;
  onZoom: (zoom: number) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const observer = new ResizeObserver(() => setSize({ width: el.clientWidth, height: el.clientHeight }));
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  const baseSize = useRef(size);
  if (zoom === 1 && size.width > 0) baseSize.current = size;
  const view = useRef({ zoom, onZoom });
  view.current = { zoom, onZoom };
  const pendingScroll = useRef<{ left: number; top: number } | null>(null);
  useEffect(() => {
    if (!screen) return;
    return screen.on("deskyard.gesture", (gesture) => {
      const el = ref.current, iframe = frame.current;
      if (!el || !iframe) return;
      if (gesture.kind === "pan") {
        el.scrollLeft += gesture.dx;
        el.scrollTop += gesture.dy;
      } else if (gesture.kind === "cursor") {
        const bounds = el.getBoundingClientRect();
        const inset = Math.min(28, bounds.width / 4, bounds.height / 4);
        // The browser clamps at the content bounds; an unzoomed full screen
        // has no overflow, so following it does nothing.
        const x = gesture.x - bounds.left, y = gesture.y - bounds.top;
        if (x < inset) el.scrollLeft += x - inset;
        else if (x > el.clientWidth - inset) el.scrollLeft += x - el.clientWidth + inset;
        if (y < inset) el.scrollTop += y - inset;
        else if (y > el.clientHeight - inset) el.scrollTop += y - el.clientHeight + inset;
      } else {
        const bounds = el.getBoundingClientRect();
        const next = zoomAt(view.current.zoom, gesture.factor,
          { x: gesture.x - bounds.left, y: gesture.y - bounds.top }, pendingScroll.current ?? { left: el.scrollLeft, top: el.scrollTop });
        if (next.zoom === view.current.zoom) return;
        pendingScroll.current = next;
        view.current.zoom = next.zoom;
        view.current.onZoom(next.zoom);
      }
    });
  }, [screen, frame]);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (pendingScroll.current) {
      el.scrollLeft = pendingScroll.current.left;
      el.scrollTop = pendingScroll.current.top;
      pendingScroll.current = null;
    } else if (zoom === 1) el.scrollTo(0, 0);
    screen?.setViewZoom?.(zoom);
  }, [zoom, screen]);
  const [panning, setPanning] = useState(false);
  const drag = useRef<{ x: number; y: number; left: number; top: number } | null>(null);
  const region = focus && windowRegion(windows, focus.id, focus.width, focus.height);
  const scale = region ? (readable ? 1 : Math.min(size.width / region.width, size.height / region.height, 2)) * zoom : zoom;
  return <div ref={ref} data-window-viewport data-focused={String(!!region)} data-zoom={zoom.toFixed(2)} class="relative min-h-0 min-w-0 flex-1 overflow-auto bg-[#101722]">
    {region && readable && <button type="button" aria-pressed={panning} class="sticky top-2 left-2 z-20 min-h-11 cursor-pointer rounded-lg bg-bar px-4 text-sm text-white" onClick={() => setPanning(!panning)}>{panning ? "Use window" : "Pan view"}</button>}
    <div style={region ? { position: "relative", overflow: "hidden", width: `${region.width * scale}px`, height: `${region.height * scale}px`,
      margin: "0 auto" } : { position: "relative", width: zoom > 1 ? `${baseSize.current.width * zoom}px` : "100%", height: zoom > 1 ? `${baseSize.current.height * zoom}px` : "100%", overflow: "hidden" }}>
      <div style={region && focus ? { position: "absolute", width: `${focus.width}px`, height: `${focus.height}px`,
        transformOrigin: "0 0", transform: `translate(${-region.x * scale}px, ${-region.y * scale}px) scale(${scale})` } : { width: zoom > 1 ? `${baseSize.current.width}px` : "100%", height: zoom > 1 ? `${baseSize.current.height}px` : "100%", transformOrigin: "0 0", transform: `scale(${zoom})` }}>
        {children}
      </div>
      {region && readable && panning && <div aria-label="Drag to pan window" style={{ position: "absolute", inset: 0, touchAction: "none", cursor: "grab" }}
        onPointerDown={(e) => {
          const el = ref.current;
          if (!el) return;
          drag.current = { x: e.clientX, y: e.clientY, left: el.scrollLeft, top: el.scrollTop };
          e.currentTarget.setPointerCapture(e.pointerId);
        }}
        onPointerMove={(e) => {
          const el = ref.current, start = drag.current;
          if (!el || !start) return;
          el.scrollLeft = start.left + start.x - e.clientX;
          el.scrollTop = start.top + start.y - e.clientY;
        }}
        onPointerUp={() => { drag.current = null; }} onPointerCancel={() => { drag.current = null; }} />}

    </div>
  </div>;
}
