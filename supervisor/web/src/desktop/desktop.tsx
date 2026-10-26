// The desktop page at /<name>/: a thin bar of ours above Selkies' web client
// in a frame. How we drive the client is described in selkies.ts.
import type { ComponentChildren } from "preact";
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { api, type Environment } from "../api.ts";
import { Avatar, Avatars } from "../components/avatar.tsx";
import { power } from "../components/environment-menu.tsx";
import { Menu, type MenuItem } from "../components/menu.tsx";
import { ViewersPopover } from "../components/viewers-popover.tsx";
import { notify, notifyError } from "../components/toast.tsx";
import { Button, cx } from "../components/ui.tsx";
import { DownloadDialog } from "../dialogs/download.tsx";
import { useEnvironment, useLive } from "../live.tsx";
import { agentTitle } from "../lib/activity-format.ts";
import { vpnPresentation } from "../lib/vpn-form.ts";
import { names, notice } from "../lib/format.ts";
import { TouchControls, useVisibleViewport } from "./touch-controls.tsx";
import { ZoomViewport } from "./zoom-viewport.tsx";
import { AudioControl } from "./audio-panel.tsx";
import { ClipboardIcon, DisplayIcon, FolderIcon, KeyboardIcon, ShieldIcon, TerminalIcon } from "./bar-icons.tsx";
import { avSyncWanted, startAvSync } from "./avsync.ts";
import { ClipboardPopover } from "./clipboard.tsx";
import { ActivityOverlay } from "./overlay.tsx";
import { DropZone } from "./upload.tsx";
import {
  type ConnectionState,
  defaultQuality,
  type DeskyardScreen,
  type Display,
  focusFrame,
  frameSrc,
  httpsHelp,
  insecureContext,
  isQualityId,
  jpegOnly,
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
  const c = useEnvironment(name);
  const viewport = useVisibleViewport();
  useEffect(() => {
    document.title = name;
  }, [name]);
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
        <Desktop environment={c} />
      )}
    </div>
  );
}

function Centered({ children }: { children: ComponentChildren }) {
  return <div class="flex flex-1 flex-col items-center justify-center gap-3.5 px-6 text-center text-[15px] text-[#c5cdd8]">{children}</div>;
}

