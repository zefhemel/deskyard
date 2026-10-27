import { useState } from "preact/hooks";
import { api } from "../api.ts";
import { useLive } from "../live.tsx";
import { bytes } from "../lib/format.ts";
import type { MenuItem } from "./menu.tsx";
import { errorMessage } from "./ui.tsx";
import { notifyLive } from "./toast.tsx";

/** The ⋯ menu item for a manual request; apps keep running and the result is resident accounting. */
export function useReclaimMemoryItem(name: string, disabled?: boolean): MenuItem {
  const { host } = useLive();
  const support = host?.memoryReclaim;
  const unavailable = support?.reason || "Memory reclamation is unavailable on this host.";
  const [busy, setBusy] = useState(false);
  const reclaim = async () => {
    if (busy || !support?.enabled) return;
    setBusy(true);
    const notice = notifyLive(<span class="max-w-[calc(100vw-4rem)] [overflow-wrap:anywhere]">Reclaiming memory for {name}…</span>);
    try {
      const result = await api.reclaimMemory(name);
      notice.finish(
        <span class="max-w-[calc(100vw-4rem)] [overflow-wrap:anywhere]">
          {name}: resident memory {bytes(result.beforeBytes)} → {bytes(result.afterBytes)}
          <span class="block text-xs font-normal">
            {result.partial ? "The full request could not be reclaimed. " : ""}
            Swapped pages may still use compressed RAM.
          </span>
        </span>,
        { ms: 10000 },
      );
    } catch (error) {
      notice.finish(<span class="max-w-[calc(100vw-4rem)] [overflow-wrap:anywhere]">{errorMessage(error)}</span>, { error: true });
    } finally {
      setBusy(false);
    }
  };
  return {
    label: (
      <>
        {busy ? "Reclaiming…" : "Reclaim memory"}
        {support && !support.enabled && <span class="block max-w-xs text-xs whitespace-normal text-muted [overflow-wrap:anywhere]">Unavailable: {unavailable}</span>}
      </>
    ),
    disabled: busy || disabled || !support?.enabled,
    onSelect: reclaim,
  };
}
