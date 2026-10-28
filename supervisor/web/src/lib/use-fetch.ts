import { useCallback, useEffect, useReducer, useRef, useState } from "preact/hooks";
import { api, type BackupItem, type BackupsOverview, type ItemKind } from "../api.ts";
import { errorMessage } from "../components/ui.tsx";
import { SharedFetches } from "./shared-fetch.ts";

export interface Fetched<T> {
  data: T | undefined;
  error: string | null;
  reload: () => void;
}

/** Loads `fn()` when `key` changes; `reload()` loads it again. */
export function useFetch<T>(fn: () => Promise<T>, key: string): Fetched<T> {
  const [data, setData] = useState<T | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);
  const fnRef = useRef(fn);
  fnRef.current = fn;
  const seq = useRef(0);

  const reload = useCallback(() => {
    const n = ++seq.current;
    fnRef.current().then(
      (d) => {
        if (n !== seq.current) return;
        setData(d);
        setError(null);
      },
      (e: unknown) => {
        if (n === seq.current) setError(errorMessage(e));
      },
    );
  }, []);

  useEffect(() => {
    setData(undefined);
    reload();
  }, [key, reload]);

  return { data, error, reload };
}

const shared = new SharedFetches();

/**
 * Like useFetch, for data several views show at once: views of the same key
 * share one request and every reload, so one view's reload updates the
 * others. A cached value shows at once while it is loaded again. A null key
 * loads nothing.
 */
export function useSharedFetch<T>(key: string | null, fn: () => Promise<T>): Fetched<T> {
  const [, rerender] = useReducer((n: number) => n + 1, 0);
  const fnRef = useRef(fn);
  fnRef.current = fn;
  useEffect(() => {
    if (key === null) return;
    const off = shared.subscribe(key, () => rerender(undefined));
    shared.load(key, () => fnRef.current());
    return off;
  }, [key]);
  const reload = useCallback(() => {
    if (key !== null) shared.load(key, () => fnRef.current(), true);
  }, [key]);
  if (key === null) return { data: undefined, error: null, reload };
  const { data, error } = shared.get<T>(key);
  return { data, error, reload };
}

/** The backups overview (repositories and every item's backups), one source for every view of it. */
export function useBackups(enabled = true): Fetched<BackupsOverview> {
  return useSharedFetch(enabled ? "backups" : null, () => api.backups());
}

/** One environment's or volume's backups, one source for every view of it. */
export function useBackupItem(kind: ItemKind, name: string): Fetched<BackupItem> {
  return useSharedFetch(`backup:${kind}:${name}`, () => api.backup(kind, name));
}
