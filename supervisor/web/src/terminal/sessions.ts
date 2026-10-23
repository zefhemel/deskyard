export type SessionListState = {names: string[]; loading: boolean; error: string};

/** Owns one visible picker's bounded discovery requests and polling lifetime. */
export class SessionDiscovery {
  private state: SessionListState = {names: [], loading: false, error: ""};
  private active = false;
  private request: AbortController | undefined;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private fetchNames: (signal: AbortSignal) => Promise<string[]>;
  private changed: (state: SessionListState) => void;
  constructor(fetchNames: (signal: AbortSignal) => Promise<string[]>, changed: (state: SessionListState) => void) {
    this.fetchNames = fetchNames;
    this.changed = changed;
  }
  start() {
    this.active = true;
    void this.refresh();
  }
  stop() {
    this.active = false;
    clearTimeout(this.timer);
    this.request?.abort();
    this.request = undefined;
  }
  async refresh() {
    if (!this.active || this.request) return;
    clearTimeout(this.timer);
    const request = new AbortController();
    this.request = request;
    this.update({loading: true, error: ""});
    const timeout = setTimeout(() => request.abort(), 6000);
    try {
      const names = await this.fetchNames(request.signal);
      if (this.request === request) this.update({names, error: ""});
    } catch (error) {
      if (this.request === request)
        this.update({error: request.signal.aborted ? "Session refresh timed out. Try again." : error instanceof Error ? error.message : "Could not load sessions."});
    } finally {
      clearTimeout(timeout);
      if (this.request === request) {
        this.request = undefined;
        this.update({loading: false});
        if (this.active) this.timer = setTimeout(() => void this.refresh(), 5000);
      }
    }
  }
  private update(change: Partial<SessionListState>) {
    this.state = {...this.state, ...change};
    this.changed(this.state);
  }
}
