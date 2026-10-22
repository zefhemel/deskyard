// The desktop page at /<name>/: a thin bar of ours above KasmVNC's web
// client in a frame. The protocol between the two is described in kasm.ts.
import type { ComponentChildren } from "preact";
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { api, type Desktop } from "../api.ts";
import { Avatar, Avatars } from "../components/avatar.tsx";
import { power } from "../components/desktop-menu.tsx";
import { Menu, type MenuItem } from "../components/menu.tsx";
import { DownloadDialog } from "../dialogs/download.tsx";
import { ViewersPopover } from "../components/viewers-popover.tsx";
import { notify, notifyError } from "../components/toast.tsx";
import { Button, cx } from "../components/ui.tsx";
import { useDesktop, useLive } from "../live.tsx";
import { names } from "../lib/format.ts";
import { DropZone } from "./upload.tsx";
import { ClipboardPopover } from "./clipboard.tsx";
import { type ConnectionState, focusFrame, frameSrc, keyCombos, parseFrameMessage, qualities, sendClipboard, sendQuality } from "./kasm.ts";

const QUALITY_KEY = "cc.desktop.quality";

function storedQuality(): number {
  try {
    return Number(localStorage.getItem(QUALITY_KEY) ?? 2) || 2;
  } catch {
    return 2;
  }
}

type Conn = ConnectionState | "idle";

const connLabels: Record<Conn, string> = {
  init: "Connecting",
  connecting: "Connecting",
  connected: "Connected",
  disconnecting: "Disconnecting",
  disconnected: "Disconnected",
  reconnecting: "Reconnecting",
  idle: "Disconnected while idle",
};

const barButton =
  "inline-flex cursor-pointer items-center gap-1.5 rounded-[7px] border-0 bg-transparent px-2.5 py-[7px] text-[13.5px] font-semibold whitespace-nowrap text-bar-text no-underline hover:bg-bar-hover hover:text-white";

