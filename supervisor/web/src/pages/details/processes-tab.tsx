import { useEffect, useState } from "preact/hooks";
import { api, type Environment, type Processes } from "../../api.ts";
import { bytes, plural } from "../../lib/format.ts";
import { errorMessage } from "../../components/ui.tsx";

/** ps's etime ("[[dd-]hh:]mm:ss") as "3 days", "2 h 14 min", "41 min". */
export function elapsed(etime: string): string {
  const m = /^(?:(\d+)-)?(?:(\d+):)?(\d+):(\d+)$/.exec(etime.trim());
  if (!m) return etime;
  const [d, h, min] = [Number(m[1] ?? 0), Number(m[2] ?? 0), Number(m[3] ?? 0)];
  if (d) return plural(d, "day");
  if (h) return `${h} h ${String(min).padStart(2, "0")} min`;
  if (min) return `${min} min`;
  return `${Number(m[4])} s`;
}

function column(titles: string[], name: string, fallback: number): number {
  const i = titles.findIndex((t) => t.toUpperCase().startsWith(name));
  return i >= 0 ? i : fallback;
}

const th = "border-b border-line px-3.5 py-2.5 text-left text-[13px] font-semibold text-muted";
const td = "border-b border-hairline px-3.5 py-[7px] whitespace-nowrap";

export function ProcessesTab({ environment: c }: { environment: Environment }) {
  const [data, setData] = useState<Processes | null>(null);
  const [error, setError] = useState<string | null>(null);
  const running = c.state === "running";

  useEffect(() => {
    if (!running) return;
    let live = true;
    const load = () =>
      api.processes(c.name).then(
        (p) => {
          if (!live) return;
          setData(p);
          setError(null);
        },
        (e: unknown) => live && setError(errorMessage(e)),
      );
    void load();
    const t = window.setInterval(load, 3000);
    return () => {
      live = false;
      window.clearInterval(t);
    };
  }, [c.name, running]);

  if (!running) return <p class="m-0 px-4 py-3.5 text-sm text-muted">{c.name} isn't running.</p>;
  if (!data) return <p class="m-0 px-4 py-3.5 text-sm text-muted">{error ?? "Loading…"}</p>;

  const t = data.titles;
  const col = {
    pid: column(t, "PID", 0),
    user: column(t, "USER", 1),
    cpu: column(t, "%CPU", 2),
    rss: column(t, "RSS", 3),
    time: column(t, "ELAPSED", 4),
    cmd: column(t, "COMMAND", 5),
  };
  return (
    <>
      <div class="overflow-x-auto">
        <table class="w-full border-collapse text-[13.5px]">
          <thead>
            <tr>
              <th class={`${th} text-right`}>PID</th>
              <th class={th}>User</th>
              <th class={`${th} text-right`}>CPU</th>
              <th class={`${th} text-right`}>Memory</th>
              <th class={`${th} text-right`}>Running for</th>
              <th class={th}>Command</th>
            </tr>
          </thead>
          <tbody>
            {data.rows.map((r, i) => {
              const cpu = Number(r[col.cpu] ?? 0);
              const cmd = String(r[col.cmd] ?? "");
              return (
                <tr key={`${r[col.pid]}-${i}`} class="last:[&>td]:border-b-0">
                  <td class={`${td} mono text-right`}>{r[col.pid]}</td>
                  <td class={td}>{r[col.user]}</td>
                  <td class={`${td} text-right`}>
                    {cpu.toFixed(1)}%
                    <span class="ml-2 inline-block h-1 w-[46px] rounded-sm bg-track align-middle">
                      <i class="block h-full rounded-sm bg-ink" style={{ width: `${Math.min(100, cpu * 4)}%` }} />
                    </span>
                  </td>
                  <td class={`${td} text-right`}>{bytes(Number(r[col.rss] ?? 0) * 1024)}</td>
                  <td class={`${td} text-right`}>{elapsed(String(r[col.time] ?? ""))}</td>
                  <td class={`${td} mono max-w-[560px] overflow-hidden text-[12.5px] text-ellipsis`} title={cmd}>
                    {cmd}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <div class="flex justify-between border-t border-line px-3.5 py-2.5 text-[13px] text-muted">
        <span>{plural(data.rows.length, "process", "processes")}</span>
        <span>{error ? <span class="text-danger">{error}</span> : "Updated every 3 seconds"}</span>
      </div>
    </>
  );
}
