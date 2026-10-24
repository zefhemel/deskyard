// Agents' screen actions drawn over the remote screen: click ripples with the
// agent's name, drag lines, scroll arrows, key caps and "… is typing". The
// marks live in this page, above the frame, so they never reach the desktop
// or an agent's screenshots, and they take no pointer events.
import { useEffect, useRef, useState } from "preact/hooks";
import type { ScreenEvent } from "../api.ts";
import { agentLabel } from "../lib/activity-format.ts";
import { watchUIEvents } from "../lib/event-feed.ts";
import { keyCap } from "./keycap.ts";
import { type ScreenGeometry, type ScreenGeometrySource, toViewport } from "./screen-geometry.ts";

type Point = { left: number; top: number };

type Mark = { id: number; color: string } & (
  | { kind: "click" | "double-click" | "triple-click"; at: Point; label: string }
  | { kind: "drag"; from: Point; to: Point }
  | { kind: "scroll"; at: Point; dir: NonNullable<ScreenEvent["dir"]> }
  | { kind: "key"; at: Point; text: string }
);

type Typing = { color: string; label: string; at: Point; fallback: boolean };

/** How long each mark stays, in ms. */
const lifetimes: Record<Mark["kind"], number> = {
  click: 1800, // ripple 600 ms; the name fades out after 1.5 s
  "double-click": 1800,
  "triple-click": 1800,
  drag: 1000,
  scroll: 800,
  key: 1000,
};
const TYPING_MS = 1500;
const rings = { click: 1, "double-click": 2, "triple-click": 3 };
const turns = { up: 0, right: 90, down: 180, left: 270 };

export function ActivityOverlay({ name, geometry }: { name: string; geometry: ScreenGeometrySource }) {
  const [marks, setMarks] = useState<Mark[]>([]);
  const [typing, setTyping] = useState<Record<string, Typing>>({});
  const source = useRef(geometry);
  source.current = geometry;

  useEffect(() => {
    let nextId = 0;
    const timers = new Set<number>();
    const typingTimers = new Map<string, number>();
    /** Each agent's last click, in remote screen pixels. */
    const lastClick = new Map<string, [number, number]>();

    const later = (ms: number, f: () => void) => {
      const t = window.setTimeout(() => {
        timers.delete(t);
        f();
      }, ms);
      timers.add(t);
      return t;
    };
    const add = (m: Mark) => {
      setMarks((ms) => [...ms, m]);
      later(lifetimes[m.kind], () => setMarks((ms) => ms.filter((x) => x.id !== m.id)));
    };
    /** Near the agent's last click, or centred on the bottom edge of the screen. */
    const near = (g: ScreenGeometry, agent: string): { at: Point; fallback: boolean } => {
      const c = lastClick.get(agent);
      const at = c && toViewport(g, c[0], c[1]);
      if (at) return { at, fallback: false };
      const d = g.displayed;
      return { at: { left: d.left + d.width / 2, top: d.top + d.height - 16 }, fallback: true };
    };

    const draw = (e: ScreenEvent) => {
      const g = source.current();
      if (!g) return;
      const color = e.agent.color;
      const id = nextId++;
      const [x1 = -1, y1 = -1, x2 = -1, y2 = -1] = e.pos ?? [];
      switch (e.action) {
        case "click":
        case "double-click":
        case "triple-click": {
          const at = toViewport(g, x1, y1);
          if (!at) return;
          lastClick.set(e.agent.id, [x1, y1]);
          add({ id, color, kind: e.action, at, label: agentLabel(e.agent) });
          return;
        }
        case "drag": {
          const from = toViewport(g, x1, y1);
          const to = toViewport(g, x2, y2);
          if (from && to) add({ id, color, kind: "drag", from, to });
          return;
        }
        case "scroll": {
          const at = toViewport(g, x1, y1);
          if (at && e.dir) add({ id, color, kind: "scroll", at, dir: e.dir });
          return;
        }
        case "key":
        case "hold-key": {
          if (!e.keys?.length) return;
          add({ id, color, kind: "key", at: near(g, e.agent.id).at, text: e.keys.map(keyCap).join(" ") });
          return;
        }
        case "type": {
          const agent = e.agent.id;
          setTyping((t) => ({ ...t, [agent]: { color, label: agentLabel(e.agent), ...near(g, agent) } }));
          const old = typingTimers.get(agent);
          if (old !== undefined) {
            window.clearTimeout(old);
            timers.delete(old);
          }
          typingTimers.set(
            agent,
            later(TYPING_MS, () => {
              typingTimers.delete(agent);
              setTyping(({ [agent]: _, ...rest }) => rest);
            }),
          );
          return;
        }
      }
    };

    const es = watchUIEvents(`/api/desktops/${encodeURIComponent(name)}/screen-events`);
    es.addEventListener("screen", (m) => {
      try {
        draw(JSON.parse((m as MessageEvent<string>).data) as ScreenEvent);
      } catch {
        // a malformed event draws nothing
      }
    });
    return () => {
      es.close();
      for (const t of timers) window.clearTimeout(t);
      setMarks([]);
      setTyping({});
    };
  }, [name]);

  return (
    <div aria-hidden="true" class="pointer-events-none fixed inset-0 z-30 overflow-hidden select-none">
      {marks.map((m) => (m.kind === "drag" ? <DragMark key={m.id} mark={m} /> : <PointMark key={m.id} mark={m} />))}
      {Object.entries(typing).map(([agent, t]) => (
        <div
          key={agent}
          data-mark="typing"
          class={`absolute -translate-x-1/2 ${t.fallback ? "-translate-y-full" : "translate-y-[22px]"} ${pill}`}
          style={{ ...place(t.at), background: t.color }}
        >
          {t.label} is typing…
        </div>
      ))}
    </div>
  );
}

