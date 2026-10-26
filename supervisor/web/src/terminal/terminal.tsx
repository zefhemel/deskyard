import { createPortal } from "preact/compat";
import { useEffect, useRef, useState } from "preact/hooks";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import "./terminal.css";
import { api } from "../api.ts";
import { Menu, type MenuItem } from "../components/menu.tsx";
import { notify } from "../components/toast.tsx";
import { cx } from "../components/ui.tsx";
import { ClipboardIcon, KeyboardIcon, TextSizeIcon } from "../desktop/bar-icons.tsx";
import { barButton, type BarSlots, type Conn, ConnDot, panelId, pressed as pressedClass, tabId, useTabHooks } from "../desktop/tabs.tsx";
import { TerminalConnection, type Socket } from "./connection.ts";
import {
  binaryBytes,
  encodeKey,
  OneShotModifiers,
  type Modifiers,
} from "./keys.ts";
import { KeyBar, terminalKeys } from "./key-bar.tsx";
import { terminalLayout } from "./viewport.ts";

// Text size, remembered per browser and shared by every open terminal tab.
const FONT_KEY = "deskyard.terminal.fontSize";
const FONT_EVENT = "deskyard-terminal-font";
const fontSizes = [12, 14, 16, 18, 20];
function loadFontSize(): number {
  try {
    const v = Number(localStorage.getItem(FONT_KEY));
    return fontSizes.includes(v) ? v : 16;
  } catch {
    return 16;
  }
}
function chooseFontSize(size: number) {
  try {
    localStorage.setItem(FONT_KEY, String(size));
  } catch {
    // not remembered
  }
  window.dispatchEvent(new CustomEvent(FONT_EVENT, { detail: size }));
}

const finePointer = () => !matchMedia("(pointer: coarse)").matches;

const connOf = (state: string): Conn => (state === "connected" ? "connected" : state === "connecting" || state === "retrying" ? "connecting" : "disconnected");

export interface TerminalPaneProps {
  name: string;
  session: string;
  /** The shown tab; hidden panes stay mounted and connected. */
  active: boolean;
  /** An explicit open: the first attach may create the session. */
  create: boolean;
  slots: BarSlots;
  /** Whether the session is still listed, checked before reconnecting. */
  alive: () => boolean;
  onState: (session: string, state: string) => void;
  /** The session ended ("ended"), or this attachment's shell exited ("exited"). */
  onEnded: (session: string, how: "ended" | "exited") => void;
}

