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
  private environments = new Map<string, string>();
  private listeners = new Set<Update>();
  private order = 0;
  private watch: Watch;

  constructor(watch: Watch) { this.watch = watch; }

  subscribe(listener: Update): () => void {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  }

  get(id: string): Job | null { return this.entries.get(id)?.state ?? null; }
  forEnvironment(name: string): Job | null { return this.get(this.environments.get(name) ?? ""); }
  runningByEnvironment(): Record<string, Job> {
    return Object.fromEntries([...this.environments.keys()].flatMap(name => {
      const job = this.forEnvironment(name);
      return job?.status === "running" ? [[name, job]] : [];
    }));
  }

  track(id: string, onUpdate?: Update, environment?: string): () => void {
    let entry = this.entries.get(id);
    if (!entry) {
      entry = { state: { status: "running", message: "Starting", progress: null }, order: ++this.order, callbacks: new Set(), stop: () => {} };
      this.entries.set(id, entry);
      if (environment) this.assignEnvironment(environment, id, entry);
      for (const listener of this.listeners) listener(entry.state);
      entry.stop = this.watch(id, job => this.update(id, job));
    } else if (environment && this.assignEnvironment(environment, id, entry)) {
      for (const listener of this.listeners) listener(entry.state);
    }
    if (onUpdate) {
      if (entry.state.status === "running") entry.callbacks.add(onUpdate);
      onUpdate(entry.state);
    }
    return () => { if (onUpdate) entry.callbacks.delete(onUpdate); };
  }

  private assignEnvironment(name: string, id: string, entry: TrackedJob) {
    const currentID = this.environments.get(name);
    const current = this.entries.get(currentID ?? "");
    if (currentID === id || (current && current.order > entry.order)) return false;
    this.environments.set(name, id);
    return true;
  }

  private update(id: string, state: Job) {
    const entry = this.entries.get(id);
    if (!entry || entry.state.status !== "running") return;
    entry.state = state;
    if (state.environment) this.assignEnvironment(state.environment, id, entry);
    for (const callback of entry.callbacks) callback(state);
    if (state.status !== "running") entry.callbacks.clear();
    for (const listener of this.listeners) listener(state);
  }

  dispose() {
    const entries = [...this.entries.values()];
    this.entries.clear();
    this.environments.clear();
    this.listeners.clear();
    for (const entry of entries) entry.stop();
  }
}
