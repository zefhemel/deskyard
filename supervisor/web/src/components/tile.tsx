import { VPNStatus } from "./vpn.tsx";
import type { Desktop, Job } from "../api.ts";
import { limits, memoryOf, statusKind, statusLine } from "../lib/format.ts";
import { DesktopMenu } from "./desktop-menu.tsx";
import { Monitor } from "./screen.tsx";
import { cx, Meter, Status, Tag } from "./ui.tsx";

export function Meters({ desktop: c }: { desktop: Desktop }) {
  const cpu = Math.round(c.stats?.cpuPercent ?? 0);
  const mem = c.stats?.memBytes ?? 0;
  const memLimit = c.stats?.memLimit ?? 0;
  return (
    <div class="mt-2.5 grid grid-cols-2 gap-3.5">
      <div>
        <div class="mb-1 flex justify-between text-[12.5px] text-muted">
          CPU <b class="font-semibold text-ink">{c.stats ? `${cpu}%` : "–"}</b>
        </div>
        <Meter percent={cpu} hot={cpu > 80} />
      </div>
      <div>
        <div class="mb-1 flex justify-between gap-2 text-[12.5px] text-muted">
          Memory <b class="font-semibold whitespace-nowrap text-ink">{c.stats ? memoryOf(mem, c.settings.memoryBytes || null) : "–"}</b>
        </div>
        <Meter percent={memLimit ? (100 * mem) / memLimit : 0} />
      </div>
    </div>
  );
}

export function ImageLine({ desktop: c, admin }: { desktop: Desktop; admin: boolean }) {
  if (!c.imageTitle && !c.imageVersion) return null;
  return (
    <div class="mt-2 text-[12.5px] text-muted">
      {c.imageTitle} {c.imageVersion}
      {admin && c.updateAvailable && (
        <>
          {" · "}
          <Tag kind="update">Update available</Tag>
        </>
      )}
    </div>
  );
}

export function Tile({ desktop: c, job, now, admin }: { desktop: Desktop; job: Job | undefined; now: number; admin: boolean }) {
  const kind = statusKind(c);
  const busy = kind === "busy";
  return (
    <article class="relative">
      <Monitor desktop={c} job={job} now={now} />
      <div class="px-0.5 pt-3">
        <div class="flex items-center gap-2.5">
          <div class="flex min-w-0 flex-col gap-0.5">
            <h2 class="truncate text-[17px] leading-tight font-semibold">
              <a href={`/desktops/${c.name}`} class="text-ink no-underline hover:underline">
                {c.name}
              </a>
            </h2>
            <Status kind={kind}>{statusLine(c, now)}</Status>
          </div>
          {!busy && (
            <div class="ml-auto flex gap-1">
              <DesktopMenu desktop={c} />
            </div>
          )}
        </div>
        {c.state === "running" && <Meters desktop={c} />}
        {c.state === "stopped" && <div class={cx("mt-2.5 text-[13px] text-muted")}>{limits(c.settings)}</div>}
        <div class="mt-2"><VPNStatus vpn={c.vpn} compact /></div>
        {!busy && <ImageLine desktop={c} admin={admin} />}
      </div>
    </article>
  );
}
