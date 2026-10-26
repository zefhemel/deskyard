// CPU and memory over the last 15 minutes: the history from
// /api/environments/{name}/stats, then each `stats` event appended.
import { useEffect, useState } from "preact/hooks";
import { api, type Environment, type StatsPoint } from "../api.ts";
import { useLive } from "../live.tsx";
import { memoryOf } from "../lib/format.ts";
import { Chart } from "./chart.tsx";

const SLOTS = 180; // 15 minutes at 5 s
const WINDOW_MS = 15 * 60 * 1000;

export function ChartsPanel({ environment: c }: { environment: Environment }) {
  const { onStats } = useLive();
  const [points, setPoints] = useState<StatsPoint[]>([]);
  const [memLimit, setMemLimit] = useState(0);

  useEffect(() => {
    let live = true;
    setPoints([]);
    api.stats(c.name).then(
      (h) => {
        if (!live) return;
        setPoints(h.history);
        setMemLimit(h.memLimit);
      },
      () => undefined,
    );
    const off = onStats((s) => {
      if (s.name !== c.name) return;
      setMemLimit(s.memLimit);
      setPoints((p) => {
        const cutoff = Date.parse(s.t) - WINDOW_MS;
        return [...p.filter((x) => Date.parse(x.t) > cutoff), { t: s.t, cpuPercent: s.cpuPercent, memBytes: s.memBytes }].slice(-SLOTS);
      });
    });
    return () => {
      live = false;
      off();
    };
  }, [c.name, onStats]);

  const last = points[points.length - 1];
  const running = c.state === "running";
  const limit = memLimit || c.stats?.memLimit || 0;
  return (
    <div class="rounded-[10px] border border-line bg-surface px-5 py-[18px]">
      <div>
        <div class="mb-1 flex items-baseline justify-between text-[13px] text-muted">
          CPU, last 15 minutes <b class="text-[15px] font-semibold text-ink">{running && last ? `${Math.round(last.cpuPercent)}%` : "–"}</b>
        </div>
        <Chart values={points.map((p) => p.cpuPercent)} max={100} slots={SLOTS} />
      </div>
      <div class="mt-3.5">
        <div class="mb-1 flex items-baseline justify-between text-[13px] text-muted">
          Memory <b class="text-[15px] font-semibold text-ink">{running && last ? memoryOf(last.memBytes, c.settings.memoryBytes || null) : "–"}</b>
        </div>
        <Chart values={points.map((p) => p.memBytes)} max={limit} slots={SLOTS} />
      </div>
    </div>
  );
}
