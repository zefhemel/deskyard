export interface EventSocket {
  onopen: ((e: Event) => void) | null;
  onmessage: ((e: MessageEvent) => void) | null;
  onclose: ((e: CloseEvent) => void) | null;
  onerror: ((e: Event) => void) | null;
  close(): void;
}

interface Options {
  socket: (url: string) => EventSocket;
  schedule: (f: () => void, ms: number) => () => void;
}

/** Named UI events over a reconnecting WebSocket, outside HTTP/1's fetch pool. */
export class EventFeed {
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private listeners = new Map<string, Set<(e: MessageEvent<string>) => void>>();
  private wire: EventSocket | null = null;
  private stopped = false;
  private attempt = 0;
  private cancelRetry: (() => void) | undefined;
  private url: string;
  private options: Options;

  constructor(url: string, options: Options = {
    socket: url => new WebSocket(url),
    schedule: (f, ms) => { const timer = setTimeout(f, ms); return () => clearTimeout(timer); },
  }) {
    this.url = url;
    this.options = options;
    this.connect();
  }

  addEventListener(type: string, listener: (e: MessageEvent<string>) => void) {
    let listeners = this.listeners.get(type);
    if (!listeners) this.listeners.set(type, listeners = new Set());
    listeners.add(listener);
  }

  private connect() {
    if (this.stopped) return;
    let wire: EventSocket;
    try { wire = this.options.socket(this.url); }
    catch { this.retry(); return; }
    this.wire = wire;
    const current = () => !this.stopped && this.wire === wire;
    wire.onopen = () => {
      if (!current()) return;
      this.attempt = 0;
      this.onopen?.();
    };
    wire.onmessage = e => {
      if (!current() || typeof e.data !== "string") return;
      let message: { type: string; data: unknown };
      try {
        message = JSON.parse(e.data);
        if (!message || typeof message.type !== "string" || !("data" in message)) return;
      } catch { return; }
      const event = new MessageEvent<string>(message.type, { data: JSON.stringify(message.data) });
      for (const listener of this.listeners.get(message.type) ?? []) listener(event);
    };
    wire.onerror = () => { /* close is the definitive notification */ };
    wire.onclose = () => {
      if (!current()) return;
      this.wire = null;
      this.retry();
    };
  }

  private retry() {
    this.onerror?.();
    if (this.stopped) return;
    const delay = Math.min(500 * 2 ** Math.min(this.attempt++, 5), 10000);
    this.cancelRetry = this.options.schedule(() => {
      this.cancelRetry = undefined;
      this.connect();
    }, delay);
  }

  close() {
    this.stopped = true;
    this.cancelRetry?.();
    this.wire?.close();
    this.wire = null;
    this.listeners.clear();
  }
}

export function watchUIEvents(path: string): EventFeed {
  const url = new URL(path, window.location.href);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  return new EventFeed(url.href);
}
