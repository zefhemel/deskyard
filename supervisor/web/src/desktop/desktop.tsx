// The environment page at /<name>/: a thin bar of ours with tabs for the
// desktop and the environment's shared terminal sessions. The Desktop tab is
// Selkies' web client in a frame (how we drive it is described in
// selkies.ts); terminal tabs are xterm panes (terminal/terminal.tsx).
import type { ComponentChildren } from "preact";
import { createPortal, lazy, Suspense } from "preact/compat";
import { useCallback, useEffect, useMemo, useRef, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { api, ApiError, type Environment } from "../api.ts";
import { Avatar, Avatars } from "../components/avatar.tsx";
import { power } from "../components/environment-menu.tsx";
import { Menu, type MenuItem } from "../components/menu.tsx";
import { ViewersPopover } from "../components/viewers-popover.tsx";
import { notify, notifyError } from "../components/toast.tsx";
import { Button, cx, Dialog, ErrorText, Field, MORE } from "../components/ui.tsx";
import { LinkDialog, ShareDialog } from "../dialogs/share.tsx";
import { useEnvironment, useLive } from "../live.tsx";
import { agentTitle } from "../lib/activity-format.ts";
import { leftNeighbour, loadOrder, orderTabs, saveOrder, vanished } from "../lib/tab-order.ts";
import { vpnPresentation } from "../lib/vpn-form.ts";
import { names, notice } from "../lib/format.ts";
import { useDevicePixelRatio } from "../lib/use-device-pixel-ratio.ts";
import { useEventListener, useFrameWindowListener } from "../lib/use-event-listener.ts";
import { isShell, needsConfirm, others } from "../terminal/sessions.ts";
import { TouchControls, useVisibleViewport } from "./touch-controls.tsx";
import { ZoomViewport } from "./zoom-viewport.tsx";
import { tabShortcutListener } from "./tab-shortcuts.ts";
import { FullscreenButton } from "./fullscreen.tsx";
import { AudioControl } from "./audio-panel.tsx";
import { hasStats, startAutoQuality } from "./auto-quality.ts";
import { useDisplay } from "./use-display.ts";
import { ClipboardIcon, DisplayIcon, KeyboardIcon, ShieldIcon } from "./bar-icons.tsx";
import { barButton, type BarSlots, ConnDot, EnvironmentTabs, panelId, pressed, tabId, useTabHooks } from "./tabs.tsx";
import { avSyncWanted, startAvSync } from "./avsync.ts";
import { ClipboardPopover } from "./clipboard.tsx";
import { ActivityOverlay } from "./overlay.tsx";
import { ImagePaste } from "./image-paste.tsx";
import { DropZone } from "./upload.tsx";
import type { ConnectionState, DeskyardScreen, Display, Size } from "../selkies/screen.ts";
import {
  defaultQuality,
  focusFrame,
  frameAllow,
  frameSrc,
  httpsHelp,
  insecureContext,
  isQualityId,
  jpegOnly,
  keyCombos,
  configureDisplay,
  lockSize,
  onClipboard,
  presetOf,
  qualityChoices,
  qualityOf,
  type PresetId,
  type QualityId,
  remoteSize,
  screenOf,
  selkiesScreenGeometry,
  sendClipboard,
  sendQuality,
} from "./selkies.ts";
import { isPhone, useTouchCapable } from "./device.ts";
import { AUTO_FIT, initialLock, initialTrackpad, parseScaling, displayScale, scaledSize, type Scaling } from "./display-prefs.ts";

// Remembered per browser.
const QUALITY_KEY = "deskyard.quality";
const LOCK_SIZE_KEY = "deskyard.lockSize";
const SCALING_KEY = "deskyard.scaling";
const TRACKPAD_KEY = "deskyard.trackpad";
const resolutions: Size[] = [
  { width: 800, height: 600 },
  { width: 1024, height: 768 },
  { width: 1280, height: 720 },
  { width: 1280, height: 800 },
  { width: 1920, height: 1080 },
  { width: 600, height: 900 },
  { width: 900, height: 1200 },
];

function stored<T>(key: string, parse: (v: string) => T | null, fallback: T): T {
  const v = storedRaw(key);
  return (v !== null && parse(v)) || fallback;
}

function storedRaw(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function store(key: string, value: string | null): void {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    // private mode: not remembered
  }
}

const parseQuality = (v: string): QualityId | null => (isQualityId(v) ? v : null);

// The xterm code loads with the first terminal tab.
const TerminalPane = lazy(() => import("../terminal/terminal.tsx").then((m) => ({ default: m.TerminalPane })));

const validSession = (s: string) => /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(s);

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

function Centered({ children }: { children: ComponentChildren }) {
  return <div class="flex flex-1 flex-col items-center justify-center gap-3.5 px-6 text-center text-[15px] text-[#c5cdd8]">{children}</div>;
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
  const listed = running && !observing ? (c.terminals ?? []).map((t) => t.name) : [];
  const listKey = listed.join("\n");

  // Sessions opened here that the list doesn't show yet (session → create on first attach).
  const [pending, setPending] = useState<Record<string, boolean>>(() => {
    const s = query.terminal;
    if (!s || !validSession(s) || observing) return {};
    if (query.create === "1") return { [s]: true };
    return listed.includes(s) ? {} : { [s]: false };
  });
  // Ended from here while the list still shows them.
  const [gone, setGone] = useState<string[]>([]);
  const [opened, setOpened] = useState<string[]>([]);
  const [order, setOrder] = useState(() => loadOrder(c.name));
  const [states, setStates] = useState<Record<string, string>>({});
  const [renameInFlight, setRenameInFlight] = useState<string | null>(null);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<{ session: string; others: number; command: string } | null>(null);
  const [viewersOpen, setViewersOpen] = useState(false);
  // Share, as under Shares on the environment's Access tab: full access, but not a share itself.
  const canShare = !observing && !me.share;
  const [sharing, setSharing] = useState(false);
  const [shared, setShared] = useState<{ username: string; link: string } | null>(null);
  // The Desktop tab's frame while it is mounted, for image paste.
  const [desktopFrame, setDesktopFrame] = useState<HTMLIFrameElement | null>(null);
  const seen = useRef(new Set<string>());
  const ending = useRef(new Set<string>());
  // A deep link's session the list hasn't shown: if it turns out ended, Desktop shows.
  const linked = useRef(new Set<string>(!observing && query.terminal && validSession(query.terminal) && query.create !== "1" && !listed.includes(query.terminal) ? [query.terminal] : []));
  // × and + in flight, so a double click asks or acts once.
  const busy = useRef(new Set<string>());
  const deleting = useRef(new Set<string>());
  const adding = useRef(false);

  const shown = [...listed, ...(renameInFlight && !listed.includes(renameInFlight) ? [renameInFlight] : []), ...Object.keys(pending).filter((s) => !listed.includes(s))].filter((s) => !gone.includes(s));
  const tabs = orderTabs(shown, order);
  const tabsKey = tabs.join("\n");
  const active = query.terminal && tabs.includes(query.terminal) ? query.terminal : null;

  // What callbacks from panes and requests need, current.
  const latest = useRef({ tabs, shownTab: query.terminal, listed, pending, states, route });
  latest.current = { tabs, shownTab: query.terminal, listed, pending, states, route };
  // The tabs as last rendered, to find a closing tab's left neighbour.
  const rendered = useRef(tabs);
  // The tab being closed: its URL is about to change, so it isn't a bad link.
  const closing = useRef<string | null>(null);

  const [dot, setDot] = useState<HTMLElement | null>(null);
  const [controls, setControls] = useState<HTMLElement | null>(null);
  // The active tab sets this as it renders and clears it when it goes away.
  const tabHooks = useRef<BarSlots["active"]["current"]>(null);
  const slots = useMemo<BarSlots>(() => ({ dot, controls, active: tabHooks }), [dot, controls]);

  const tabUrl = (s: string | null) => (s ? `/${c.name}/?terminal=${encodeURIComponent(s)}` : `/${c.name}/`);
  const show = (s: string | null) => latest.current.route(tabUrl(s), true);

  const selectShortcut = useCallback((index: number) => {
    const session = index === 0 ? null : latest.current.tabs[index - 1];
    if (session === undefined || document.querySelector('[role="dialog"]')) return false;
    show(session);
    return true;
  }, []);
  useEventListener(window, "keydown", tabShortcutListener(selectShortcut), true);

  /** Closes a session's tab; when it was the active one, moves left (or to Desktop) and maybe says so. */
  const closeTab = (s: string, say: boolean, toDesktop = false) => {
    const now = latest.current;
    setOpened((o) => o.filter((x) => x !== s));
    setPending((p) => {
      if (!(s in p)) return p;
      const { [s]: _, ...rest } = p;
      return rest;
    });
    if (now.listed.includes(s)) setGone((g) => (g.includes(s) ? g : [...g, s]));
    setStates(({ [s]: _, ...rest }) => rest);
    const wasLinked = linked.current.delete(s);
    // The list and the pane may both report the end before the page moves on.
    if (now.shownTab === s && closing.current !== s) {
      // Its left neighbour among the tabs still there, or Desktop.
      const before = rendered.current.includes(s) ? rendered.current.slice(0, rendered.current.indexOf(s) + 1) : [s];
      const left = before.filter((t) => t === s || now.tabs.includes(t));
      closing.current = s;
      show(toDesktop || wasLinked ? null : leftNeighbour(left, s));
      if (say) notify(`Session ${s} ended`);
    }
  };

  // On load: drop ?create=1 (an explicit open, e.g. an environment card's +),
  // and check that a deep-linked session exists; a link never creates one.
  useEffect(() => {
    const s = query.terminal;
    if (!s) return;
    if (!validSession(s)) {
      show(null);
      return;
    }
    if (observing) {
      show(null);
      return;
    }
    if (query.create === "1") {
      show(s);
      return;
    }
    if (listed.includes(s) || !running) return;
    api.terminalSessions(c.name).then(
      (r) => {
        if (!r.sessions.some((t) => t.name === s)) closeTab(s, true, true);
      },
      () => undefined, // the pane's join says whether it exists
    );
  }, []);

  // Follow the session list: newly listed sessions stop being pending, and a
  // session that disappears (ended elsewhere, or its shell exited) closes.
  useEffect(() => {
    if (Object.keys(pending).some((s) => listed.includes(s))) setPending((p) => Object.fromEntries(Object.entries(p).filter(([s]) => !listed.includes(s))));
    if (gone.some((s) => !listed.includes(s))) setGone((g) => g.filter((s) => listed.includes(s)));
    for (const s of listed) {
      seen.current.add(s);
      linked.current.delete(s);
    }
    for (const s of vanished(seen.current, listed, [...Object.keys(pending), ...(renameInFlight ? [renameInFlight] : [])])) {
      seen.current.delete(s);
      closeTab(s, running && !ending.current.has(s));
    }
  }, [listKey, running]);

  // A stopped environment has no terminal tabs.
  useEffect(() => {
    if (running) return;
    setPending({});
    setOpened([]);
    setGone([]);
  }, [running]);

  // A link to a tab that isn't there (any more) shows Desktop.
  useEffect(() => {
    if (query.terminal === closing.current) return;
    closing.current = null;
    if (query.terminal && !active && !(query.terminal in pending)) show(null);
  }, [query.terminal, active, tabsKey]);

  // Panes mount on first showing.
  useEffect(() => {
    if (active && !opened.includes(active)) setOpened((o) => [...o, active]);
  }, [active]);

  // The viewer's order: new sessions go at the end of it from now on.
  useEffect(() => {
    if (!tabs.length) return;
    setOrder(tabs);
    saveOrder(c.name, tabs);
  }, [tabsKey]);

  useEffect(() => {
    document.title = active ? `${c.name} · ${active}` : c.name;
  }, [c.name, active]);

  useEffect(() => {
    rendered.current = tabs;
  });

  useJoinToasts(c.viewers, me.username);

  /** A session + just created: it exists, so its pane only joins. */
  const openCreated = (s: string) => {
    ending.current.delete(s);
    seen.current.delete(s); // a reused name: a late list without it isn't about this one
    setGone((g) => g.filter((x) => x !== s));
    setPending((p) => ({ ...p, [s]: false }));
    show(s);
  };
  const add = () => {
    if (adding.current) return;
    adding.current = true;
    api
      .createTerminal(c.name)
      .then(({ name: s }) => openCreated(s), notifyError)
      .finally(() => {
        adding.current = false;
      });
  };

  const end = (s: string) => {
    if (deleting.current.has(s)) return;
    deleting.current.add(s);
    ending.current.add(s);
    busy.current.add(s);
    api
      .endTerminal(c.name, s)
      .finally(() => {
        deleting.current.delete(s);
        busy.current.delete(s);
      })
      .then(
      () => {
        closeTab(s, false);
        notify(`Ended ${s}`);
      },
      (e: unknown) => {
        if (e instanceof ApiError && e.status === 404) {
          closeTab(s, false);
          notify(`${s} had already ended`);
        } else {
          ending.current.delete(s);
          notifyError(e);
        }
      },
    );
  };
  /** ×: ends at once for an idle shell only this page uses; asks first otherwise. */
  const requestEnd = async (s: string) => {
    if (busy.current.has(s)) return;
    busy.current.add(s);
    // Once askEnd hands over, the dialog or end()'s request releases it.
    let handedOver = false;
    try {
      handedOver = await askEnd(s);
    } finally {
      if (!handedOver) busy.current.delete(s);
    }
  };
  /** Whether it handed the session over to the dialog or to end(). */
  const askEnd = async (s: string): Promise<boolean> => {
    let session = (c.terminals ?? []).find((t) => t.name === s);
    try {
      // The freshest details: a program may have started since the last poll.
      const r = await api.terminalSessions(c.name);
      session = r.sessions.find((t) => t.name === s);
      if (!session) {
        closeTab(s, false);
        notify(`${s} had already ended`);
        return false;
      }
    } catch {
      // the list we have
    }
    const attached = latest.current.states[s] === "connected";
    const details = session ?? { clients: attached ? 1 : 0, command: "" };
    if (needsConfirm(details, attached)) {
      setConfirm({ session: s, others: others(details, attached), command: details.command });
    } else end(s);
    return true;
  };

  const onPaneEnded = useCallback((s: string, how: "ended" | "exited") => {
    if (how === "ended") {
      closeTab(s, !ending.current.has(s));
      return;
    }
    // The shell exited: the tab closes if that ended the session.
    api.terminalSessions(c.name).then(
      (r) => {
        if (!r.sessions.some((t) => t.name === s)) closeTab(s, true);
      },
      () => undefined,
    );
  }, []);
  const onPaneState = useCallback((s: string, state: string) => setStates((m) => (m[s] === state ? m : { ...m, [s]: state })), []);

  const closeViewers = useCallback(() => {
    setViewersOpen(false);
    tabHooks.current?.refocus();
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
            const t = (c.terminals ?? []).find((x) => x.name === s);
            return { name: s, others: t ? others(t, states[s] === "connected") : 0 };
          })}
          active={active}
          canAdd={running && c.terminal && !observing}
          onSelect={show}
          tabUrl={tabUrl}
          onRename={setRenaming}
          onDetach={(s) => closeTab(s, false)}
          onClose={(s) => void requestEnd(s)}
          onAdd={() => void add()}
          onReorder={(next) => {
            setOrder(next);
            saveOrder(c.name, next);
          }}
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
            onClose={() => tabHooks.current?.refocus()}
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
            tabHooks.current?.refocus();
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
            tabHooks.current?.refocus();
          }}
        />
      )}
      <div class="relative flex min-h-0 flex-1 flex-col">
        <div role="tabpanel" id={panelId(null)} aria-labelledby={tabId(null)} hidden={active !== null} class={cx("relative min-h-0 flex-1 flex-col", active === null ? "flex" : "hidden")}>
          {/* A new level or run is a new Desktop: a loaded frame keeps the
              permissions it loaded with (an observer's has no microphone or
              clipboard), and a desktop that comes back needs a fresh frame. */}
          {active === null && <Desktop key={`${c.level}:${running}`} environment={c} slots={slots} selectShortcut={selectShortcut} onFrame={setDesktopFrame} />}
        </div>
        {opened
          .filter((s) => tabs.includes(s))
          .map((s) => (
            <Suspense key={s} fallback={s === active ? <Centered>Loading terminal…</Centered> : null}>
              <TerminalPane
                name={c.name}
                session={s}
                active={s === active}
                create={pending[s] === true}
                slots={slots}
                alive={() => latest.current.listed.includes(s) || s in latest.current.pending}
                onState={onPaneState}
                onEnded={onPaneEnded}
              />
            </Suspense>
          ))}
      </div>
      {running && !observing && <ImagePaste environment={c.name} frame={desktopFrame} />}
      {renaming && <RenameSessionDialog session={renaming} environment={c.name} onStart={() => setRenameInFlight(renaming)} onFailure={() => setRenameInFlight(null)} onClose={() => setRenaming(null)} onRenamed={(next) => {
        const old = renaming;
        if (next === old) { setRenameInFlight(null); setRenaming(null); return; }
        seen.current.delete(old);
        setOpened((o) => o.filter((s) => s !== old));
        setGone((g) => [...g, old]);
        setOrder((order) => order.map((s) => s === old ? next : s));
        openCreated(next);
        setRenameInFlight(null);
        setRenaming(null);
      }} />}
      {confirm && (
        <EndSessionDialog
          {...confirm}
          onClose={() => {
            busy.current.delete(confirm.session);
            setConfirm(null);
            tabHooks.current?.refocus();
          }}
          onEnd={() => {
            end(confirm.session);
            setConfirm(null);
            tabHooks.current?.refocus();
          }}
        />
      )}
    </>
  );
}

