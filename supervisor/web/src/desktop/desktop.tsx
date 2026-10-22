// The desktop page at /<name>/: a thin bar of ours above Selkies' web client
// in a frame. How we drive the client is described in selkies.ts.
import type { ComponentChildren } from "preact";
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { api, type Desktop } from "../api.ts";
import { Avatar, Avatars } from "../components/avatar.tsx";
import { power } from "../components/desktop-menu.tsx";
import { Menu, type MenuItem } from "../components/menu.tsx";
import { ViewersPopover } from "../components/viewers-popover.tsx";
import { notify, notifyError } from "../components/toast.tsx";
import { Button, cx } from "../components/ui.tsx";
import { DownloadDialog } from "../dialogs/download.tsx";
import { useDesktop, useLive } from "../live.tsx";
import { agentTitle } from "../lib/activity-format.ts";
import { names } from "../lib/format.ts";
import { AudioControl } from "./audio-panel.tsx";
import { ClipboardPopover } from "./clipboard.tsx";
import { ActivityOverlay } from "./overlay.tsx";
import {
  browserSupported,
  type ConnectionState,
  defaultQuality,
  type DeskyardScreen,
  type Display,
  focusFrame,
  frameSrc,
  isQualityId,
  keyCombos,
  lockSize,
  onClipboard,
  qualities,
  qualityOf,
  type QualityId,
  remoteSize,
  screenOf,
  selkiesScreenGeometry,
  sendClipboard,
  sendQuality,
  type Size,
  unlockSize,
} from "./selkies.ts";

// Remembered per browser.
const QUALITY_KEY = "deskyard.quality";
const LOCK_SIZE_KEY = "deskyard.lockSize";

