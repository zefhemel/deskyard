// A small keyed cache for data several views show at once (the backups
// overview, an item's backups): one request per key at a time, and every
// view of a key sees each reload, whichever view asked for it.

export interface SharedState<T> {
  data: T | undefined;
  error: string | null;
}

interface Entry {
  state: SharedState<unknown>;
  seq: number;
  inflight: boolean;
  listeners: Set<() => void>;
}

export class SharedFetches {
  private entries = new Map<string, Entry>();

  private entry(key: string): Entry {
    let e = this.entries.get(key);
    if (!e) {
      e = { state: { data: undefined, error: null }, seq: 0, inflight: false, listeners: new Set() };
      this.entries.set(key, e);
    }
    return e;
  }

  get<T>(key: string): SharedState<T> {
    return this.entry(key).state as SharedState<T>;
  }

  /** Calls `listener` whenever key's state changes; returns the unsubscribe. */
  subscribe(key: string, listener: () => void): () => void {
    const e = this.entry(key);
    e.listeners.add(listener);
    return () => {
      e.listeners.delete(listener);
    };
  }

  /**
   * Loads key with fn. Unless `force`, a load already under way is joined
   * rather than repeated; a forced load supersedes it.
   */
  load<T>(key: string, fn: () => Promise<T>, force = false): void {
    const e = this.entry(key);
    if (e.inflight && !force) return;
    const n = ++e.seq;
    e.inflight = true;
    const settle = (state: SharedState<unknown>) => {
      if (n !== e.seq) return;
      e.inflight = false;
      e.state = state;
      for (const l of [...e.listeners]) l();
    };
    fn().then(
      (data) => settle({ data, error: null }),
      (err: unknown) => settle({ data: e.state.data, error: err instanceof Error ? err.message : String(err) }),
    );
  }
}
