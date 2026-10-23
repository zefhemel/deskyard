import { useState } from "preact/hooks";
import { api } from "../api.ts";
import { useLive } from "../live.tsx";
import { bytes } from "../lib/format.ts";
import { Button, errorMessage } from "./ui.tsx";
import { notifyLive } from "./toast.tsx";

/** A manual request; apps keep running and the result is resident accounting. */
export function ReclaimMemoryButton({ name, disabled }: { name: string; disabled?: boolean }) {
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
  return (
    <div class="flex min-w-0 max-w-full flex-col items-start gap-1">
      <Button
        disabled={busy || disabled || !support?.enabled}
        onClick={reclaim}
        title={!support ? "Loading host capabilities…" : !support.enabled ? unavailable : "Reclaim idle memory while apps keep running. Their next interaction may be slower."}
      >
        {busy ? "Reclaiming…" : "Reclaim memory"}
      </Button>
      {support && !support.enabled && <span class="max-w-xs text-xs text-muted [overflow-wrap:anywhere]">Unavailable: {unavailable}</span>}
    </div>
  );
}
