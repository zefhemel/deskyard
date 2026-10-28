// A button's request: busy while it runs, a toast when it worked, and its
// error to show (inline, or as a toast).
import { useCallback, useRef, useState } from "preact/hooks";
import { notify, notifyError } from "./toast.tsx";
import { errorMessage } from "./ui.tsx";

export interface ActionOptions<R> {
  /** What to say once it worked. */
  success?: string | ((result: R) => string);
  /** After it worked (and the toast). */
  onDone?: (result: R) => void;
  /** Report a failure as a toast instead of in `error`. */
  toastErrors?: boolean;
}

export interface Action<A extends unknown[]> {
  /** Runs the request; true when it worked. */
  run: (...args: A) => Promise<boolean>;
  busy: boolean;
  error: string | null;
  setError: (error: string | null) => void;
}

export function useAction<A extends unknown[], R>(fn: (...args: A) => Promise<R>, options: ActionOptions<R> = {}): Action<A> {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Callers pass new closures on every render; run uses the latest.
  const latest = useRef({ fn, options });
  latest.current = { fn, options };
  const run = useCallback(async (...args: A) => {
    const { fn, options: o } = latest.current;
    setBusy(true);
    setError(null);
    try {
      const result = await fn(...args);
      if (o.success) notify(typeof o.success === "string" ? o.success : o.success(result));
      o.onDone?.(result);
      return true;
    } catch (e) {
      if (o.toastErrors) notifyError(e);
      else setError(errorMessage(e));
      return false;
    } finally {
      setBusy(false);
    }
  }, []);
  return { run, busy, error, setError };
}
