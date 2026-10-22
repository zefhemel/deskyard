import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { errorMessage } from "../components/ui.tsx";

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
