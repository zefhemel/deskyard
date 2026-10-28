// The environment page's tab strip: Desktop first and fixed, then one tab per
// shared terminal session in this viewer's order, then +. Also the pieces of
// the bar that the active tab fills in (its dot, controls and ⋯ items).
import { useEffect, useRef, useState } from "preact/hooks";
import { Menu, type MenuItem } from "../components/menu.tsx";
import { cx } from "../components/ui.tsx";
import { moveTab, moveTabTo } from "../lib/tab-order.ts";
import { DisplayIcon, TerminalIcon } from "./bar-icons.tsx";

export const barButton =
  "inline-flex cursor-pointer items-center gap-1.5 rounded-[7px] border-0 bg-transparent px-2.5 py-[7px] text-[13.5px] font-semibold whitespace-nowrap text-bar-text no-underline hover:bg-bar-hover hover:text-white";
export const pressed = "bg-bar-hover text-white";

/**
 * Where the active tab puts its part of the bar: the connection dot, its
 * controls (portals), and what it adds to ⋯ and how it takes focus back.
 */
export interface BarSlots {
  dot: HTMLElement | null;
  controls: HTMLElement | null;
  active: { current: TabHooks | null };
}

/** What the active tab adds to ⋯ and how it takes focus back; `owner` says whose they are. */
export interface TabHooks {
  owner: object;
  menu: () => MenuItem[];
  refocus: () => void;
}

/**
 * Registers the active tab's hooks as it renders (so ⋯ sees its latest items)
 * and clears them when the tab is hidden or unmounts, unless another tab has
 * taken over meanwhile.
 */
export function useTabHooks(slots: BarSlots, active: boolean, hooks: Omit<TabHooks, "owner">): void {
  const owner = useRef({}).current;
  if (active) slots.active.current = { owner, ...hooks };
  useEffect(() => {
    if (!active) return;
    return () => {
      if (slots.active.current?.owner === owner) slots.active.current = null;
    };
  }, [active, slots]);
}

export type Conn = "connecting" | "connected" | "disconnected";
const connLabels: Record<Conn, string> = {
  connecting: "Connecting",
  connected: "Connected",
  disconnected: "Disconnected",
};

/** The active tab's connection, as a dot with its state in the tooltip. */
export function ConnDot({ state }: { state: Conn }) {
  const dot = state === "connected" ? "bg-[#3ac08a]" : state === "disconnected" ? "bg-[#e0685b]" : "bg-busy animate-pulse";
  return (
    <span class="inline-flex items-center px-1" title={connLabels[state]} role="status" aria-label={connLabels[state]}>
      <span class={cx("h-2 w-2 rounded-full", dot)} />
    </span>
  );
}

/** Element ids tying each tab to its panel (session names are [A-Za-z0-9_-]). */
export const tabId = (session: string | null) => (session === null ? "tab-desktop" : `tab-terminal-${session}`);
export const panelId = (session: string | null) => (session === null ? "panel-desktop" : `panel-terminal-${session}`);

export interface TabInfo {
  name: string;
  /** Clients other than this page. */
  others: number;
}