export function DesktopPage({ name }: { name: string }) {
  const c = useDesktop(name);
  useEffect(() => {
    document.title = name;
  }, [name]);
  return (
    <div class="fixed inset-0 flex flex-col bg-glass">
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
  const [conn, setConn] = useState<Conn>("connecting");
  const [frameKey, setFrameKey] = useState(0);
  const [remoteClip, setRemoteClip] = useState<string | null>(null);
  const [clipOpen, setClipOpen] = useState(false);
  const [viewersOpen, setViewersOpen] = useState(false);
  const [downloadOpen, setDownloadOpen] = useState(false);
  const [quality, setQuality] = useState(storedQuality);
  const running = c.state === "running";

  const refocus = useCallback(() => {
    // After the click that closed a menu has landed.
    window.setTimeout(() => focusFrame(frame.current), 0);
  }, []);

  // Messages from the frame.
  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      if (e.origin !== location.origin || !frame.current || e.source !== frame.current.contentWindow) return;
      const m = parseFrameMessage(e.data);
      if (!m) return;
      switch (m.action) {
        case "connection_state":
          setConn(m.value);
          if (m.value === "connected") focusFrame(frame.current);
          break;
        case "disconnectrx":
          setConn((s) => (s === "reconnecting" ? s : "disconnected"));
          break;
        case "idle_session_timeout":
          setConn("idle");
          break;
        case "clipboardrx":
          setRemoteClip(m.value);
          break;
        case "noVNC_initialized":
          break;
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, []);

  // A fresh frame when the desktop comes back.
  const wasRunning = useRef(running);
  useEffect(() => {
    if (running && !wasRunning.current) {
      setConn("connecting");
      setFrameKey((k) => k + 1);
    }
    wasRunning.current = running;
  }, [running]);

  useJoinToasts(c.viewers, me.username);

  const reconnect = () => {
    setConn("connecting");
    setFrameKey((k) => k + 1);
  };
  const sendKeys = (keys: string) => {
    api.keys(c.name, keys).catch(notifyError);
    refocus();
  };
  const chooseQuality = (level: number) => {
    setQuality(level);
    try {
      localStorage.setItem(QUALITY_KEY, String(level));
    } catch {
      // private mode
    }
    sendQuality(frame.current, level);
  };
  const closeViewers = useCallback(() => {
    setViewersOpen(false);
    refocus();
  }, [refocus]);
  const closeDownload = useCallback(() => {
    setDownloadOpen(false);
    refocus();
  }, [refocus]);
  const closeClip = useCallback(() => {
    setClipOpen(false);
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
    checked: q.level === quality,
    onSelect: () => chooseQuality(q.level),
  }));
  const phoneOnly = "sm:hidden";
  const moreItems: MenuItem[] = [
    ...(running
      ? [
          { label: "Restart", onSelect: () => power(c.name, "restart") },
          { label: "Stop", onSelect: () => power(c.name, "stop") },
        ]
      : []),
    // Temporary entry point until the Download button joins the bar.
    ...(running ? [{ label: "Download…", onSelect: () => setDownloadOpen(true), separated: true }] : []),
    { label: "Desktop page", onSelect: () => route(`/desktops/${c.name}`), separated: running },
    ...(me.admin ? [{ label: "Settings", onSelect: () => route(`/desktops/${c.name}?tab=settings`) }] : []),
    // On a phone the bar has no room for these.
    ...(running
      ? [
          ...keyItems.map((k, i) => ({ ...k, label: <>Send {k.label}</>, separated: i === 0, class: phoneOnly })),
          ...qualities.map((q, i) => ({
            label: `Quality: ${q.label}`,
            checked: q.level === quality,
            onSelect: () => chooseQuality(q.level),
            separated: i === 0,
            class: phoneOnly,
          })),
        ]
      : []),
  ];

  const dot =
    conn === "connected" ? "bg-[#3ac08a]" : conn === "disconnected" || conn === "idle" ? "bg-[#e0685b]" : "bg-busy animate-pulse";

  return (
    <>
      <div class="relative flex h-[46px] flex-none items-center gap-1.5 bg-bar px-2.5 text-sm text-[#e6ebf1]">
        <a href="/" class={barButton} title="Desktops">
          ‹ <span class="max-sm:hidden">Desktops</span>
        </a>
        <span class="mx-1.5 h-[22px] w-px bg-[#2e3d52] max-sm:mx-0.5" />
        <span class="mx-0.5 truncate text-[15px] font-bold text-white">{c.name}</span>
        {running ? (
          <span class="inline-flex items-center gap-1.5 text-[13px] whitespace-nowrap text-bar-muted">
            <span class={cx("h-2 w-2 rounded-full", dot)} />
            <span class="max-sm:hidden">{connLabels[conn]}</span>
          </span>
        ) : null}
        {running && (conn === "disconnected" || conn === "idle") && (
          <button type="button" class={barButton} onClick={reconnect}>
            Reconnect
          </button>
        )}
        {(c.viewers.length > 0 || c.forwards.length > 0) && (
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
              title={names(c.viewers)}
              onClick={() => (viewersOpen ? closeViewers() : setViewersOpen(true))}
            >
              {c.viewers.length > 0 && (
                <>
                  <Avatars names={c.viewers} ring="var(--color-bar)" />
                  <span class="truncate max-md:hidden">{names(c.viewers)}</span>
                </>
              )}
              {c.forwards.length > 0 && <span class="whitespace-nowrap">⇄ {c.forwards.length}</span>}
            </button>
          </>
        )}
        <div class="ml-auto flex items-center gap-0.5">
          {running && (
            <>
              <button
                type="button"
                data-clipboard-toggle
                aria-expanded={clipOpen}
                class={cx(barButton, clipOpen && "bg-bar-hover text-white")}
                onClick={() => (clipOpen ? closeClip() : setClipOpen(true))}
              >
                Clipboard
              </button>
              <Menu
                class="max-sm:hidden"
                items={keyItems}
                onClose={refocus}
                trigger={({ toggle, open }) => (
                  <button type="button" aria-haspopup="menu" aria-expanded={open} class={cx(barButton, open && "bg-bar-hover text-white")} onClick={toggle}>
                    Keys <span class="text-[10px]">▾</span>
                  </button>
                )}
              />
              <Menu
                class="max-sm:hidden"
                items={qualityItems}
                onClose={refocus}
                trigger={({ toggle, open }) => (
                  <button type="button" aria-haspopup="menu" aria-expanded={open} class={cx(barButton, open && "bg-bar-hover text-white")} onClick={toggle}>
                    Quality <span class="text-[10px]">▾</span>
                  </button>
                )}
              />
            </>
          )}
          <Menu
            items={moreItems}
            onClose={refocus}
            trigger={({ toggle, open }) => (
              <button type="button" aria-label="More" aria-haspopup="menu" aria-expanded={open} class={cx(barButton, open && "bg-bar-hover text-white")} onClick={toggle}>
                ⋯
              </button>
            )}
          />
        </div>
        {running && <DropZone desktop={c.name} frame={frame} frameKey={frameKey} />}
        {downloadOpen && running && <DownloadDialog desktop={c} onClose={closeDownload} />}
        {viewersOpen && (c.viewers.length > 0 || c.forwards.length > 0) && (
          <ViewersPopover viewers={c.viewers} forwards={c.forwards} onClose={closeViewers} />
        )}
        {clipOpen && running && (
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
      {running ? (
        <iframe
          key={frameKey}
          ref={frame}
          src={frameSrc(c.name)}
          title={`${c.name}'s desktop`}
          allow="clipboard-read; clipboard-write"
          class="block w-full flex-1 border-0"
        />
      ) : (
        <Stopped desktop={c} />
      )}
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