/** Preact 11 doesn't add "px" to numbers. */
const place = (p: Point) => ({ left: `${p.left}px`, top: `${p.top}px` });

const pill = "rounded-full px-2.5 py-[3px] text-[12.5px] font-semibold whitespace-nowrap text-white shadow-[0_4px_12px_-4px_rgba(0,0,0,.5)]";

function DragMark({ mark: m }: { mark: Extract<Mark, { kind: "drag" }> }) {
  return (
    <svg data-mark="drag" class="mark-fade-late absolute inset-0 h-full w-full" style={{ animationDelay: "600ms", animationDuration: "400ms" }}>
      <line x1={m.from.left} y1={m.from.top} x2={m.to.left} y2={m.to.top} stroke={m.color} stroke-width="3" stroke-linecap="round" stroke-dasharray="7 6" />
      <circle cx={m.from.left} cy={m.from.top} r="5" fill={m.color} />
      <circle cx={m.to.left} cy={m.to.top} r="7" fill="none" stroke={m.color} stroke-width="3" />
    </svg>
  );
}

function PointMark({ mark: m }: { mark: Exclude<Mark, { kind: "drag" }> }) {
  const at = place(m.at);
  switch (m.kind) {
    case "click":
    case "double-click":
    case "triple-click":
      // Centred on the click point.
      return (
        <div data-mark={m.kind} class="absolute h-9 w-9 -translate-x-1/2 -translate-y-1/2" style={at}>
          {Array.from({ length: rings[m.kind] }, (_, i) => (
            <span key={i} class="mark-ripple absolute inset-0 rounded-full border-[3px]" style={{ borderColor: m.color, animationDelay: `${i * 150}ms` }} />
          ))}
          <span class="mark-fade-late absolute top-1/2 left-1/2 h-2 w-2 -translate-x-1/2 -translate-y-1/2 rounded-full" style={{ background: m.color }} />
          <span class={`mark-fade-late absolute top-1/2 left-[42px] -translate-y-1/2 ${pill}`} style={{ background: m.color }}>
            {m.label}
          </span>
        </div>
      );
    case "scroll":
      return (
        <div
          data-mark="scroll"
          class="mark-fade-late absolute h-10 w-10 -translate-x-1/2 -translate-y-1/2 rounded-full shadow-[0_4px_12px_-4px_rgba(0,0,0,.5)]"
          style={{ ...at, background: m.color, animationDelay: "550ms", animationDuration: "250ms" }}
        >
          <div class="h-full w-full" style={{ transform: `rotate(${turns[m.dir]}deg)` }}>
            <svg viewBox="0 0 40 40" class="mark-nudge h-full w-full">
              <path d="M20 29V11M12 18l8-8 8 8" fill="none" stroke="#fff" stroke-width="3.5" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </div>
        </div>
      );
    case "key":
      return (
        <div
          data-mark="key"
          class="mark-fade-late absolute -translate-x-1/2 -translate-y-[calc(100%+22px)] rounded-md border-2 border-b-4 bg-surface px-2 py-0.5 font-mono text-[14px] font-medium whitespace-nowrap text-ink shadow-[0_4px_12px_-4px_rgba(0,0,0,.5)]"
          style={{ ...at, borderColor: m.color, animationDelay: "750ms", animationDuration: "250ms" }}
        >
          {m.text}
        </div>
      );
  }
}