export function EnvironmentTabs({
  tabs,
  active,
  canAdd,
  onSelect,
  onClose,
  onRename,
  onAdd,
  onReorder,
}: {
  tabs: TabInfo[];
  /** The active terminal session, or null for Desktop. */
  active: string | null;
  canAdd: boolean;
  onSelect: (session: string | null) => void;
  onClose: (session: string) => void;
  onRename: (session: string) => void;
  onAdd: () => void;
  onReorder: (order: string[]) => void;
}) {
  const strip = useRef<HTMLDivElement>(null);
  const [dragging, setDragging] = useState<string | null>(null);
  // Where a drop would land: before this tab, or at the end (null).
  const [target, setTarget] = useState<{ before: string | null } | null>(null);
  const refocus = useRef<string | null>(null);
  const order = tabs.map((t) => t.name);

  // Keep the active tab in view, and focus on a tab moved with the keyboard.
  useEffect(() => {
    const key = refocus.current ?? active ?? "";
    const el = strip.current?.querySelector<HTMLElement>(`[data-tab="${CSS.escape(key)}"]`);
    el?.scrollIntoView({ block: "nearest", inline: "nearest" });
    if (refocus.current !== null) {
      el?.focus();
      refocus.current = null;
    }
  }, [active, order.join("\n")]);

  const focusTab = (session: string | null) => strip.current?.querySelector<HTMLElement>(`[data-tab="${CSS.escape(session ?? "")}"]`)?.focus();
  const onKeyDown = (e: KeyboardEvent, session: string | null) => {
    if (e.key === "Delete" && session !== null) {
      e.preventDefault();
      onClose(session);
      return;
    }
    if (e.key === "Home" || e.key === "End") {
      e.preventDefault();
      focusTab(e.key === "Home" ? null : (order.at(-1) ?? null));
      return;
    }
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    e.preventDefault();
    const delta = e.key === "ArrowLeft" ? -1 : 1;
    if (e.altKey && e.shiftKey) {
      if (session === null) return; // Desktop stays first
      refocus.current = session;
      onReorder(moveTab(order, session, delta));
      return;
    }
    // Plain arrows move between tabs.
    const all = [null, ...order];
    const next = all[all.indexOf(session) + delta];
    if (next !== undefined) focusTab(next);
  };

  const dropAt = (e: DragEvent, session: string | null) => {
    if (dragging === null) return null;
    if (session === null) return { before: order[0] ?? null };
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const after = e.clientX > rect.left + rect.width / 2;
    return { before: after ? (order[order.indexOf(session) + 1] ?? null) : session };
  };
  const dragProps = (session: string | null) => ({
    onDragOver: (e: DragEvent) => {
      const at = dropAt(e, session);
      if (!at) return;
      e.preventDefault();
      if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
      if (target?.before !== at.before) setTarget(at);
    },
    onDrop: (e: DragEvent) => {
      const at = dropAt(e, session);
      if (!at || dragging === null) return;
      e.preventDefault();
      onReorder(moveTabTo(order, dragging, at.before));
      setDragging(null);
      setTarget(null);
    },
  });
  const marker = (before: string | null) => dragging !== null && target !== null && target.before === before && before !== dragging;

  const tab = "group relative flex h-8 flex-none items-center rounded-[7px] text-[13.5px] font-semibold";
  const tabButton = (session: string | null) => ({
    type: "button" as const,
    role: "tab" as const,
    id: tabId(session),
    "data-tab": session ?? "",
    "aria-selected": active === session,
    "aria-controls": panelId(session),
    // Roving focus: only the selected tab is in the Tab order; arrows move between tabs.
    tabIndex: active === session ? 0 : -1,
    onClick: () => onSelect(session),
    onKeyDown: (e: KeyboardEvent) => onKeyDown(e, session),
  });
  return (
    <div
      ref={strip}
      class="flex min-w-0 items-center gap-0.5 overflow-x-auto overscroll-x-contain [scrollbar-width:none] max-sm:order-last max-sm:h-10 max-sm:basis-full max-sm:px-1 sm:mx-1.5"
      onDragLeave={(e) => {
        if (!strip.current?.contains(e.relatedTarget as Node)) setTarget(null);
      }}
    >
      <div role="tablist" aria-label="Desktop and terminals" class="flex flex-none items-center gap-0.5">
        <div role="presentation" class={cx(tab, active === null ? "bg-bar-hover text-white" : "text-bar-muted hover:bg-bar-hover hover:text-white")} {...dragProps(null)}>
          <button {...tabButton(null)} title="Desktop (Ctrl+Shift+1)" aria-keyshortcuts="Control+Shift+1" class="inline-flex h-full cursor-pointer items-center gap-1.5 rounded-[7px] border-0 bg-transparent px-2.5 text-inherit">
            <DisplayIcon />
            Desktop
          </button>
        </div>
        {tabs.map((t, index) => (
          <div
            key={t.name}
            role="presentation"
            draggable
            class={cx(
              tab,
              active === t.name ? "bg-bar-hover text-white" : "text-bar-muted hover:bg-bar-hover hover:text-white",
              dragging === t.name && "opacity-50",
              marker(t.name) && "shadow-[inset_2px_0_0_#9ec0ff]",
            )}
            onDragStart={(e) => {
              e.dataTransfer?.setData("text/plain", t.name);
              if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
              setDragging(t.name);
            }}
            onDragEnd={() => {
              setDragging(null);
              setTarget(null);
            }}
            {...dragProps(t.name)}
          >
            <button
              {...tabButton(t.name)}
              aria-keyshortcuts={`Delete Alt+Shift+ArrowLeft Alt+Shift+ArrowRight${index < 8 ? ` Control+Shift+${index + 2}` : ""}`}
              title={`${t.name}${t.others ? ` · ${t.others} other${t.others === 1 ? "" : "s"} attached` : ""} (${index < 8 ? `Ctrl+Shift+${index + 2} to switch, ` : ""}Alt+Shift+← → to move, Delete to end)`}
              class="inline-flex h-full max-w-[200px] cursor-pointer items-center gap-1.5 rounded-[7px] border-0 bg-transparent pr-1 pl-2.5 text-inherit"
            >
              <TerminalIcon />
              <span class="truncate">{t.name}</span>
              {t.others > 0 && (
                <>
                  <span aria-hidden="true" class="rounded-full bg-[#2e3d52] px-1.5 text-[11px] leading-[16px] text-bar-text">
                    {t.others}
                  </span>
                  <span class="sr-only">({t.others === 1 ? "1 other" : `${t.others} others`} attached)</span>
                </>
              )}
            </button>
            <Menu
              class="inline-flex h-6 items-center"
              portal
              items={[{ label: "Rename session…", onSelect: () => onRename(t.name) }]}
              trigger={({ open, toggle }) => (
                <button type="button" aria-label={`Session menu for ${t.name}`} aria-haspopup="menu" aria-expanded={open}
                  title={`Session menu for ${t.name}`}
                  class="inline-flex h-6 w-6 cursor-pointer items-center justify-center rounded-[5px] border-0 bg-transparent text-[15px] leading-none text-inherit opacity-70 hover:bg-[#3a4b63] hover:opacity-100"
                  onClick={toggle}>⋯</button>
              )}
            />
            {/* Out of the Tab order: Delete on the focused tab does the same. */}
            <button
              type="button"
              tabIndex={-1}
              aria-label={`End ${t.name}`}
              title={`End ${t.name} for everyone`}
              class="mr-1 inline-flex h-6 w-6 cursor-pointer items-center justify-center rounded-[5px] border-0 bg-transparent text-[15px] leading-none text-inherit opacity-70 hover:bg-[#3a4b63] hover:opacity-100"
              onClick={() => onClose(t.name)}
            >
              ×
            </button>
          </div>
        ))}
      </div>
      {canAdd && (
        <button
          type="button"
          aria-label="New terminal"
          title="New terminal session"
          class={cx(
            "inline-flex h-8 w-8 flex-none cursor-pointer items-center justify-center rounded-[7px] border-0 bg-transparent text-[18px] text-bar-muted hover:bg-bar-hover hover:text-white",
            marker(null) && "shadow-[inset_2px_0_0_#9ec0ff]",
          )}
          onClick={onAdd}
          onDragOver={(e) => {
            if (dragging === null) return;
            e.preventDefault();
            if (target?.before !== null) setTarget({ before: null });
          }}
          onDrop={(e) => {
            if (dragging === null) return;
            e.preventDefault();
            onReorder(moveTabTo(order, dragging, null));
            setDragging(null);
            setTarget(null);
          }}
        >
          +
        </button>
      )}
    </div>
  );
}
