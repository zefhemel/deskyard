import { useEffect, useRef, useState } from "preact/hooks";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import "./terminal.css";
import { api } from "../api.ts";
import { useEnvironment } from "../live.tsx";
import { TerminalConnection, type Socket } from "./connection.ts";
import {
  binaryBytes,
  encodeKey,
  OneShotModifiers,
  type Modifiers,
} from "./keys.ts";
import { KeyBar } from "./key-bar.tsx";
import { SessionPicker } from "./session-picker.tsx";
import { terminalLayout } from "./viewport.ts";

export function TerminalPage({ name }: { name: string }) {
  const environment = useEnvironment(name);
  const initial = new URLSearchParams(location.search).get("session") ?? "main";
  const [session, setSession] = useState(initial);
  const [revision, setRevision] = useState(0);
  const shell = useRef<HTMLDivElement>(null);
  const insets = useRef<HTMLDivElement>(null);
  const generation = useRef("");
  useEffect(() => {
    document.title = `${name} · Terminal`;
    const viewport = window.visualViewport;
    const size = () => {
      const element = shell.current;
      if (!element) return;
      const height = viewport?.height ?? window.innerHeight;
      element.style.height = `${height}px`;
      const safeArea = insets.current && getComputedStyle(insets.current);
      const layout = terminalLayout(height,
        parseFloat(safeArea?.paddingTop ?? "0"),
        parseFloat(safeArea?.paddingBottom ?? "0"));
      element.dataset.compact = String(layout !== "full");
      element.dataset.layout = layout;
      element.style.top = `${viewport?.offsetTop ?? 0}px`;
    };
    size();
    viewport?.addEventListener("resize", size);
    viewport?.addEventListener("scroll", size);
    window.addEventListener("resize", size);
    return () => {
      viewport?.removeEventListener("resize", size);
      viewport?.removeEventListener("scroll", size);
      window.removeEventListener("resize", size);
    };
  }, [name]);
  const valid = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(session);
  return (
    <div ref={shell} data-terminal-page class="terminal-page">
      <div ref={insets} class="terminal-insets" aria-hidden="true" />
      <div class="terminal-too-short" role="status">Rotate to portrait or dismiss the keyboard to use the terminal.</div>
      <header class="terminal-header">
        <a href="/" aria-label="Environments">
          ‹
        </a>
        <strong>{name}</strong>
        <a href={`/${name}/`}>Desktop</a>
        <SessionPicker key={name} name={name} session={session} onSelect={value => {
          history.replaceState(null, "", `/${name}/terminal/?session=${encodeURIComponent(value)}`);
          setSession(value);
          setRevision(0);
        }} />
      </header>
      {!valid ? (
        <p class="terminal-notice">
          Invalid session. Use 1–64 letters, digits, underscores or hyphens,
          starting with a letter or digit.
        </p>
      ) : environment === undefined ? (
        <p class="terminal-notice">Loading environment…</p>
      ) : !environment ? (
        <p class="terminal-notice">
          Environment not found, or you no longer have access.
        </p>
      ) : !environment.terminal ? (
        <p class="terminal-notice">
          Upgrade this environment's image to use shared terminals.{" "}
          <a href={`/environments/${name}`}>Environment settings</a>
        </p>
      ) : environment.state !== "running" ? (
        <p class="terminal-notice">
          Environment is {environment.state}.{" "}
          <a href={`/environments/${name}`}>Open environment details</a>
        </p>
      ) : (
        <TerminalPane
          key={`${name}:${session}:${revision}`}
          name={name}
          generation={generation}
          session={session}
          reopen={() => setRevision((n) => n + 1)}
        />
      )}
    </div>
  );
}

function TerminalPane({
  name,
  session,
  generation,
  reopen,
}: {
  name: string;
  session: string;
  generation: { current: string };
  reopen: () => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const terminal = useRef<Terminal | null>(null);
  const connection = useRef<TerminalConnection | null>(null);
  const modifiers = useRef(new OneShotModifiers());
  const [pressed, setPressed] = useState<Modifiers>(modifiers.current.value);
  const [state, setState] = useState("connecting");
  const [message, setMessage] = useState("");
  const [warning, setWarning] = useState("");
  useEffect(() => {
    if (!host.current) return;
    const term = new Terminal({
      fontFamily: '"IBM Plex Mono", monospace',
      fontSize: 16,
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
  }, [name, session]);
  return (
    <>
      <div class="terminal-status" role="status">
        <span data-terminal-state={state}>
          {state === "connected"
            ? "Connected"
            : message ||
              (state === "retrying" ? "Reconnecting…" : "Connecting…")}
        </span>
        {(state === "exited" || state === "error") && (
          <button type="button" onClick={reopen}>
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
        onKey={(key, ctrl) => {
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
        }}
      />
    </>
  );
}
