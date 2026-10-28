// The environment page's Desktop tab: Selkies' web client in a frame (how we
// drive it is described in selkies.ts), with its bar controls.
import type { ComponentChildren } from "preact";
import { createPortal } from "preact/compat";
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { api, type Environment } from "../api.ts";
import { power } from "../components/environment-menu.tsx";
import { Menu, type MenuItem } from "../components/menu.tsx";
import { notify, notifyError } from "../components/toast.tsx";
import { Button, cx } from "../components/ui.tsx";
import { notice } from "../lib/format.ts";
import { useDevicePixelRatio } from "../lib/use-device-pixel-ratio.ts";
import { useFrameWindowListener } from "../lib/use-event-listener.ts";
import type { ConnectionState, DeskyardScreen, Display, Size } from "../selkies/screen.ts";
import { AudioControl } from "./audio-panel.tsx";
import { hasStats, startAutoQuality } from "./auto-quality.ts";
import { avSyncWanted, startAvSync } from "./avsync.ts";
import { ClipboardIcon, DisplayIcon, KeyboardIcon } from "./bar-icons.tsx";
import { ClipboardPopover } from "./clipboard.tsx";
import { isPhone, useTouchCapable } from "./device.ts";
import { compactDisplayMenu, displayMenu, type DisplayMenuState } from "./display-menu.ts";
import { AUTO_FIT, displayScale, initialLock, initialTrackpad, parseScaling, scaledSize } from "./display-prefs.ts";
import { ActivityOverlay } from "./overlay.tsx";
import {
  configureDisplay,
  defaultQuality,
  focusFrame,
  frameAllow,
  frameSrc,
  httpsHelp,
  insecureContext,
  isQualityId,
  jpegOnly,
  keyCombos,
  lockSize,
  onClipboard,
  presetOf,
  type PresetId,
  qualityOf,
  type QualityId,
  remoteSize,
  screenOf,
  selkiesScreenGeometry,
  sendClipboard,
  sendQuality,
} from "./selkies.ts";
import { tabShortcutListener } from "./tab-shortcuts.ts";
import { barButton, type BarSlots, ConnDot, pressed, useTabHooks } from "./tabs.tsx";
import { TouchControls } from "./touch-controls.tsx";
import { DropZone } from "./upload.tsx";
import { useDisplay } from "./use-display.ts";
import { ZoomViewport } from "./zoom-viewport.tsx";

// Remembered per browser.
const QUALITY_KEY = "deskyard.quality";
const LOCK_SIZE_KEY = "deskyard.lockSize";
const SCALING_KEY = "deskyard.scaling";
const TRACKPAD_KEY = "deskyard.trackpad";

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

export function Centered({ children }: { children: ComponentChildren }) {
  return <div class="flex flex-1 flex-col items-center justify-center gap-3.5 px-6 text-center text-[15px] text-[#c5cdd8]">{children}</div>;
}

/**
 * The Desktop tab: mounted only while it's shown, so a hidden desktop doesn't
 * stream. An observer's desktop is view only: the supervisor drops its input
 * before it reaches the desktop, and the page offers none (a cover over the
 * frame takes the pointer, the frame never takes focus, the bar has no input
 * controls) and never asks to change the display.
 */
export function Desktop({ environment: c, slots, selectShortcut, onFrame }: { environment: Environment; slots: BarSlots; selectShortcut: (index: number) => boolean; onFrame: (frame: HTMLIFrameElement | null) => void }) {
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
  const menu: DisplayMenuState = {
    owner,
    locked,
    scaling,
    ratio,
    quality,
    live,
    onLock: toggleLock,
    onResolution: chooseResolution,
    onScaling: (value) => {
      if (!owner) return;
      setScaling(value);
      store(SCALING_KEY, value);
      setZoom(1);
      refocus();
    },
    onQuality: chooseQuality,
  };
  const showing = running;
  // The bar's input controls: none for an observer.
  const controlling = showing && !observing;
  // Phones and narrow tablets need these actions in ⋯.
  const moreItems: MenuItem[] = controlling ? compactDisplayMenu(menu, keyItems.map((k) => ({ ...k, label: <>Send {k.label}</> }))) : [];
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
                items={displayMenu(menu)}
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

