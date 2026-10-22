/*
 * Talking to the KasmVNC web client in the desktop page's frame.
 *
 * Read from the client's source in the image (/usr/share/kasmvnc/www/vnc.html
 * and its bundled assets/ui-*.js, the minified app/ui.js):
 *
 * The client counts as framed when `window.self !== window.top`. It reads
 * settings with `hr(name)`: the URL hash first, then the query string, as
 * strings; checkbox settings treat "0", "no" and "false" as false.
 *
 * Query parameters:
 * - `show_control_bar`: the control bar is hidden only when this parameter is
 *   ABSENT. The check is `hr("show_control_bar") || hide()`, and the string "0"
 *   is truthy, so `show_control_bar=0` would SHOW the bar. We leave it out.
 * - Framed without `show_control_bar`, the client defaults clipboard_up,
 *   clipboard_down and clipboard_seamless to false, resize to "off" and
 *   enable_webp to false. Query parameters win over those defaults, so we pass
 *   `clipboard_up=1&clipboard_down=1` (without them there is no `clipboardrx`
 *   and `clipboardsnd` is ignored), `clipboard_seamless=1` (copy and paste
 *   straight to the system clipboard over HTTPS; the client turns it off itself
 *   on Safari and Firefox) and `resize=remote`.
 * - `autoconnect=1`, `reconnect=1`, `path=<name>/websockify`.
 *
 * Frame → page, `window.parent.postMessage({action, value}, "*")`:
 * - {action: "noVNC_initialized", value: null}
 * - {action: "connection_state", value: "init" | "connecting" | "connected" |
 *   "disconnecting" | "disconnected" | "reconnecting"}
 * - {action: "disconnectrx", value: reason (string or undefined)}: the RFB
 *   connection closed. After a graceful server notice the frame goes to
 *   disconnected.html 3 s later.
 * - {action: "clipboardrx", value: text}: the desktop's clipboard changed.
 * - {action: "idle_session_timeout", value: "Idle session timeout exceeded"}:
 *   no input for idle_disconnect minutes (default 20); the frame goes to
 *   disconnected.html 10 s later.
 * - Also control_open, control_close, fullscreen, togglenav, enable_audio,
 *   can_control_displays, smartcard_status: ignored here.
 *
 * Page → frame, `frame.contentWindow.postMessage({action, ...}, origin)`; the
 * client only checks that `data.action` is set:
 * - {action: "clipboardsnd", value: text}: puts text on the desktop's clipboard.
 * - {action: "setvideoquality", qualityLevel: 0..5, frameRate?: number}:
 *   0 Static, 1 Low, 2 Medium, 3 High, 4 Extreme, 5 Lossless (`value` works
 *   instead of `qualityLevel`). The client saves it in its own settings.
 * - Not used: resize, set_resolution, enable_hidpi, terminate, enable_webrtc, ….
 *
 * Both are on the supervisor's origin, so we only accept messages whose source
 * is our frame and whose origin is ours.
 */

export type ConnectionState = "init" | "connecting" | "connected" | "disconnecting" | "disconnected" | "reconnecting";

export type FrameMessage =
  | { action: "noVNC_initialized" }
  | { action: "connection_state"; value: ConnectionState }
  | { action: "disconnectrx"; value?: string }
  | { action: "clipboardrx"; value: string }
  | { action: "idle_session_timeout" };

const states: ConnectionState[] = ["init", "connecting", "connected", "disconnecting", "disconnected", "reconnecting"];

/** The frame's URL for a desktop. */
export function frameSrc(name: string): string {
  const q = new URLSearchParams({
    path: `${name}/websockify`,
    autoconnect: "1",
    resize: "remote",
    reconnect: "1",
    clipboard_up: "1",
    clipboard_down: "1",
    clipboard_seamless: "1",
  });
  return `/${name}/vnc/vnc.html?${q.toString()}`;
}

/** Parses a message from the frame; null for anything we don't handle. */
export function parseFrameMessage(data: unknown): FrameMessage | null {
  if (!data || typeof data !== "object" || !("action" in data)) return null;
  const d = data as { action: unknown; value?: unknown };
  switch (d.action) {
    case "noVNC_initialized":
      return { action: "noVNC_initialized" };
    case "connection_state":
      return typeof d.value === "string" && (states as string[]).includes(d.value)
        ? { action: "connection_state", value: d.value as ConnectionState }
        : null;
    case "disconnectrx":
      return { action: "disconnectrx", value: typeof d.value === "string" ? d.value : undefined };
    case "clipboardrx":
      return typeof d.value === "string" ? { action: "clipboardrx", value: d.value } : null;
    case "idle_session_timeout":
      return { action: "idle_session_timeout" };
    default:
      return null;
  }
}

function send(frame: HTMLIFrameElement | null, msg: Record<string, unknown>): void {
  frame?.contentWindow?.postMessage(msg, location.origin);
}

export function sendClipboard(frame: HTMLIFrameElement | null, text: string): void {
  send(frame, { action: "clipboardsnd", value: text });
}

export function sendQuality(frame: HTMLIFrameElement | null, level: number): void {
  send(frame, { action: "setvideoquality", qualityLevel: level });
}

export const qualities = [
  { level: 1, label: "Low", hint: "for slow connections" },
  { level: 2, label: "Medium", hint: "" },
  { level: 3, label: "High", hint: "" },
  { level: 4, label: "Extreme", hint: "" },
  { level: 5, label: "Lossless", hint: "for fast networks" },
];

/** Keys the browser would otherwise take, in xdotool syntax for /keys. */
export const keyCombos = [
  { label: "Ctrl+Alt+Del", keys: "ctrl+alt+Delete" },
  { label: "Super", keys: "super" },
  { label: "Alt+Tab", keys: "alt+Tab" },
  { label: "Ctrl+W", keys: "ctrl+w" },
  { label: "Esc", keys: "Escape" },
  { label: "F11", keys: "F11" },
];

/**
 * Gives keyboard focus back to the desktop. The client's keyboard listens on
 * its screen canvas (the one with tabIndex -1), as RFB.focus() does; same
 * origin, so we can reach into the frame.
 */
export function focusFrame(frame: HTMLIFrameElement | null): void {
  if (!frame) return;
  try {
    frame.contentWindow?.focus();
    const target = frame.contentDocument?.querySelector<HTMLElement>('canvas[tabindex="-1"]');
    target?.focus({ preventScroll: true });
  } catch {
    frame.focus();
  }
}
