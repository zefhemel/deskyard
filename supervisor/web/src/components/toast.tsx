// Short messages at the bottom of the window, e.g. when an action fails.
import type { ComponentChildren } from "preact";
import { useEffect, useState } from "preact/hooks";

interface Toast {
  id: number;
  body: ComponentChildren;
  error: boolean;
}

let toasts: Toast[] = [];
let nextId = 1;
const listeners = new Set<(t: Toast[]) => void>();

function emit() {
  for (const l of listeners) l(toasts);
}

export function notify(body: ComponentChildren, opts: { error?: boolean; ms?: number } = {}): void {
  const t: Toast = { id: nextId++, body, error: opts.error ?? false };
  toasts = [...toasts, t];
  emit();
  window.setTimeout(() => {
    toasts = toasts.filter((x) => x !== t);
    emit();
  }, opts.ms ?? 4000);
}

export function notifyError(e: unknown): void {
  notify(e instanceof Error ? e.message : String(e), { error: true, ms: 6000 });
}

export function Toaster() {
  const [list, setList] = useState(toasts);
  useEffect(() => {
    listeners.add(setList);
    return () => {
      listeners.delete(setList);
    };
  }, []);
  if (!list.length) return null;
  return (
    <div aria-live="polite" class="pointer-events-none fixed bottom-6 left-1/2 z-[60] flex -translate-x-1/2 flex-col items-center gap-2">
      {list.map((t) => (
        <div
          key={t.id}
          class={
            "flex items-center gap-2 rounded-full px-3.5 py-[7px] text-[13px] font-semibold text-white shadow-lg " +
            (t.error ? "bg-danger" : "bg-[rgba(15,21,29,.88)]")
          }
        >
          {t.body}
        </div>
      ))}
    </div>
  );
}