function EndSessionDialog({ session, others: n, command, onEnd, onClose }: { session: string; others: number; command: string; onEnd: () => void; onClose: () => void }) {
  const program = !isShell(command);
  return (
    <Dialog
      title={`End ${session} for everyone?`}
      onClose={onClose}
      onSubmit={onEnd}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="danger">
            End session
          </Button>
        </>
      }
    >
      <ul class="m-0 list-disc space-y-1 pl-5">
        {program && <li>{command ? <><code>{command}</code> is running in it.</> : "A program may be running in it."}</li>}
        {n > 0 && <li>{n === 1 ? "Someone else is attached" : `${n} others are attached`}: their terminals close too.</li>}
      </ul>
      <p class="mt-2 text-muted">Its shell and every program in it stop.</p>
    </Dialog>
  );
}

/**
 * The Desktop tab: mounted only while it's shown, so a hidden desktop doesn't
 * stream. An observer's desktop is view only: the supervisor drops its input
 * before it reaches the desktop, and the page offers none (a cover over the
 * frame takes the pointer, the frame never takes focus, the bar has no input
 * controls) and never asks to change the display.
 */
function Desktop({ environment: c, slots, selectShortcut, onFrame }: { environment: Environment; slots: BarSlots; selectShortcut: (index: number) => boolean; onFrame: (frame: HTMLIFrameElement | null) => void }) {
  // Fixed for this Desktop: a new level mounts a new one.
  const observing = c.level === "observe";
  const frame = useRef<HTMLIFrameElement | null>(null);
  // The same frame, as state: listeners on its window follow it.
  const [frameElement, setFrameElement] = useState<HTMLIFrameElement | null>(null);
  const frameRef = useCallback((element: HTMLIFrameElement | null) => {
    frame.current = element;
    setFrameElement(element);
    onFrame(element);
  }, []);
  useFrameWindowListener(frameElement, "keydown", tabShortcutListener(selectShortcut), true);
  const [trackpad, setTrackpad] = useState(() => initialTrackpad(storedRaw(TRACKPAD_KEY), isPhone(window.screen)));
  const trackpadRef = useRef(trackpad);
  trackpadRef.current = trackpad;
  const [keyboard, setKeyboard] = useState(false);
  const [zoom, setZoom] = useState(1);
  const touchControls = useTouchCapable();
  const [conn, setConn] = useState<ConnectionState>("connecting");
  // The frame's client, for the Audio panel; null until it loads.
  const [screen, setScreen] = useState<DeskyardScreen | null>(null);
  const [frameKey, setFrameKey] = useState(0);
  const [remoteClip, setRemoteClip] = useState<string | null>(null);
  const [clipOpen, setClipOpen] = useState(false);
  const [quality, setQuality] = useState(() => stored(QUALITY_KEY, parseQuality, defaultQuality));
  const [locked, setLocked] = useState(() => initialLock(storedRaw(LOCK_SIZE_KEY), isPhone(window.screen)));
  const [scaling, setScaling] = useState(() => parseScaling(storedRaw(SCALING_KEY)));
  const ratio = useDevicePixelRatio();
  // The preset this page last sent, over the display settings it replaces.
  const [sent, setSent] = useState<{ preset: PresetId; over: Display["settings"] } | null>(null);
  const running = c.state === "running";

  // What this browser wants: applied when this page owns the display.
  const prefs = useRef({ quality, locked, scaling, ratio });
  prefs.current = { quality, locked, scaling, ratio };

  /** This browser's display, for when this page owns it (configureDisplay applies it then, once). */
  const applyDisplay = useCallback(() => {
    const element = frame.current;
    if (!element || observing) return;
    const { locked, scaling, ratio } = prefs.current;
    const scale = displayScale(scaling, ratio);
    const size = locked ?? { width: element.clientWidth, height: element.clientHeight };
    if (size.width <= 0 || size.height <= 0) return;
    configureDisplay(element, scaledSize(size, scale), scale);
  }, []);

  /** Sends a Quality choice's preset to the display. */
  const sendChoice = (id: QualityId, over: Display["settings"]) => {
    sendQuality(frame.current, id);
    setSent({ preset: presetOf(id).id, over });
  };
  // What this page does as the display changes.
  const beside = (d: Display) => observing || d.owner === false;
  const onDisplay = (d: Display, was: Display) => {
    if (beside(d) && d.width > 0 && (!beside(was) || d.width !== was.width || d.height !== was.height)) {
      // Beside the owner (as an observer always is): the picture at the
      // stream's size, scaled to fit.
      lockSize(frame.current, { width: d.width, height: d.height });
    }
    // An observer has no settings of ours.
    if (observing) return;
    const { quality } = prefs.current;
    if (jpegOnly && d.settings && d.settings.encoder !== "jpeg") {
      // A display streaming a codec this browser can't decode (another
      // page chose H.264): JPEG, at this browser's quality, so this page
      // sees it too. Everyone's browser decodes JPEG.
      sendChoice(quality, d.settings);
    }
    // The first page on the display: this browser's choices apply. (A
    // page handed the display later keeps the quality it streams with.)
    if (d.owner && was.owner === null) sendChoice(quality, d.settings);
    applyDisplay();
  };
  const display = useDisplay(screen, onDisplay);
  // Whether this page owns the display (null until known), and the preset the
  // display streams with (null when unknown or none of ours): what this page
  // sent, until the display reports new settings.
  const owner = display.owner;
  const live = sent && sent.over === display.settings ? sent.preset : display.settings && qualityOf(display.settings);

  useEffect(() => {
    applyDisplay();
    const element = frame.current;
    if (!element || !owner || observing) return;
    let timer: ReturnType<typeof setTimeout>;
    const observer = new ResizeObserver(() => {
      clearTimeout(timer);
      timer = setTimeout(applyDisplay, 150);
    });
    observer.observe(element);
    return () => { clearTimeout(timer); observer.disconnect(); };
  }, [owner, observing, locked, scaling, ratio, screen, applyDisplay]);

  const refocus = useCallback(() => {
    // After the click that closed a menu has landed.
    if (!observing) window.setTimeout(() => focusFrame(frame.current), 0);
  }, []);

  // Each load of the frame: follow its stream and the desktop's clipboard.
  const unsubscribe = useRef<(() => void)[]>([]);
  const onFrameLoad = useCallback(() => {
    for (const f of unsubscribe.current) f();
    unsubscribe.current = [];
    const screen = screenOf(frame.current);
    setScreen(screen);
    setKeyboard(false);
    screen?.setTouchMode?.(trackpadRef.current);
    if (!screen) {
      setConn("disconnected"); // not the client: an error page
      return;
    }
    const onState = (s: ConnectionState) => {
      setConn(s);
      if (s === "connected") screen.setTouchMode?.(trackpadRef.current);
      // Not while a panel or menu of the bar is open: focusing the frame blurs
      // this window, which closes it under the user's pointer.
      if (s === "connected" && !observing && !document.querySelector('[aria-expanded="true"]')) focusFrame(frame.current);
    };
    unsubscribe.current.push(screen.on("deskyard.state", onState), onClipboard(screen, setRemoteClip), screen.on("deskyard.keyboard", setKeyboard));
    if (avSyncWanted()) unsubscribe.current.push(startAvSync(screen));
    onState(screen.state());
  }, []);
  useEffect(
    () => () => {
      for (const f of unsubscribe.current) f();
    },
    [],
  );

  function reconnect() {
    setZoom(1);
    setConn("connecting");
    setScreen(null);
    setFrameKey((k) => k + 1);
  }
  const sendKeys = (keys: string) => {
    api.keys(c.name, keys).catch(notifyError);
    refocus();
  };
  const chooseQuality = (id: QualityId) => {
    setQuality(id);
    store(QUALITY_KEY, id);
    sendChoice(id, display.settings);
  };
  // Auto adapts while this page owns a connected display (and its client reports statistics).
  useEffect(() => {
    if (!screen || !hasStats(screen) || quality !== "auto" || !owner || observing || conn !== "connected") return;
    return startAutoQuality(screen, presetOf(quality));
  }, [screen, quality, owner, observing, conn]);
  const chooseResolution = (size: Size | null) => {
    if (!owner) return; // the display's owner sets its size
    setLocked(size);
    store(LOCK_SIZE_KEY, size ? JSON.stringify(size) : AUTO_FIT);
    setZoom(1);
    // The display effect applies the committed preferences.
    refocus();
  };
  const toggleLock = () => {
    if (locked) chooseResolution(null);
    else {
      const size = remoteSize(frame.current);
      if (size) {
        const scale = displayScale(scaling, ratio);
        chooseResolution({ width: size.width / scale, height: size.height / scale });
      }
    }
  };
  const closeClip = useCallback(() => {
    setClipOpen(false);
    refocus();
  }, [refocus]);

  const toggleKeyboard = () => screen?.keyboard?.(!keyboard);
  const chooseTouchMode = (next: boolean) => {
    setTrackpad(next);
    store(TRACKPAD_KEY, String(next));
    screen?.setTouchMode?.(next);
    refocus();
  };
  const keyItems: MenuItem[] = keyCombos.map((k) => ({ label: k.label, onSelect: () => sendKeys(k.keys) }));
  const resolutionItems: MenuItem[] = [
    { label: "Resolution: Auto fit", checked: locked === null, onSelect: () => chooseResolution(null) },
    ...resolutions.map((size) => ({
      label: `Resolution: ${size.width} × ${size.height}${size.height > size.width ? " (portrait)" : ""}`,
      checked: locked?.width === size.width && locked?.height === size.height,
      onSelect: () => chooseResolution(size),
    })),
  ];
  const scalingItems: MenuItem[] = (["auto", "1", "2"] as Scaling[]).map((value) => ({
    label: `Scaling: ${value === "auto" ? `Auto (${displayScale(value, ratio)}×)` : `${value}×`}`,
    checked: scaling === value,
    onSelect: () => {
      if (!owner) return;
      setScaling(value);
      store(SCALING_KEY, value);
      setZoom(1);
      refocus();
    },
  }));
  // Auto, while it's this browser's choice; else the preset the display streams with.
  const checkedQuality: QualityId | null = quality === "auto" ? "auto" : live;
  const displayItems: MenuItem[] = [
    ...(owner
      ? [{ label: "Lock size", checked: locked !== null, onSelect: toggleLock }, ...resolutionItems, ...scalingItems]
      : [{ label: owner === false ? "Screen size: set by the first viewer" : "Screen size: connecting…", disabled: true, onSelect: () => {} }]),
    ...qualityChoices.map((q, i) => ({
      label: (
        <>
          Quality: {q.label}
          {q.hint && <span class="text-muted">, {q.hint}</span>}
        </>
      ),
      checked: q.id === checkedQuality,
      onSelect: () => chooseQuality(q.id),
      separated: i === 0,
    })),
  ];
  const compactOnly = "sm:hidden";
  const showing = running;
  // The bar's input controls: none for an observer.
  const controlling = showing && !observing;
  // Phones and narrow tablets need these actions in ⋯.
  const moreItems: MenuItem[] = [
    ...(controlling
      ? [
          ...(owner ? [
            { label: "Lock size", checked: locked !== null, onSelect: toggleLock, separated: true, class: compactOnly },
            ...[...resolutionItems, ...scalingItems].map((item) => ({ ...item, class: compactOnly })),
          ] : [{
            label: owner === false ? "Resolution: controlled by another viewer" : "Resolution: connecting…",
            disabled: true, onSelect: () => {}, separated: true, class: compactOnly,
          }]),
          ...keyItems.map((k, i) => ({ ...k, label: <>Send {k.label}</>, separated: i === 0 && !owner, class: compactOnly })),
          ...qualityChoices.map((q, i) => ({
            label: `Quality: ${q.label}`,
            checked: q.id === checkedQuality,
            onSelect: () => chooseQuality(q.id),
            separated: i === 0,
            class: compactOnly,
          })),
        ]
      : []),
  ];
  useTabHooks(slots, true, { menu: () => moreItems, refocus });

  return (
    <>
      {showing && slots.dot && createPortal(<ConnDot state={conn} />, slots.dot)}
      {slots.controls && createPortal(
        <>
          {showing && insecureContext && <NotSecure />}
          {showing && conn === "disconnected" && (
            <button type="button" class={barButton} onClick={reconnect}>
              Reconnect
            </button>
          )}
          {showing && observing && <AudioControl name={c.name} screen={screen} connected={conn === "connected"} micAllowed={false} />}
          {controlling && (
            <>
              <AudioControl name={c.name} screen={screen} connected={conn === "connected"} />
              <Menu
                class="max-sm:hidden"
                items={displayItems}
                onClose={refocus}
                trigger={({ toggle, open }) => (
                  <button type="button" aria-label="Display" aria-haspopup="menu" aria-expanded={open} title="Display: screen size, scaling and quality" class={cx(barButton, "px-2", open && pressed)} onClick={toggle}>
                    <DisplayIcon />
                    <span aria-hidden="true" class="text-[10px]">▾</span>
                  </button>
                )}
              />
              <button
                type="button"
                data-clipboard-toggle
                aria-expanded={clipOpen}
                aria-label="Clipboard"
                title="Clipboard"
                class={cx(barButton, "px-2", clipOpen && pressed)}
                onClick={() => (clipOpen ? closeClip() : setClipOpen(true))}
              >
                <ClipboardIcon />
              </button>
              <Menu
                class="max-sm:hidden"
                items={keyItems}
                onClose={refocus}
                trigger={({ toggle, open }) => (
                  <button type="button" aria-label="Keys" aria-haspopup="menu" aria-expanded={open} title="Send keys" class={cx(barButton, "px-2", open && pressed)} onClick={toggle}>
                    <KeyboardIcon />
                    <span aria-hidden="true" class="text-[10px]">▾</span>
                  </button>
                )}
              />
            </>
          )}
          {clipOpen && controlling && (
            <ClipboardPopover
              name={c.name}
              frame={frameElement}
              remote={remoteClip}
              onClose={closeClip}
              onSend={(text) => {
                sendClipboard(frame.current, text);
                notify(`On ${c.name}'s clipboard now`);
                closeClip();
              }}
            />
          )}
        </>,
        slots.controls,
      )}
      {controlling && touchControls && <TouchControls keyboard={keyboard} trackpad={trackpad} zoom={zoom}
        onKeyboard={toggleKeyboard} onMode={chooseTouchMode} onFit={() => setZoom(1)} onKey={sendKeys} />}
      {!running ? (
        <Stopped environment={c} />
      ) : (
        <ZoomViewport screen={screen} frame={frame} zoom={zoom} onZoom={setZoom}>
        <iframe
          key={frameKey}
          ref={frameRef}
          src={frameSrc(c.name)}
          title={`${c.name}'s desktop`}
          allow={frameAllow(observing)}
          tabIndex={observing ? -1 : undefined}
          class="block h-full w-full border-0"
          onLoad={onFrameLoad}
        />
        {observing && <div data-observer-cover class="absolute inset-0 cursor-default" aria-hidden="true" />}
        </ZoomViewport>
      )}
      {running && !observing && <DropZone environment={c.name} frame={frameElement} />}
      {showing && zoom === 1 && <ActivityOverlay name={c.name} geometry={() => selkiesScreenGeometry(frame.current)} />}
    </>
  );
}