function stored<T>(key: string, parse: (v: string) => T | null, fallback: T): T {
  try {
    const v = localStorage.getItem(key);
    return (v !== null && parse(v)) || fallback;
  } catch {
    return fallback;
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

function parseSize(v: string): Size | null {
  try {
    const s = JSON.parse(v) as Partial<Size>;
    return typeof s.width === "number" && typeof s.height === "number" && s.width > 0 && s.height > 0 ? { width: s.width, height: s.height } : null;
  } catch {
    return null;
  }
}

const connLabels: Record<ConnectionState, string> = {
  connecting: "Connecting",
  connected: "Connected",
  disconnected: "Disconnected",
};

const barButton =
  "inline-flex cursor-pointer items-center gap-1.5 rounded-[7px] border-0 bg-transparent px-2.5 py-[7px] text-[13.5px] font-semibold whitespace-nowrap text-bar-text no-underline hover:bg-bar-hover hover:text-white";

export function DesktopPage({ name }: { name: string }) {
  const c = useDesktop(name);
  useEffect(() => {
    document.title = name;
  }, [name]);
  return (
    // data-desktop-page: no scrollbar gutter on this page (index.css).
    <div data-desktop-page class="fixed inset-0 flex flex-col bg-glass">
      {c === undefined ? null : c === null ? (
        <Centered>
          <p>There's no desktop named {name}, or you can't use it.</p>
          <a href="/" class="text-[#9ec0ff]">
            Back to the desktops
          </a>
        </Centered>
      ) : (
        <Desktop desktop={c} />
      )}
    </div>
  );
}

function Centered({ children }: { children: ComponentChildren }) {
  return <div class="flex flex-1 flex-col items-center justify-center gap-3.5 px-6 text-center text-[15px] text-[#c5cdd8]">{children}</div>;
}

function Desktop({ desktop: c }: { desktop: Desktop }) {
  const { me } = useLive();
  const { route } = useLocation();
  const frame = useRef<HTMLIFrameElement>(null);
  const [conn, setConn] = useState<ConnectionState>("connecting");
  // The frame's client, for the Audio panel; null until it loads.
  const [screen, setScreen] = useState<DeskyardScreen | null>(null);
  const [frameKey, setFrameKey] = useState(0);
  const [remoteClip, setRemoteClip] = useState<string | null>(null);
  const [clipOpen, setClipOpen] = useState(false);
  const [viewersOpen, setViewersOpen] = useState(false);
  const [downloadOpen, setDownloadOpen] = useState(false);
  const [quality, setQuality] = useState(() => stored(QUALITY_KEY, parseQuality, defaultQuality));
  const [locked, setLocked] = useState(() => stored<Size | null>(LOCK_SIZE_KEY, parseSize, null));
  // Whether this page owns the display (null until known), and the preset the
  // display streams with (null when unknown or none of ours).
  const [owner, setOwner] = useState<boolean | null>(null);
  const [live, setLive] = useState<QualityId | null>(null);
  const running = c.state === "running";
  const supported = browserSupported();

  // What this browser wants: applied when this page owns the display.
  const prefs = useRef({ quality, locked });
  prefs.current = { quality, locked };

  const refocus = useCallback(() => {
    // After the click that closed a menu has landed.
    window.setTimeout(() => focusFrame(frame.current), 0);
  }, []);

  // Each load of the frame: follow its stream and the desktop's clipboard.
  const unsubscribe = useRef<(() => void)[]>([]);
  const onFrameLoad = useCallback(() => {
    for (const f of unsubscribe.current) f();
    unsubscribe.current = [];
    const screen = screenOf(frame.current);
    setScreen(screen);
    if (!screen) {
      setConn("disconnected"); // not the client: an error page
      return;
    }
    const onState = (s: ConnectionState) => {
      setConn(s);
      if (s === "connected") focusFrame(frame.current);
    };
    // What this page has done for the current connection's display.
    let was: Display = { owner: null, width: 0, height: 0, settings: null };
    const onDisplay = (d: Display) => {
      if (d.owner === null) was = d; // a new connection
      setOwner(d.owner);
      if (d.settings) setLive(qualityOf(d.settings));
      const { quality, locked } = prefs.current;
      if (d.owner && !was.owner) {
        // The first page on the display: this browser's choices apply. (A
        // page handed the display later keeps the quality it streams with.)
        if (was.owner === null) {
          sendQuality(frame.current, quality);
          setLive(quality);
        }
        if (locked) lockSize(frame.current, locked);
        else unlockSize(frame.current);
      } else if (d.owner === false && d.width > 0 && (was.owner !== false || d.width !== was.width || d.height !== was.height)) {
        // Beside the owner: the picture at the stream's size, scaled to fit.
        lockSize(frame.current, { width: d.width, height: d.height });
      }
      was = d;
    };
    unsubscribe.current = [screen.on("deskyard.state", onState), screen.on("deskyard.display", onDisplay), onClipboard(screen, setRemoteClip)];
    onState(screen.state());
    onDisplay(screen.display());
  }, []);
  useEffect(
    () => () => {
      for (const f of unsubscribe.current) f();
    },
    [],
  );

  // A fresh frame when the desktop comes back.
  const wasRunning = useRef(running);
  useEffect(() => {
    if (running && !wasRunning.current) reconnect();
    wasRunning.current = running;
  }, [running]);

  useJoinToasts(c.viewers, me.username);

  function reconnect() {
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
    setLive(id);
    store(QUALITY_KEY, id);
    sendQuality(frame.current, id);
  };
  const toggleLock = () => {
    if (!owner) return; // the display's owner sets its size
    if (locked) {
      setLocked(null);
      store(LOCK_SIZE_KEY, null);
      unlockSize(frame.current);
    } else {
      const size = remoteSize(frame.current);
      if (size) {
        setLocked(size);
        store(LOCK_SIZE_KEY, JSON.stringify(size));
        lockSize(frame.current, size);
      }
    }
    refocus();
  };
  const closeViewers = useCallback(() => {
    setViewersOpen(false);
    refocus();
  }, [refocus]);
  const closeClip = useCallback(() => {
    setClipOpen(false);
    refocus();
  }, [refocus]);
  const closeDownload = useCallback(() => {
    setDownloadOpen(false);
    refocus();
  }, [refocus]);

  const keyItems: MenuItem[] = keyCombos.map((k) => ({ label: k.label, onSelect: () => sendKeys(k.keys) }));
  const qualityItems: MenuItem[] = qualities.map((q) => ({
    label: (
      <>
        {q.label}
        {q.hint && <span class="text-muted">, {q.hint}</span>}
      </>
    ),
    checked: q.id === live,
    onSelect: () => chooseQuality(q.id),
  }));
  const phoneOnly = "sm:hidden";
  const showing = running && supported;
  const moreItems: MenuItem[] = [
    ...(running
      ? [
          { label: "Restart", onSelect: () => power(c.name, "restart") },
          { label: "Stop", onSelect: () => power(c.name, "stop") },
        ]
      : []),
    { label: "Details", onSelect: () => route(`/desktops/${c.name}`), separated: running },
    ...(me.admin ? [{ label: "Settings", onSelect: () => route(`/desktops/${c.name}?tab=settings`) }] : []),
    // On a phone the bar has no room for these.
    ...(showing
      ? [
          ...(owner ? [{ label: "Lock size", checked: locked !== null, onSelect: toggleLock, separated: true, class: phoneOnly }] : []),
          ...keyItems.map((k, i) => ({ ...k, label: <>Send {k.label}</>, separated: i === 0 && !owner, class: phoneOnly })),
          ...qualities.map((q, i) => ({
            label: `Quality: ${q.label}`,
            checked: q.id === live,
            onSelect: () => chooseQuality(q.id),
            separated: i === 0,
            class: phoneOnly,
          })),
        ]
      : []),
    ...(running ? [{ label: "Download…", onSelect: () => setDownloadOpen(true), separated: true, class: phoneOnly }] : []),
  ];

  const dot = conn === "connected" ? "bg-[#3ac08a]" : conn === "disconnected" ? "bg-[#e0685b]" : "bg-busy animate-pulse";
  const pressed = "bg-bar-hover text-white";

  return (
    <>
      <div class="relative flex h-[46px] flex-none items-center gap-1.5 bg-bar px-2.5 text-sm text-[#e6ebf1]">
        <a href="/" class={barButton} title="Desktops">
          ‹ <span class="max-sm:hidden">Desktops</span>
        </a>
        <span class="mx-1.5 h-[22px] w-px bg-[#2e3d52] max-sm:mx-0.5" />
        <span class="mx-0.5 truncate text-[15px] font-bold text-white">{c.name}</span>
        {showing ? (
          <span class="inline-flex items-center gap-1.5 text-[13px] whitespace-nowrap text-bar-muted">
            <span class={cx("h-2 w-2 rounded-full", dot)} />
            <span class="max-sm:hidden">{connLabels[conn]}</span>
          </span>
        ) : null}
        {showing && conn === "disconnected" && (
          <button type="button" class={barButton} onClick={reconnect}>
            Reconnect
          </button>
        )}
        {(c.viewers.length > 0 || c.forwards.length > 0 || c.agents.length > 0) && (
          <>
            <span class="mx-1.5 h-[22px] w-px bg-[#2e3d52] max-sm:hidden" />
            <button
              type="button"
              data-viewers-toggle
              aria-expanded={viewersOpen}
              aria-label="Viewers and port mappings"
              class={cx(
                "inline-flex min-w-0 cursor-pointer items-center gap-2 rounded-md border-0 bg-transparent px-1.5 py-1 text-[13px] text-bar-muted hover:bg-bar-hover",
                viewersOpen && "bg-bar-hover text-white",
              )}
              title={[...(c.viewers.length ? [names(c.viewers)] : []), ...c.agents.map(agentTitle)].join(", ")}
              onClick={() => (viewersOpen ? closeViewers() : setViewersOpen(true))}
            >
              {(c.viewers.length > 0 || c.agents.length > 0) && <Avatars names={c.viewers} mics={c.mics} agents={c.agents} ring="var(--color-bar)" />}
              {c.viewers.length > 0 && <span class="truncate max-md:hidden">{names(c.viewers)}</span>}
              {c.forwards.length > 0 && <span class="whitespace-nowrap">⇄ {c.forwards.length}</span>}
            </button>
          </>
        )}
        <div class="ml-auto flex items-center gap-0.5">
          {showing && (
            <>
              <AudioControl name={c.name} screen={screen} connected={conn === "connected"} webcam={c.webcam} />
              <button
                type="button"
                aria-pressed={owner === true && locked !== null}
                disabled={owner !== true}
                title={
                  owner === false
                    ? "The first viewer sets the screen size; yours is scaled to fit"
                    : "Keep the desktop's screen size and scale it to fit"
                }
                class={cx(barButton, "max-sm:hidden disabled:cursor-default disabled:opacity-50 disabled:hover:bg-transparent", owner && locked && pressed)}
                onClick={toggleLock}
              >
                <span aria-hidden="true" class="text-[12px]">
                  {owner && locked ? "☑" : "☐"}
                </span>
                Lock size
              </button>
              <button
                type="button"
                data-clipboard-toggle
                aria-expanded={clipOpen}
                class={cx(barButton, clipOpen && pressed)}
                onClick={() => (clipOpen ? closeClip() : setClipOpen(true))}
              >
                Clipboard
              </button>
              <Menu
                class="max-sm:hidden"
                items={keyItems}
                onClose={refocus}
                trigger={({ toggle, open }) => (
                  <button type="button" aria-haspopup="menu" aria-expanded={open} class={cx(barButton, open && pressed)} onClick={toggle}>
                    Keys <span aria-hidden="true" class="text-[10px]">▾</span>
                  </button>
                )}
              />
              <Menu
                class="max-sm:hidden"
                items={qualityItems}
                onClose={refocus}
                trigger={({ toggle, open }) => (
                  <button type="button" aria-haspopup="menu" aria-expanded={open} class={cx(barButton, open && pressed)} onClick={toggle}>
                    Quality <span aria-hidden="true" class="text-[10px]">▾</span>
                  </button>
                )}
              />
            </>
          )}
          {running && (
            <button type="button" aria-haspopup="dialog" class={cx(barButton, "max-sm:hidden", downloadOpen && pressed)} onClick={() => setDownloadOpen(true)}>
              Download
            </button>
          )}
          <Menu
            items={moreItems}
            onClose={refocus}
            trigger={({ toggle, open }) => (
              <button type="button" aria-label="More" aria-haspopup="menu" aria-expanded={open} class={cx(barButton, open && pressed)} onClick={toggle}>
                ⋯
              </button>
            )}
          />
        </div>
        {viewersOpen && (c.viewers.length > 0 || c.forwards.length > 0 || c.agents.length > 0) && (
          <ViewersPopover name={c.name} viewers={c.viewers} mics={c.mics} forwards={c.forwards} agents={c.agents} onClose={closeViewers} />
        )}
        {clipOpen && showing && (
          <ClipboardPopover
            name={c.name}
            remote={remoteClip}
            onClose={closeClip}
            onSend={(text) => {
              sendClipboard(frame.current, text);
              notify(`On ${c.name}'s clipboard now`);
              closeClip();
            }}
          />
        )}
      </div>
      {!running ? (
        <Stopped desktop={c} />
      ) : !supported ? (
        <Centered>
          <p>This browser can't show Deskyard desktops; use a current Chrome, Firefox or Safari</p>
        </Centered>
      ) : (
        <iframe
          key={frameKey}
          ref={frame}
          src={frameSrc(c.name)}
          title={`${c.name}'s desktop`}
          allow="clipboard-read; clipboard-write; autoplay; microphone; camera; speaker-selection"
          class="block w-full flex-1 border-0"
          onLoad={onFrameLoad}
        />
      )}
      {showing && <ActivityOverlay name={c.name} geometry={() => selkiesScreenGeometry(frame.current)} />}
      {downloadOpen && running && <DownloadDialog desktop={c} onClose={closeDownload} />}
    </>
  );
}

function Stopped({ desktop: c }: { desktop: Desktop }) {
  if (c.state === "stopped") {
    return (
      <Centered>
        <p class="text-lg font-semibold text-white">{c.name} is stopped</p>
        <Button variant="primary" onClick={() => power(c.name, "start")}>
          Start
        </Button>
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
