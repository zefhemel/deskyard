// Live state shared by every signed-in page: who is signed in, the desktops
// they can see (kept current through /api/events), the host's numbers and the
// jobs running on them, whoever started them.
import { createContext, type ComponentChildren } from "preact";
import { useCallback, useContext, useEffect, useMemo, useRef, useState } from "preact/hooks";
import { watchUIEvents } from "./lib/event-feed.ts";
import {
  api,
  watchJob,
  type ActivityEvent,
  type Desktop,
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
  desktops: Desktop[] | null;
  host: Host | null;
  /** Running jobs by desktop name, for busy tiles. */
  jobs: Record<string, Job>;
  /** Follows a job; its progress also shows on the desktop's tile. */
  trackJob: (id: string, onUpdate?: (job: Job) => void) => () => void;
  /** Gets the desktops and the host again (after a change the events may not cover). */
  refresh: () => void;
  onStats: (listener: StatsListener) => () => void;
  /** Agent summaries, at most once a second per desktop. */
  onActivity: (listener: ActivityListener) => () => void;
}

const LiveContext = createContext<Live | null>(null);

export function useLive(): Live {
  const live = useContext(LiveContext);
  if (!live) throw new Error("useLive outside LiveProvider");
  return live;
}

export function useDesktop(name: string): Desktop | null | undefined {
  const { desktops } = useLive();
  if (!desktops) return undefined;
  return desktops.find((c) => c.name === name) ?? null;
}

function upsert(list: Desktop[], c: Desktop): Desktop[] {
  const rest = list.filter((x) => x.name !== c.name);
  rest.push(c);
  return rest.sort((a, b) => a.name.localeCompare(b.name));
}

export function LiveProvider({ me, children }: { me: Me; children: ComponentChildren }) {
  const [desktops, setDesktops] = useState<Desktop[] | null>(null);
  const [host, setHost] = useState<Host | null>(null);
  const [jobs, setJobs] = useState<Record<string, Job>>({});
  const statsListeners = useRef(new Set<StatsListener>());
  const activityListeners = useRef(new Set<ActivityListener>());
  const hostTimer = useRef<number | undefined>(undefined);
  const followed = useRef(new Set<string>());

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
      api.desktops().then(
        (list) => {
          if (refreshGeneration.current === generation) setDesktops(list);
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
    const es = watchUIEvents("/api/events");
    es.addEventListener("desktop", (e: MessageEvent<string>) => {
      const c = JSON.parse(e.data) as Desktop;
      setDesktops((list) => (list ? upsert(list, c) : list));
      hostSoon();
    });
    es.addEventListener("removed", (e: MessageEvent<string>) => {
      const { name } = JSON.parse(e.data) as RemovedEvent;
      setDesktops((list) => (list ? list.filter((c) => c.name !== name) : list));
      hostSoon();
    });
    es.addEventListener("stats", (e: MessageEvent<string>) => {
      const s = JSON.parse(e.data) as StatsEvent;
      setDesktops((list) =>
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
      setDesktops((list) => (list ? list.map((c) => (c.name === v.name ? { ...c, viewers: v.viewers, mics: v.mics, cams: v.cams, forwards: v.forwards, agents: v.agents ?? c.agents } : c)) : list));
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
      window.clearTimeout(hostTimer.current);
      window.clearTimeout(refreshTimer.current);
    };
  }, [refresh, loadHost, hostSoon]);

  const trackJob = useCallback((id: string, onUpdate?: (job: Job) => void) => {
    followed.current.add(id);
    let name: string | undefined;
    return watchJob(id, (job) => {
      if (job.desktop) name = job.desktop;
      if (name) {
        const key = name;
        setJobs((all) => {
          const next = { ...all };
          if (job.status === "running") next[key] = job;
          else delete next[key];
          return next;
        });
      }
      onUpdate?.(job);
    });
  }, []);

  // Jobs someone else started (or this browser started before a reload).
  useEffect(() => {
    for (const c of desktops ?? []) {
      if (c.job && !followed.current.has(c.job)) trackJob(c.job);
    }
  }, [desktops, trackJob]);

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
    () => ({ me, desktops, host, jobs, trackJob, refresh, onStats, onActivity }),
    [me, desktops, host, jobs, trackJob, refresh, onStats, onActivity],
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