function Desktop({ environment: c }: { environment: Environment }) {
  const { me } = useLive();
  const { route } = useLocation();
  const frame = useRef<HTMLIFrameElement>(null);
  const [trackpad, setTrackpad] = useState(() => stored("deskyard.trackpad", (v) => v === "true", false));
  const trackpadRef = useRef(trackpad);
  trackpadRef.current = trackpad;
  const [keyboard, setKeyboard] = useState(false);
  const [zoom, setZoom] = useState(1);
  const [touchControls, setTouchControls] = useState(() => navigator.maxTouchPoints > 0 || matchMedia("(pointer: coarse)").matches);
  useEffect(() => {
    const touched = () => setTouchControls(true);
    window.addEventListener("touchstart", touched, { passive: true });
    return () => window.removeEventListener("touchstart", touched);
  }, []);
  const [conn, setConn] = useState<ConnectionState>("connecting");
  // The frame's client, for the Audio panel; null until it loads.
  const [screen, setScreen] = useState<DeskyardScreen | null>(null);
  const [frameKey, setFrameKey] = useState(0);
  const [remoteClip, setRemoteClip] = useState<string | null>(null);
  const [clipOpen, setClipOpen] = useState(false);
  const [viewersOpen, setViewersOpen] = useState(false);
  const [downloadOpen, setDownloadOpen] = useState(false);
  // Running terminal sessions, fetched when the Terminal menu opens.
  const [sessions, setSessions] = useState<string[] | null>(null);
  const refreshSessions = () => {
    setSessions(null);
    api.terminalSessions(c.name).then((r) => setSessions(r.sessions.map((t) => t.name)), () => setSessions([]));
  };
  const [quality, setQuality] = useState(() => stored(QUALITY_KEY, parseQuality, defaultQuality));
  const [locked, setLocked] = useState(() => stored<Size | null>(LOCK_SIZE_KEY, parseSize, null));
  // Whether this page owns the display (null until known), and the preset the
  // display streams with (null when unknown or none of ours).
  const [owner, setOwner] = useState<boolean | null>(null);
  const [live, setLive] = useState<QualityId | null>(null);
  const running = c.state === "running";

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
      if (s === "connected" && !document.querySelector('[aria-expanded="true"]')) focusFrame(frame.current);
    };
    // What this page has done for the current connection's display.
    let was: Display = { owner: null, width: 0, height: 0, settings: null };
    const onDisplay = (d: Display) => {
      if (d.owner === null) was = d; // a new connection
      setOwner(d.owner);
      if (d.settings) setLive(qualityOf(d.settings));
      const { quality, locked } = prefs.current;
      if (jpegOnly && d.settings && d.settings.encoder !== "jpeg") {
        // A display streaming a codec this browser can't decode (another
        // page chose H.264): JPEG, at this browser's quality, so this page
        // sees it too. Everyone's browser decodes JPEG.
        sendQuality(frame.current, quality);
        setLive(quality);
      }
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
    unsubscribe.current = [screen.on("deskyard.state", onState), screen.on("deskyard.display", onDisplay), onClipboard(screen, setRemoteClip), screen.on("deskyard.keyboard", setKeyboard)];
    if (avSyncWanted()) unsubscribe.current.push(startAvSync(screen));
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
    setLive(id);
    store(QUALITY_KEY, id);
    sendQuality(frame.current, id);
  };
  const chooseResolution = (size: Size | null) => {
    if (!owner) return; // the display's owner sets its size
    setLocked(size);
    store(LOCK_SIZE_KEY, size ? JSON.stringify(size) : null);
    setZoom(1);
    if (size) lockSize(frame.current, size);
    else unlockSize(frame.current);
    refocus();
  };
  const toggleLock = () => {
    if (locked) chooseResolution(null);
    else {
      const size = remoteSize(frame.current);
      if (size) chooseResolution(size);
    }
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

  const toggleKeyboard = () => screen?.keyboard?.(!keyboard);
  const chooseTouchMode = (next: boolean) => {
    setTrackpad(next);
    store("deskyard.trackpad", String(next));
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
  const displayItems: MenuItem[] = [
    ...(owner
      ? [{ label: "Lock size", checked: locked !== null, onSelect: toggleLock }, ...resolutionItems]
      : [{ label: owner === false ? "Screen size: set by the first viewer" : "Screen size: connecting…", disabled: true, onSelect: () => {} }]),
    ...qualities.map((q, i) => ({
      label: (
        <>
          Quality: {q.label}
          {q.hint && <span class="text-muted">, {q.hint}</span>}
        </>
      ),
      checked: q.id === live,
      onSelect: () => chooseQuality(q.id),
      separated: i === 0,
    })),
  ];
  const openTerminal = (session?: string) => {
    location.href = `/${c.name}/terminal/${session ? `?session=${encodeURIComponent(session)}` : ""}`;
  };
  const terminalItems: MenuItem[] = [
    ...(sessions ?? []).map((t) => ({ label: t, onSelect: () => openTerminal(t) })),
    ...(sessions === null ? [{ label: "Loading sessions…", disabled: true, onSelect: () => {} }] : []),
    { label: sessions?.length ? "New session…" : "Open terminal", onSelect: () => openTerminal(sessions?.length ? undefined : "main"), separated: (sessions?.length ?? 0) > 0 },
  ];
  const compactOnly = "sm:hidden";
  const showing = running;
  const moreItems: MenuItem[] = [
    ...(running
      ? [
          { label: "Restart", onSelect: () => power(c.name, "restart") },
          { label: "Stop", onSelect: () => power(c.name, "stop") },
        ]
      : []),
    { label: "Details", onSelect: () => route(`/environments/${c.name}`), separated: running },
    { label: "Terminal", onSelect: () => route(`/${c.name}/terminal/`), class: "sm:hidden" },
    ...(me.admin ? [{ label: "Settings", onSelect: () => route(`/environments/${c.name}?tab=settings`) }] : []),
    // Phones and narrow tablets need these actions in More.
    ...(showing
      ? [
          ...(owner ? [
            { label: "Lock size", checked: locked !== null, onSelect: toggleLock, separated: true, class: compactOnly },
            ...resolutionItems.map((item) => ({ ...item, class: compactOnly })),
          ] : [{
            label: owner === false ? "Resolution: controlled by another viewer" : "Resolution: connecting…",
            disabled: true, onSelect: () => {}, separated: true, class: compactOnly,
          }]),
          ...keyItems.map((k, i) => ({ ...k, label: <>Send {k.label}</>, separated: i === 0 && !owner, class: compactOnly })),
          ...qualities.map((q, i) => ({
            label: `Quality: ${q.label}`,
            checked: q.id === live,
            onSelect: () => chooseQuality(q.id),
            separated: i === 0,
            class: compactOnly,
          })),
        ]
      : []),
    ...(running ? [{ label: "Download…", onSelect: () => setDownloadOpen(true), separated: true, class: compactOnly }] : []),
  ];

  const dot = conn === "connected" ? "bg-[#3ac08a]" : conn === "disconnected" ? "bg-[#e0685b]" : "bg-busy animate-pulse";
  const pressed = "bg-bar-hover text-white";

  return (
    <>
      <div class="relative flex h-[46px] flex-none items-center gap-1.5 bg-bar px-2.5 text-sm text-[#e6ebf1]">
        <a href="/" class={cx(barButton, "px-2")} title="Environments" aria-label="Environments">
          ‹
        </a>
        <span class="min-w-0 truncate text-[15px] font-bold text-white max-sm:hidden">{c.name}</span>
        {showing ? (
          <span class="inline-flex items-center px-1" title={connLabels[conn]} role="status" aria-label={connLabels[conn]}>
            <span class={cx("h-2 w-2 rounded-full", dot)} />
          </span>
        ) : null}
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
        {showing && insecureContext && <NotSecure />}
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
              {(c.viewers.length > 0 || c.agents.length > 0) && <><span class="max-sm:hidden"><Avatars names={c.viewers} mics={c.mics} agents={c.agents} ring="var(--color-bar)" /></span><span class="sm:hidden">{c.viewers.length + c.agents.length}</span></>}
              {c.viewers.length > 0 && <span class="truncate max-md:hidden">{names(c.viewers)}</span>}
              {c.forwards.length > 0 && <span class="whitespace-nowrap max-sm:hidden">⇄ {c.forwards.length}</span>}
            </button>
          </>
        )}
        <div class="ml-auto flex shrink-0 items-center gap-0.5">
          {showing && (
            <>
              <AudioControl name={c.name} screen={screen} connected={conn === "connected"} />
              <Menu
                class="max-sm:hidden"
                items={displayItems}
                onClose={refocus}
                trigger={({ toggle, open }) => (
                  <button type="button" aria-label="Display" aria-haspopup="menu" aria-expanded={open} title="Display: screen size and quality" class={cx(barButton, "px-2", open && pressed)} onClick={toggle}>
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
          {running && (
            <Menu
              class="max-sm:hidden"
              items={terminalItems}
              onClose={refocus}
              trigger={({ toggle, open }) => (
                <button
                  type="button"
                  aria-label="Terminal"
                  aria-haspopup="menu"
                  aria-expanded={open}
                  title="Terminal"
                  class={cx(barButton, "px-2", open && pressed)}
                  onClick={() => {
                    if (!open) refreshSessions();
                    toggle();
                  }}
                >
                  <TerminalIcon />
                  <span aria-hidden="true" class="text-[10px]">▾</span>
                </button>
              )}
            />
          )}
          {running && (
            <button type="button" aria-haspopup="dialog" aria-label="Download" title="Download files" class={cx(barButton, "px-2 max-sm:hidden", downloadOpen && pressed)} onClick={() => setDownloadOpen(true)}>
              <FolderIcon />
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
            frame={frame.current}
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
      {showing && touchControls && <TouchControls keyboard={keyboard} trackpad={trackpad} zoom={zoom}
        onKeyboard={toggleKeyboard} onMode={chooseTouchMode} onFit={() => setZoom(1)} onKey={sendKeys} />}
      {!running ? (
        <Stopped environment={c} />
      ) : (
        <ZoomViewport screen={screen} frame={frame} zoom={zoom} onZoom={setZoom}>
        <iframe
          key={frameKey}
          ref={frame}
          src={frameSrc(c.name)}
          title={`${c.name}'s desktop`}
          allow="clipboard-read; clipboard-write; autoplay; microphone; speaker-selection"
          class="block h-full w-full border-0"
          onLoad={onFrameLoad}
        />
        </ZoomViewport>
      )}
      {running && <DropZone environment={c.name} frame={frame} frameKey={frameKey} />}
      {showing && zoom === 1 && <ActivityOverlay name={c.name} geometry={() => selkiesScreenGeometry(frame.current)} />}
      {downloadOpen && running && <DownloadDialog environment={c} onClose={closeDownload} />}
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
