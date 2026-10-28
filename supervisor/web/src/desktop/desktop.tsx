// The environment page at /<name>/: a thin bar of ours with tabs for the
// desktop and the environment's shared terminal sessions. The Desktop tab is
// Selkies' web client in a frame (desktop-tab.tsx); terminal tabs are xterm
// panes (terminal/terminal.tsx), tracked by terminal/use-terminal-tabs.ts.
import { lazy, Suspense } from "preact/compat";
import { useCallback, useEffect, useMemo, useRef, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import type { Environment } from "../api.ts";
import { Avatar, Avatars } from "../components/avatar.tsx";
import { power } from "../components/environment-menu.tsx";
import { Menu, type MenuItem } from "../components/menu.tsx";
import { ViewersPopover } from "../components/viewers-popover.tsx";
import { notify } from "../components/toast.tsx";
import { cx, MORE } from "../components/ui.tsx";
import { LinkDialog, ShareDialog } from "../dialogs/share.tsx";
import { useEnvironment, useLive } from "../live.tsx";
import { agentTitle } from "../lib/activity-format.ts";
import { vpnPresentation } from "../lib/vpn-form.ts";
import { names } from "../lib/format.ts";
import { useEventListener } from "../lib/use-event-listener.ts";
import { others } from "../terminal/sessions.ts";
import { EndSessionDialog, RenameSessionDialog } from "../terminal/session-dialogs.tsx";
import { useTerminalTabs } from "../terminal/use-terminal-tabs.ts";
import { useVisibleViewport } from "./touch-controls.tsx";
import { tabShortcutListener } from "./tab-shortcuts.ts";
import { FullscreenButton } from "./fullscreen.tsx";
import { ShieldIcon } from "./bar-icons.tsx";
import { barButton, type BarSlots, EnvironmentTabs, panelId, pressed, tabId } from "./tabs.tsx";
import { ImagePaste } from "./image-paste.tsx";
import { Centered, Desktop } from "./desktop-tab.tsx";

// The xterm code loads with the first terminal tab.
const TerminalPane = lazy(() => import("../terminal/terminal.tsx").then((m) => ({ default: m.TerminalPane })));

export function EnvironmentPage({ name }: { name: string }) {
  const c = useEnvironment(name);
  const viewport = useVisibleViewport();
  return (
    // data-desktop-page: no scrollbar gutter on this page (index.css).
    <div data-desktop-page data-software-keyboard={String(viewport?.keyboard ?? false)} class="fixed inset-0 flex flex-col bg-glass" style={viewport ? { top: `${viewport.top}px`, bottom: "auto", height: `${viewport.height}px` } : undefined}>
      {c === undefined ? null : c === null ? (
        <Centered>
          <p>There's no environment named {name}, or you can't use it.</p>
          <a href="/" class="text-[#9ec0ff]">
            Back to the environments
          </a>
        </Centered>
      ) : (
        <EnvironmentView environment={c} />
      )}
    </div>
  );
}

/**
 * The page under the fixed root: the bar (identity, tabs, the active tab's
 * controls, viewers, ⋯) and the active tab. Desktop is mounted only while
 * shown; a terminal pane stays mounted, hidden, from its first showing until
 * its tab closes, so switching keeps its connection and scrollback.
 */
function EnvironmentView({ environment: c }: { environment: Environment }) {
  const { me } = useLive();
  const { query, route } = useLocation();
  const running = c.state === "running";
  // Observers watch the desktop: no terminals, no input, no power actions.
  const observing = c.level === "observe";
  const t = useTerminalTabs(c, query, route);
  const { tabs, active } = t;
  const [viewersOpen, setViewersOpen] = useState(false);
  // Share, as under Shares on the environment's Access tab: full access, but not a share itself.
  const canShare = !observing && !me.share;
  const [sharing, setSharing] = useState(false);
  const [shared, setShared] = useState<{ username: string; link: string } | null>(null);
  // The Desktop tab's frame while it is mounted, for image paste.
  const [desktopFrame, setDesktopFrame] = useState<HTMLIFrameElement | null>(null);

  const [dot, setDot] = useState<HTMLElement | null>(null);
  const [controls, setControls] = useState<HTMLElement | null>(null);
  // The active tab sets this as it renders and clears it when it goes away.
  const tabHooks = useRef<BarSlots["active"]["current"]>(null);
  const slots = useMemo<BarSlots>(() => ({ dot, controls, active: tabHooks }), [dot, controls]);
  const refocus = () => tabHooks.current?.refocus();

  useEventListener(window, "keydown", tabShortcutListener(t.selectShortcut), true);

  useEffect(() => {
    document.title = active ? `${c.name} · ${active}` : c.name;
  }, [c.name, active]);

  useJoinToasts(c.viewers, me.username);

  const closeViewers = useCallback(() => {
    setViewersOpen(false);
    refocus();
  }, []);
  const envItems: MenuItem[] = [
    ...(running && !observing
      ? [
          { label: "Restart", onSelect: () => power(c.name, "restart") },
          { label: "Stop", onSelect: () => power(c.name, "stop") },
        ]
      : []),
    { label: "Details", onSelect: () => route(`/environments/${c.name}`), separated: running },
    ...(me.admin ? [{ label: "Settings", onSelect: () => route(`/environments/${c.name}?tab=settings`) }] : []),
  ];
  const present = c.viewers.length > 0 || c.forwards.length > 0 || c.agents.length > 0;

  return (
    <>
      <div data-environment-bar class="relative flex flex-none flex-wrap items-center gap-x-1.5 bg-bar px-2.5 text-sm text-[#e6ebf1] max-sm:min-h-[46px] sm:h-[46px] sm:flex-nowrap">
        <a href="/" class={cx(barButton, "px-2")} title="Environments" aria-label="Environments">
          ‹
        </a>
        <span class="min-w-0 shrink-0 truncate text-[15px] font-bold text-white max-sm:hidden sm:max-w-[22vw]">{c.name}</span>
        <span ref={setDot} class="contents" />
        {observing && (
          <span data-observing class="ml-1 rounded-full bg-bar-hover px-2 py-px text-[12.5px] font-semibold whitespace-nowrap text-white" title="You can watch this desktop, but not use it">
            Observing
          </span>
        )}
        {c.vpn && c.vpn.state !== "disabled" && (
          <a
            href={`/environments/${c.name}?tab=settings&section=networking`}
            class={cx(barButton, "px-1.5", c.vpn.state === "blocked" ? "text-[#ffb7ad]!" : c.vpn.state === "connected" ? "text-[#3ac08a]!" : "text-busy!")}
            title={[vpnPresentation(c.vpn).label, vpnPresentation(c.vpn).detail].filter(Boolean).join(": ")}
            aria-label={vpnPresentation(c.vpn).label}
          >
            <ShieldIcon />
          </a>
        )}
        <EnvironmentTabs
          tabs={tabs.map((s) => {
            const session = (c.terminals ?? []).find((x) => x.name === s);
            return { name: s, others: session ? others(session, t.states[s] === "connected") : 0 };
          })}
          active={active}
          canAdd={running && c.terminal && !observing}
          onSelect={t.show}
          tabUrl={t.tabUrl}
          onRename={t.startRename}
          onDetach={(s) => t.closeTab(s, false)}
          onClose={(s) => void t.requestEnd(s)}
          onAdd={t.add}
          onReorder={t.reorder}
        />
        <div class="ml-auto flex h-[46px] shrink-0 items-center gap-0.5">
          <span ref={setControls} class="contents" />
          <FullscreenButton />
          {canShare && (
            <button type="button" class={cx(barButton, sharing && pressed)} title={`Share ${c.name} through a link`} onClick={() => setSharing(true)}>
              Share
            </button>
          )}
          {present && (
            <button
              type="button"
              data-viewers-toggle
              aria-expanded={viewersOpen}
              aria-label="Viewers and port mappings"
              class={cx(
                "ml-1 inline-flex min-w-0 cursor-pointer items-center gap-2 rounded-md border-0 bg-transparent px-1.5 py-1 text-[13px] text-bar-muted hover:bg-bar-hover",
                viewersOpen && "bg-bar-hover text-white",
              )}
              title={[...(c.viewers.length ? [names(c.viewers)] : []), ...c.agents.map(agentTitle)].join(", ")}
              onClick={() => (viewersOpen ? closeViewers() : setViewersOpen(true))}
            >
              {(c.viewers.length > 0 || c.agents.length > 0) && <><span class="max-sm:hidden"><Avatars names={c.viewers} mics={c.mics} agents={c.agents} ring="var(--color-bar)" /></span><span class="sm:hidden">{c.viewers.length + c.agents.length}</span></>}
              {c.forwards.length > 0 && <span class="whitespace-nowrap max-sm:hidden">⇄ {c.forwards.length}</span>}
            </button>
          )}
          <Menu
            items={() => [...envItems, ...(tabHooks.current?.menu() ?? [])]}
            onClose={refocus}
            trigger={({ toggle, open }) => (
              <button type="button" aria-label="More" aria-haspopup="menu" aria-expanded={open} class={cx(barButton, open && pressed)} onClick={toggle}>
                {MORE}
              </button>
            )}
          />
        </div>
        {viewersOpen && present && <ViewersPopover name={c.name} viewers={c.viewers} observers={c.observers} mics={c.mics} forwards={c.forwards} agents={c.agents} canShare={canShare} onClose={closeViewers} />}
      </div>
      {sharing && (
        <ShareDialog
          environment={c.name}
          onClose={() => {
            setSharing(false);
            refocus();
          }}
          onShared={(username, link) => {
            setSharing(false);
            setShared({ username, link });
          }}
        />
      )}
      {shared && (
        <LinkDialog
          username={shared.username}
          link={shared.link}
          title={`${c.name} is shared with ${shared.username}`}
          onClose={() => {
            setShared(null);
            refocus();
          }}
        />
      )}
      <div class="relative flex min-h-0 flex-1 flex-col">
        <div role="tabpanel" id={panelId(null)} aria-labelledby={tabId(null)} hidden={active !== null} class={cx("relative min-h-0 flex-1 flex-col", active === null ? "flex" : "hidden")}>
          {/* A new level or run is a new Desktop: a loaded frame keeps the
              permissions it loaded with (an observer's has no microphone or
              clipboard), and a desktop that comes back needs a fresh frame. */}
          {active === null && <Desktop key={`${c.level}:${running}`} environment={c} slots={slots} selectShortcut={t.selectShortcut} onFrame={setDesktopFrame} />}
        </div>
        {t.opened.map((s) => (
            <Suspense key={s} fallback={s === active ? <Centered>Loading terminal…</Centered> : null}>
              <TerminalPane
                name={c.name}
                session={s}
                active={s === active}
                create={t.creates(s)}
                slots={slots}
                alive={() => t.alive(s)}
                onState={t.onPaneState}
                onEnded={t.onPaneEnded}
              />
            </Suspense>
          ))}
      </div>
      {running && !observing && <ImagePaste environment={c.name} frame={desktopFrame} />}
      {t.renaming && <RenameSessionDialog session={t.renaming} environment={c.name} {...t.rename} />}
      {t.confirm && (
        <EndSessionDialog
          {...t.confirm}
          onClose={() => {
            t.cancelEnd();
            refocus();
          }}
          onEnd={() => {
            t.confirmEnd();
            refocus();
          }}
        />
      )}
    </>
  );
}

/** "anna joined": a short note when someone else opens this desktop. */
function useJoinToasts(viewers: string[], me: string) {
  const seen = useRef<string[] | null>(null);
  useEffect(() => {
    const before = seen.current;
    seen.current = viewers;
    if (!before) return;
    for (const v of viewers) {
      if (v !== me && !before.includes(v)) {
        notify(
          <>
            <Avatar name={v} ring="transparent" size={22} />
            {v} joined
          </>,
        );
      }
    }
  }, [viewers.join(","), me]);
}