/**
 * The bar's note over plain http: the page isn't a secure context, so the
 * mic and clipboard sync are off, and the picture may be JPEG.
 */
function NotSecure() {
  return (
    <a
      href={httpsHelp}
      target="_blank"
      rel="noreferrer"
      data-not-secure
      aria-label="Not secure: some features off"
      class="ml-1 inline-flex items-center gap-1 rounded-md px-1.5 py-1 text-[12.5px] whitespace-nowrap text-bar-muted no-underline hover:bg-bar-hover hover:text-white"
      title="Plain http: this connection isn't secure, so the browser turns features off. The mic and automatic clipboard sync need https, and a browser without WebCodecs over http (Chrome, Firefox) gets a JPEG picture, which takes more bandwidth."
    >
      <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
        <rect x="3" y="7" width="10" height="7" rx="1.5" />
        <path d="M5.5 7V5a2.5 2.5 0 0 1 5 0" stroke-dasharray="2 1.6" />
      </svg>
      <span class="max-md:hidden">Not secure: some features off</span>
    </a>
  );
}

function Stopped({ environment: c }: { environment: Environment }) {
  const problem = notice(c);
  if (c.state === "stopped") {
    return (
      <Centered>
        <p class="text-lg font-semibold text-white">{c.name} is stopped</p>
        {problem && <p class={cx("max-w-[480px] text-center text-sm", problem.kind === "error" ? "text-[#f0a49b]" : "text-[#9aa6b5]")}>{problem.text}</p>}
        {c.level === "observe" ? (
          <p class="text-sm text-[#9aa6b5]">You can watch it once someone with full access starts it.</p>
        ) : (
          <Button variant="primary" onClick={() => power(c.name, "start")}>
            Start
          </Button>
        )}
      </Centered>
    );
  }
  const what = { starting: "starting", creating: "being created", upgrading: "upgrading", removing: "being removed", running: "" }[c.state];
  return (
    <Centered>
      <p class="text-lg font-semibold text-white">
        {c.name} is {what}…
      </p>
    </Centered>
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

function RenameSessionDialog({ session, environment, onClose, onRenamed, onStart, onFailure }: { session: string; environment: string; onClose: () => void; onRenamed: (next: string) => void; onStart: () => void; onFailure: () => void }) {
  const [name, setName] = useState(session);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const valid = validSession(name);
  return <Dialog title={`Rename ${session}`} onClose={() => { if (!busy) onClose(); }} onSubmit={() => {
    if (busy || !valid) return;
    setBusy(true);
    setError(null);
    onStart();
    api.renameTerminal(environment, session, name).then(({ name }) => onRenamed(name), (e: unknown) => {
      onFailure();
      setError(e instanceof Error ? e.message : String(e)); setBusy(false);
    });
  }} footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button type="submit" disabled={busy || !valid}>{busy ? "Renaming…" : "Rename"}</Button></>}>
    <Field label="Session name" htmlFor="terminal-session-name"><input id="terminal-session-name" autoFocus value={name} maxLength={64} disabled={busy} onInput={(e) => setName(e.currentTarget.value)} /></Field>
    <p class="text-sm text-muted">Use 1–64 letters, digits, underscores or dashes, starting with a letter or digit. The shell and its programs keep running.</p>
    <ErrorText error={error} />
  </Dialog>;
}
