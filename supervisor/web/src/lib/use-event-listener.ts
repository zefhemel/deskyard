// Event listeners that follow a component's lifetime: attached on mount,
// removed on unmount, always calling the latest handler (so callers needn't
// memoize it), and re-attached only when the target, type or options change.
import { useEffect, useRef } from "preact/hooks";

export type ListenerOptions = boolean | { capture?: boolean; passive?: boolean };

function normalize(options: ListenerOptions | undefined): { capture: boolean; passive: boolean | undefined } {
  return typeof options === "boolean" ? { capture: options, passive: undefined } : { capture: !!options?.capture, passive: options?.passive };
}

/** Listens on a target (window, document, an element, a MediaQueryList…) while mounted; none while null. */
export function useEventListener<E extends Event = Event>(target: EventTarget | null | undefined, type: string, handler: (event: E) => void, options?: ListenerOptions): void {
  const latest = useRef(handler);
  latest.current = handler;
  const { capture, passive } = normalize(options);
  useEffect(() => {
    if (!target) return;
    const listener = (e: Event) => latest.current(e as E);
    const opts = { capture, passive };
    target.addEventListener(type, listener, opts);
    return () => target.removeEventListener(type, listener, opts);
  }, [target, type, capture, passive]);
}

/** The frame's side of a frame window listener. */
export interface FrameLike extends EventTarget {
  readonly contentWindow: (EventTarget & { document?: unknown }) | null;
}

/**
 * Listens on an iframe's window, re-attaching each time the frame loads a
 * document (a load replaces the window's listeners). Only a same-origin frame
 * can be listened to; anything else (an error page elsewhere, a frame being
 * torn down) is skipped. Returns the cleanup.
 */
export function listenFrameWindow(frame: FrameLike, type: string, listener: (e: Event) => void, options?: ListenerOptions): () => void {
  const opts = normalize(options);
  let attached: EventTarget | null = null;
  const detach = () => {
    try {
      attached?.removeEventListener(type, listener, opts);
    } catch {
      // the frame's window is gone
    }
    attached = null;
  };
  const attach = () => {
    detach();
    try {
      const w = frame.contentWindow;
      // Reading document throws for a cross-origin frame.
      if (w?.document) {
        w.addEventListener(type, listener, opts);
        attached = w;
      }
    } catch {
      // not same-origin: nothing to listen to
    }
  };
  attach();
  frame.addEventListener("load", attach);
  return () => {
    frame.removeEventListener("load", attach);
    detach();
  };
}

/** useEventListener on an iframe's window, across its loads; none while the frame is null. */
export function useFrameWindowListener<E extends Event = Event>(frame: FrameLike | null | undefined, type: string, handler: (event: E) => void, options?: ListenerOptions): void {
  const latest = useRef(handler);
  latest.current = handler;
  const { capture, passive } = normalize(options);
  useEffect(() => {
    if (!frame) return;
    return listenFrameWindow(frame, type, (e) => latest.current(e as E), { capture, passive });
  }, [frame, type, capture, passive]);
}
