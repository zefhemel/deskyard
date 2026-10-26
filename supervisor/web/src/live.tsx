// Live state shared by every signed-in page: who is signed in, the
// environments they can see (kept current through /api/events) and, for
// admins, containers that conflict with them, the host's numbers and the
// jobs running on them, whoever started them.
import { createContext, type ComponentChildren } from "preact";
import { useCallback, useContext, useEffect, useMemo, useRef, useState } from "preact/hooks";
import { JobTracker } from "./lib/job-tracker.ts";
import { watchUIEvents } from "./lib/event-feed.ts";
import {
  api,
  watchJob,
  type ActivityEvent,
  type Conflict,
  type Environment,
  type Host,
  type Job,
  type Me,
  type RemovedEvent,
  type StatsEvent,
  type ViewersEvent,
} from "./api.ts";

type StatsListener = (e: StatsEvent) => void;
type ActivityListener = (e: ActivityEvent) => void;

export interface Live {
  me: Me;
  /** null until the first load. */
  environments: Environment[] | null;
  /** This supervisor's containers it can't match to a definition (admins only). */
  conflicts: Conflict[];
  host: Host | null;
  /** Running jobs by environment name, for busy tiles. */
  jobs: Record<string, Job>;
  /** Shared job states, retained across forms and tab navigation. */
  jobTracker: JobTracker;
  /** Gets the environments and the host again (after a change the events may not cover). */
  refresh: () => void;
  onStats: (listener: StatsListener) => () => void;
  /** Agent summaries, at most once a second per environment. */
  onActivity: (listener: ActivityListener) => () => void;
}

const LiveContext = createContext<Live | null>(null);

export function useLive(): Live {
  const live = useContext(LiveContext);
  if (!live) throw new Error("useLive outside LiveProvider");
  return live;
}

export function useEnvironment(name: string): Environment | null | undefined {
  const { environments } = useLive();
  if (!environments) return undefined;
  return environments.find((c) => c.name === name) ?? null;
}

function upsert(list: Environment[], c: Environment): Environment[] {
  const rest = list.filter((x) => x.name !== c.name);
  rest.push(c);
  return rest.sort((a, b) => a.name.localeCompare(b.name));
}

