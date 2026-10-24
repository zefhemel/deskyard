import type { Job } from "../api.ts";

type Update = (job: Job) => void;
type Watch = (id: string, update: Update) => () => void;
interface TrackedJob {
  state: Job;
  order: number;
  callbacks: Set<Update>;
  stop: () => void;
}

/** One transport and authoritative state per job, shared across UI lifetimes. */
export class JobTracker {
  private entries = new Map<string, TrackedJob>();
  private desktops = new Map<string, string>();
  private listeners = new Set<Update>();
  private order = 0;
  private watch: Watch;

  constructor(watch: Watch) { this.watch = watch; }

  subscribe(listener: Update): () => void {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  }

  get(id: string): Job | null { return this.entries.get(id)?.state ?? null; }
  forDesktop(name: string): Job | null { return this.get(this.desktops.get(name) ?? ""); }
  runningByDesktop(): Record<string, Job> {
    return Object.fromEntries([...this.desktops.keys()].flatMap(name => {
      const job = this.forDesktop(name);
      return job?.status === "running" ? [[name, job]] : [];
    }));
  }

  track(id: string, onUpdate?: Update, desktop?: string): () => void {
    let entry = this.entries.get(id);
    if (!entry) {
      entry = { state: { status: "running", message: "Starting", progress: null }, order: ++this.order, callbacks: new Set(), stop: () => {} };
      this.entries.set(id, entry);
      if (desktop) this.assignDesktop(desktop, id, entry);
      for (const listener of this.listeners) listener(entry.state);
      entry.stop = this.watch(id, job => this.update(id, job));
    } else if (desktop && this.assignDesktop(desktop, id, entry)) {
      for (const listener of this.listeners) listener(entry.state);
    }
    if (onUpdate) {
      if (entry.state.status === "running") entry.callbacks.add(onUpdate);
      onUpdate(entry.state);
    }
    return () => { if (onUpdate) entry.callbacks.delete(onUpdate); };
  }

  private assignDesktop(name: string, id: string, entry: TrackedJob) {
    const currentID = this.desktops.get(name);
    const current = this.entries.get(currentID ?? "");
    if (currentID === id || (current && current.order > entry.order)) return false;
    this.desktops.set(name, id);
    return true;
  }

  private update(id: string, state: Job) {
    const entry = this.entries.get(id);
    if (!entry || entry.state.status !== "running") return;
    entry.state = state;
    if (state.desktop) this.assignDesktop(state.desktop, id, entry);
    for (const callback of entry.callbacks) callback(state);
    if (state.status !== "running") entry.callbacks.clear();
    for (const listener of this.listeners) listener(state);
  }

  dispose() {
    const entries = [...this.entries.values()];
    this.entries.clear();
    this.desktops.clear();
    this.listeners.clear();
    for (const entry of entries) entry.stop();
  }
}