/** One terminal tab: an xterm attached to a shared session, with its own bar controls. */
export function TerminalPane({ name, session, active, create, slots, alive, onState, onEnded }: TerminalPaneProps) {
  const pane = useRef<HTMLDivElement>(null);
  const insets = useRef<HTMLDivElement>(null);
  const generation = useRef("");
  const [revision, setRevision] = useState(0);
  const callbacks = useRef({ alive, onState, onEnded });
  callbacks.current = { alive, onState, onEnded };
  const host = useRef<HTMLDivElement>(null);
  const terminal = useRef<Terminal | null>(null);
  const connection = useRef<TerminalConnection | null>(null);
  const modifiers = useRef(new OneShotModifiers());
  const [pressed, setPressed] = useState<Modifiers>(modifiers.current.value);
  const [state, setState] = useState("connecting");
  const [message, setMessage] = useState("");
  const [warning, setWarning] = useState("");
  const [fontSize, setFontSize] = useState(loadFontSize);
  const measureRef = useRef<() => void>(() => {});

  // The layout tier follows the visible page's height, bar included: below the
  // compact budget ("ultra") the bar moves beside the key row and below the
  // minimum it is covered (terminal.css), so the pane's own height would depend
  // on the tier it picks.
  useEffect(() => {
    const element = pane.current;
    if (!element) return;
    const page = element.closest<HTMLElement>("[data-desktop-page]");
    const size = () => {
      if (element.hidden) return;
      const safeArea = insets.current && getComputedStyle(insets.current);
      const layout = terminalLayout(page?.clientHeight ?? element.clientHeight + 46,
        parseFloat(safeArea?.paddingTop ?? "0"),
        parseFloat(safeArea?.paddingBottom ?? "0"));
      element.dataset.compact = String(layout !== "full");
      element.dataset.layout = layout;
    };
    size();
    const observer = new ResizeObserver(size);
    observer.observe(element);
    if (page) observer.observe(page);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const change = (e: Event) => setFontSize((e as CustomEvent<number>).detail);
    window.addEventListener(FONT_EVENT, change);
    return () => window.removeEventListener(FONT_EVENT, change);
  }, []);
  useEffect(() => {
    const term = terminal.current;
    if (!term || term.options.fontSize === fontSize) return;
    term.options.fontSize = fontSize;
    measureRef.current();
  }, [fontSize]);

  useEffect(() => {
    if (active && finePointer()) requestAnimationFrame(() => terminal.current?.focus());
  }, [active]);

  useEffect(() => {
    if (!host.current) return;
    const term = new Terminal({
      fontFamily: '"IBM Plex Mono", monospace',
      fontSize: loadFontSize(),
      cursorBlink: true,
      theme: {
        background: "#101822",
        foreground: "#e6ebf1",
        cursor: "#9ec0ff",
      },
      scrollback: 3000,
      allowProposedApi: false,
      linkHandler: null,
      windowOptions: {},
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host.current);
    terminal.current = term;
    term.textarea?.setAttribute("aria-label", "Terminal input");
    // Consume OSC 52 before any clipboard integration. No link addon or title handler.
    const clipboard = term.parser.registerOscHandler(52, () => true);
    let disposed = false;
    let composing = false;
    let compositionTimer: ReturnType<typeof setTimeout> | undefined;
    const clearModifiers = () => {
      modifiers.current.reset();
      setPressed(modifiers.current.value);
    };
    term.element?.addEventListener("paste", clearModifiers, true);
    const compositionStart = () => {
      clearModifiers();
      clearTimeout(compositionTimer);
      composing = true;
    };
    const compositionEnd = () => {
      compositionTimer = setTimeout(() => {
        composing = false;
      }, 0);
    };
    term.textarea?.addEventListener("compositionstart", compositionStart);
    term.textarea?.addEventListener("compositionend", compositionEnd);
    let frame = 0;
    const measure = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        if (disposed || !host.current || host.current.clientHeight < 20) return;
        const proposed = fit.proposeDimensions();
        if (!proposed) return;
        const cols = Math.max(2, Math.min(500, proposed.cols));
        const rows = Math.max(1, Math.min(300, proposed.rows));
        if (cols !== term.cols || rows !== term.rows) {
          term.resize(cols, rows);
          connection.current?.resize();
        }
      });
    };
    measureRef.current = measure;
    // Initial fit precedes the first WebSocket dimensions; later fitting is observer driven.
    const proposed = fit.proposeDimensions();
    if (proposed)
      term.resize(
        Math.max(2, Math.min(500, proposed.cols)),
        Math.max(1, Math.min(300, proposed.rows)),
      );
    const endpoint = new URL(
      `/api/environments/${encodeURIComponent(name)}/terminal`,
      location.href,
    );
    endpoint.protocol = location.protocol === "https:" ? "wss:" : "ws:";
    endpoint.searchParams.set("session", session);
    const transport = new TerminalConnection({
      url: endpoint.href,
      // Reopening only joins: the session may have ended meanwhile.
      create: create && revision === 0,
      alive: () => callbacks.current.alive(),
      initialGeneration: generation.current,
      generation: (value) => {
        generation.current = value;
      },
      size: () => [term.cols, term.rows],
      socket: (url) => new WebSocket(url) as unknown as Socket,
      preflight: async () => {
        const current = await api.environment(name);
        if (!current.terminal)
          throw new Error(
            "Upgrade this environment's image to use shared terminals.",
          );
        if (current.state !== "running")
          throw new Error(
            `Environment is ${current.state}. Open its details to start it.`,
          );
      },
      // Drain old writes before resetting parser/private modes; new output follows this barrier.
      reset: () =>
        term.write("", () => {
          if (!disposed) term.reset();
        }),
      write: (bytes, done) => term.write(bytes, done),
      warning: setWarning,
      state: (next, text) => {
        if (disposed) return;
        setState(next);
        setMessage(text ?? "");
        callbacks.current.onState(session, next);
        if (next === "ended" || next === "exited") callbacks.current.onEnded(session, next);
        if (next !== "connected") {
          modifiers.current.reset();
          setPressed(modifiers.current.value);
        }
      },
    });
    connection.current = transport;
    const data = term.onData((text) => {
      const transformed = modifiers.current.consume(text, composing);
      setPressed(modifiers.current.value);
      transport.input(new TextEncoder().encode(transformed));
    });
    const binary = term.onBinary((text) => {
      modifiers.current.reset();
      setPressed(modifiers.current.value);
      transport.input(binaryBytes(text));
    });
    const observer = new ResizeObserver(measure);
    observer.observe(host.current);
    void document.fonts.ready.then(() => {
      if (!disposed) measure();
    });
    const resume = () => transport.resume();
    window.addEventListener("online", resume);
    window.addEventListener("pageshow", resume);
    void transport.start();
    return () => {
      disposed = true;
      clearTimeout(compositionTimer);
      term.element?.removeEventListener("paste", clearModifiers, true);
      term.textarea?.removeEventListener("compositionstart", compositionStart);
      term.textarea?.removeEventListener("compositionend", compositionEnd);
      cancelAnimationFrame(frame);
      observer.disconnect();
      window.removeEventListener("online", resume);
      window.removeEventListener("pageshow", resume);
      transport.stop();
      data.dispose();
      binary.dispose();
      clipboard.dispose();
      term.dispose();
      terminal.current = null;
      connection.current = null;
    };
  }, [name, session, revision]);

  const sendKey = (key: string, ctrl?: boolean) => {
    let text: string;
    if (key === "History") {
      modifiers.current.reset();
      text = encodeKey(key);
    } else if (ctrl) {
      modifiers.current.reset();
      text = encodeKey(key, { ctrl: true, alt: false });
    } else
      text = modifiers.current.key(
        key,
        terminal.current?.modes.applicationCursorKeysMode,
      );
    setPressed(modifiers.current.value);
    connection.current?.input(new TextEncoder().encode(text));
  };
  const refocus = () => {
    if (finePointer()) window.setTimeout(() => terminal.current?.focus(), 0);
  };
  const paste = async () => {
    try {
      const text = await navigator.clipboard.readText();
      if (text) terminal.current?.paste(text);
    } catch {
      notify("Couldn't read the clipboard here. Paste with Ctrl+Shift+V (⌘V on a Mac) instead.", { error: true });
    }
    refocus();
  };
  const keyItems: MenuItem[] = terminalKeys.map(([key, , label]) => ({
    label,
    disabled: state !== "connected",
    onSelect: () => sendKey(key, label.startsWith("Ctrl+")),
  }));
  const sizeItems: MenuItem[] = fontSizes.map((size) => ({
    label: `Text size: ${size}px`,
    checked: size === fontSize,
    onSelect: () => chooseFontSize(size),
  }));
  // On phones Text size is in ⋯; the on-screen key bar has the keys.
  useTabHooks(slots, active, {
    menu: () => sizeItems.map((k, i) => ({ ...k, separated: i === 0, class: "sm:hidden" })),
    refocus,
  });

  return (
    <div ref={pane} role="tabpanel" id={panelId(session)} aria-labelledby={tabId(session)} data-terminal-pane={session} class="terminal-pane" hidden={!active}>
      {active && slots.dot && createPortal(<ConnDot state={connOf(state)} />, slots.dot)}
      {active && slots.controls && createPortal(
        <>
          <button type="button" aria-label="Paste" title="Paste the clipboard into the terminal" class={cx(barButton, "px-2")} disabled={state !== "connected"} onClick={() => void paste()}>
            <ClipboardIcon />
          </button>
          <Menu
            class="max-sm:hidden"
            items={keyItems}
            onClose={refocus}
            trigger={({ toggle, open }) => (
              <button type="button" aria-label="Keys" aria-haspopup="menu" aria-expanded={open} title="Send keys" class={cx(barButton, "px-2", open && pressedClass)} onClick={toggle}>
                <KeyboardIcon />
                <span aria-hidden="true" class="text-[10px]">▾</span>
              </button>
            )}
          />
          <Menu
            class="max-sm:hidden"
            items={sizeItems}
            onClose={refocus}
            trigger={({ toggle, open }) => (
              <button type="button" aria-label="Text size" aria-haspopup="menu" aria-expanded={open} title="Text size" class={cx(barButton, "px-2", open && pressedClass)} onClick={toggle}>
                <TextSizeIcon />
                <span aria-hidden="true" class="text-[10px]">▾</span>
              </button>
            )}
          />
        </>,
        slots.controls,
      )}
      <div ref={insets} class="terminal-insets" aria-hidden="true" />
      <div class="terminal-too-short" role="status">Rotate to portrait or dismiss the keyboard to use the terminal.</div>
      <div class="terminal-status" role="status">
        <span data-terminal-state={state}>
          {state === "connected"
            ? "Connected"
            : message ||
              (state === "retrying" ? "Reconnecting…" : "Connecting…")}
        </span>
        {(state === "exited" || state === "error") && (
          <button type="button" onClick={() => setRevision((n) => n + 1)}>
            Reopen terminal
          </button>
        )}
      </div>
      {warning && (
        <div class="terminal-warning" role="alert">
          {warning}
          <button
            type="button"
            aria-label="Dismiss terminal notice"
            onClick={() => setWarning("")}
          >
            ×
          </button>
        </div>
      )}
      <div class="terminal-scroll">
        <div ref={host} class="terminal-host" />
      </div>
      <KeyBar
        modifiers={pressed}
        disabled={state !== "connected"}
        onModifier={(key) => {
          modifiers.current.toggle(key);
          setPressed(modifiers.current.value);
        }}
        onFocus={() => terminal.current?.focus()}
        onKey={sendKey}
      />
    </div>
  );
}