export function LiveProvider({ me, children }: { me: Me; children: ComponentChildren }) {
  const [environments, setEnvironments] = useState<Environment[] | null>(null);
  const [conflicts, setConflicts] = useState<Conflict[]>([]);
  const [host, setHost] = useState<Host | null>(null);
  const [jobTracker] = useState(() => new JobTracker(watchJob));
  const [jobVersion, setJobVersion] = useState(0);
  const statsListeners = useRef(new Set<StatsListener>());
  const activityListeners = useRef(new Set<ActivityListener>());
  const hostTimer = useRef<number | undefined>(undefined);

  const loadHost = useCallback(() => {
    api.host().then(setHost, () => undefined);
  }, []);
  // Debounced: state changes come in bursts.
  const hostSoon = useCallback(() => {
    window.clearTimeout(hostTimer.current);
    hostTimer.current = window.setTimeout(loadHost, 500);
  }, [loadHost]);

  // A failed load (the network blinking, say) is tried again, sooner and
  // then later: nothing else would fill the list until the event stream
  // reconnects, so the page would say "Loading…" for good.
  const refreshTimer = useRef<number | undefined>(undefined);
  const refreshGeneration = useRef(0);
  const refresh = useCallback(() => {
    const generation = ++refreshGeneration.current;
    window.clearTimeout(refreshTimer.current);
    const load = (wait: number) => {
      api.environments().then(
        (list) => {
          if (refreshGeneration.current !== generation) return;
          setEnvironments(list.environments);
          setConflicts(list.conflicts);
        },
        () => {
          if (refreshGeneration.current === generation) {
            refreshTimer.current = window.setTimeout(() => load(Math.min(wait * 2, 30000)), wait);
          }
        },
      );
    };
    load(1000);
    loadHost();
  }, [loadHost]);

  useEffect(() => {
    refresh();
    const hostInterval = window.setInterval(loadHost, 15000);
    // Conflicts come from the supervisor's reconcile pass, which no event
    // reports: admins look again as often as it runs.
    const conflictInterval = me.admin
      ? window.setInterval(() => api.environments().then((list) => setConflicts(list.conflicts), () => undefined), 30000)
      : undefined;
    const es = watchUIEvents("/api/events");
    es.addEventListener("environment", (e: MessageEvent<string>) => {
      const c = JSON.parse(e.data) as Environment;
      setEnvironments((list) => (list ? upsert(list, c) : list));
      hostSoon();
    });
    es.addEventListener("removed", (e: MessageEvent<string>) => {
      const { name } = JSON.parse(e.data) as RemovedEvent;
      setEnvironments((list) => (list ? list.filter((c) => c.name !== name) : list));
      hostSoon();
    });
    es.addEventListener("stats", (e: MessageEvent<string>) => {
      const s = JSON.parse(e.data) as StatsEvent;
      setEnvironments((list) =>
        list
          ? list.map((c) =>
              c.name === s.name ? { ...c, stats: { cpuPercent: s.cpuPercent, memBytes: s.memBytes, memLimit: s.memLimit } } : c,
            )
          : list,
      );
      for (const l of statsListeners.current) l(s);
    });
    es.addEventListener("viewers", (e: MessageEvent<string>) => {
      const v = JSON.parse(e.data) as ViewersEvent;
      setEnvironments((list) => (list ? list.map((c) => (c.name === v.name ? { ...c, viewers: v.viewers, mics: v.mics, cams: v.cams, forwards: v.forwards, agents: v.agents ?? c.agents } : c)) : list));
    });
    es.addEventListener("activity", (e: MessageEvent<string>) => {
      const a = JSON.parse(e.data) as ActivityEvent;
      for (const l of activityListeners.current) l(a);
    });
    let reconnected = false;
    es.onopen = () => {
      // After a dropped connection, catch up on what we missed.
      if (reconnected) refresh();
      reconnected = true;
    };
    es.onerror = () => {
      // The feed retries by itself; this only notices a lost session.
      api.me().catch(() => undefined);
    };
    return () => {
      ++refreshGeneration.current;
      es.close();
      window.clearInterval(hostInterval);
      window.clearInterval(conflictInterval);
      window.clearTimeout(hostTimer.current);
      window.clearTimeout(refreshTimer.current);
    };
  }, [refresh, loadHost, hostSoon, me.admin]);

  useEffect(() => {
    const unsubscribe = jobTracker.subscribe(job => {
      setJobVersion(version => version + 1);
      if (job.status !== "running") refresh();
    });
    return () => { unsubscribe(); jobTracker.dispose(); };
  }, [jobTracker, refresh]);

  // Discover operations started elsewhere and reconnect after a page reload.
  useEffect(() => {
    for (const c of environments ?? []) {
      if (c.job) jobTracker.track(c.job, undefined, c.name);
    }
  }, [environments, jobTracker]);

  const jobs = useMemo(() => jobTracker.runningByEnvironment(), [jobTracker, jobVersion]);

  const onStats = useCallback((listener: StatsListener) => {
    statsListeners.current.add(listener);
    return () => {
      statsListeners.current.delete(listener);
    };
  }, []);

  const onActivity = useCallback((listener: ActivityListener) => {
    activityListeners.current.add(listener);
    return () => {
      activityListeners.current.delete(listener);
    };
  }, []);

  const value = useMemo<Live>(
    () => ({ me, environments, conflicts, host, jobs, jobTracker, refresh, onStats, onActivity }),
    [me, environments, conflicts, host, jobs, jobTracker, refresh, onStats, onActivity, jobVersion],
  );
  return <LiveContext.Provider value={value}>{children}</LiveContext.Provider>;
}

/** Re-renders every `ms` milliseconds and returns the current time (for "Running for …" and thumbnails). */
export function useNow(ms: number): number {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), ms);
    return () => window.clearInterval(t);
  }, [ms]);
  return now;
}
