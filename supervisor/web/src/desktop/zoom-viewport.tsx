import type { ComponentChildren } from "preact";
import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import type { DeskyardScreen } from "../selkies/screen.ts";
import { zoomAt } from "./view-zoom.ts";

/** Transform the whole client so its own screen/input coordinate mapping stays intact. */
export function ZoomViewport({ children, screen, frame, zoom, onZoom }: {
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
  return <div ref={ref} data-zoom-viewport data-zoom={zoom.toFixed(2)} class="relative min-h-0 min-w-0 flex-1 overflow-auto bg-[#101722]">
    <div style={{ position: "relative", width: zoom > 1 ? `${baseSize.current.width * zoom}px` : "100%", height: zoom > 1 ? `${baseSize.current.height * zoom}px` : "100%", overflow: "hidden" }}>
      <div style={{ width: zoom > 1 ? `${baseSize.current.width}px` : "100%", height: zoom > 1 ? `${baseSize.current.height}px` : "100%", transformOrigin: "0 0", transform: `scale(${zoom})` }}>
        {children}
      </div>
    </div>
  </div>;
}
