export interface Socket {
  readyState: number;
  bufferedAmount: number;
  binaryType: string;
  onopen: (() => void) | null;
  onmessage: ((e: { data: unknown }) => void) | null;
  onclose: ((e: { code: number }) => void) | null;
  onerror: (() => void) | null;
  send(data: string | Uint8Array): void;
  close(): void;
}
interface Options {
  url: string;
  size: () => [number, number];
  socket: (url: string) => Socket;
  preflight: () => Promise<void>;
  reset?: () => void;
  initialGeneration?: string;
  generation?: (value: string) => void;
  write: (data: Uint8Array, done: () => void) => void;
  state: (state: string, message?: string) => void;
  warning: (message: string) => void;
  schedule?: (callback: () => void, ms: number) => () => void;
  /** Ask to create the session: only an explicit open does, and only until the first attach. */
  create?: boolean;
  /** Whether the session is still listed; checked before every reconnect. */
  alive?: () => boolean;
}
const backoff = [500, 1000, 2000, 4000, 8000];
/** One page attachment: no pending input, and ACK ownership stays with its socket. */
export class TerminalConnection {
  private socket: Socket | null = null;
  private ready = false;
  private stopped = false;
  private attempt = 0;
  private starting = false;
  private generation = "";
  /** Attached at least once: later sockets are reconnects and only join. */
  private attached = false;
  private cancelRetry: (() => void) | undefined;
  private cancelHandshake: (() => void) | undefined;
  private options: Options;
  constructor(options: Options) {
    this.options = options;
    this.generation = options.initialGeneration ?? "";
  }
  private schedule(callback: () => void, ms: number) {
    if (this.options.schedule) return this.options.schedule(callback, ms);
    const timer = setTimeout(callback, ms);
    return () => clearTimeout(timer);
  }
  async start() {
    if (this.stopped || this.starting || this.socket) return;
    if (this.gone()) return;
    this.starting = true;
    this.options.state("connecting");
    try {
      await this.options.preflight();
    } catch (error) {
      this.starting = false;
      if (!this.stopped) {
        const status =
          error && typeof error === "object" && "status" in error
            ? Number(error.status)
            : null;
        if (
          error instanceof TypeError ||
          (status !== null && (status === 0 || status >= 500))
        )
          this.retry();
        else
          this.finish(
            "error",
            error instanceof Error ? error.message : "Connection unavailable",
          );
      }
      return;
    }
    this.starting = false;
    if (this.stopped) return;
    let socket: Socket;
    try {
      socket = this.options.socket(this.url());
    } catch {
      this.retry();
      return;
    }
    this.socket = socket;
    socket.binaryType = "arraybuffer";
    socket.onopen = () => {
      if (this.socket !== socket || this.stopped) return;
      const [cols, rows] = this.options.size();
      socket.send(JSON.stringify({ cols, rows }));
      this.cancelHandshake = this.schedule(() => {
        if (this.socket === socket && !this.ready) socket.close();
      }, 10000);
    };
    socket.onmessage = (event) => {
      if (this.socket !== socket || this.stopped) return;
      if (event.data instanceof ArrayBuffer) {
        if (!this.ready) {
          this.finish("error", "Unexpected terminal output");
          return;
        }
        const bytes = new Uint8Array(event.data);
        this.options.write(bytes, () => {
          if (
            !this.stopped &&
            this.socket === socket &&
            socket.readyState === 1
          )
            socket.send(JSON.stringify({ ack: bytes.byteLength }));
        });
        return;
      }
      try {
        if (typeof event.data !== "string") throw new Error();
        const message = JSON.parse(event.data);
        if (
          message.ready === true &&
          typeof message.generation === "string" &&
          !this.ready
        ) {
          if (this.generation && this.generation !== message.generation)
            this.options.warning(
              "The environment restarted. Previous shells and jobs ended.",
            );
          this.options.reset?.();
          this.generation = message.generation;
          this.options.generation?.(this.generation);
          this.ready = true;
          this.attached = true;
          // The viewport may have changed while the attachment was starting.
          this.resize();
          this.attempt = 0;
          this.cancelHandshake?.();
          this.options.state("connected");
        } else if (message.ended === true)
          this.finish("ended", "Session ended.");
        else if (Number.isInteger(message.exit))
          this.finish(
            "exited",
            `Shell exited (${message.exit}).`,
          );
        else if (typeof message.error === "string")
          this.finish("error", message.error);
        else throw new Error();
      } catch {
        this.finish("error", "Invalid terminal protocol");
      }
    };
    socket.onerror = () => {
      /* close provides the definitive status; input is never buffered. */
    };
    socket.onclose = (event) => {
      if (this.socket !== socket || this.stopped) return;
      this.socket = null;
      this.ready = false;
      this.cancelHandshake?.();
      if (this.gone()) return;
      if ([1000, 1002, 1003, 1007, 1008, 1009].includes(event.code))
        this.finish("error", "Terminal closed. Check your access or reopen.");
      else this.retry();
    };
  }
  private url(): string {
    if (!this.options.create || this.attached) return this.options.url;
    const url = new URL(this.options.url);
    url.searchParams.set("create", "1");
    return url.href;
  }
  /** A reconnect to a session that's no longer listed ends instead. */
  private gone(): boolean {
    if (!this.attached || this.options.alive?.() !== false) return false;
    this.finish("ended", "Session ended.");
    return true;
  }
  private retry() {
    this.ready = false;
    const delay = backoff[this.attempt++];
    if (delay === undefined) {
      this.finish("error", "Connection lost. Reopen to try again.");
      return;
    }
    this.options.state(
      "retrying",
      `Connection lost. Retrying (${this.attempt}/${backoff.length})…`,
    );
    this.cancelRetry = this.schedule(
      () => {
        this.cancelRetry = undefined;
        void this.start();
      },
      Math.round(delay * (0.8 + Math.random() * 0.4)),
    );
  }
  /** Resume a pending retry after browser suspension or the network returning. */
  resume() {
    if (
      this.stopped ||
      this.ready ||
      this.starting ||
      this.socket ||
      !this.cancelRetry
    )
      return;
    this.cancelRetry();
    this.cancelRetry = undefined;
    void this.start();
  }
  input(data: Uint8Array): boolean {
    const socket = this.socket;
    if (!this.ready || this.stopped || !socket || socket.readyState !== 1)
      return false;
    if (
      data.byteLength > 65536 ||
      socket.bufferedAmount + data.byteLength > 262144
    ) {
      this.options.warning(
        "Input was not sent: paste a smaller amount or wait for the connection.",
      );
      return false;
    }
    socket.send(data);
    return true;
  }
  resize() {
    if (!this.ready || !this.socket || this.socket.readyState !== 1) return;
    this.socket.send(JSON.stringify({ resize: this.options.size() }));
  }
  private finish(state: string, message: string) {
    this.stop();
    this.options.state(state, message);
  }
  stop() {
    this.stopped = true;
    this.ready = false;
    this.cancelRetry?.();
    this.cancelHandshake?.();
    const socket = this.socket;
    this.socket = null;
    socket?.close();
  }
}
