import { VPNStatus } from "./vpn.tsx";
import type { Environment, Job } from "../api.ts";
import { limits, memoryOf, notice, statusKind, statusLine } from "../lib/format.ts";
import { EnvironmentMenu } from "./environment-menu.tsx";
import { Monitor } from "./screen.tsx";
import { cx, Meter, Status, Tag } from "./ui.tsx";

export function Meters({ environment: c }: { environment: Environment }) {
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

export function ImageLine({ environment: c, admin }: { environment: Environment; admin: boolean }) {
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

/** A conflict, a failed change or one not applied yet; nothing when there's none. */
export function NoticeLine({ environment: c, class: extra }: { environment: Environment; class?: string }) {
  const n = notice(c);
  if (!n) return null;
  return (
    <p role={n.kind === "error" ? "alert" : undefined} class={cx("m-0 text-[13px]", n.kind === "error" ? "text-danger" : "text-busy", extra)}>
      {n.text}
    </p>
  );
}

/** The volumes an environment is granted, linked to the Volumes page for admins. */
export function VolumesLine({ environment: c, admin }: { environment: Environment; admin: boolean }) {
  const grants = c.settings.volumes ?? [];
  if (!grants.length) return null;
  return (
    <div class="mt-1 text-sm text-muted" aria-label="Volumes">
      {grants.length === 1 ? "Volume " : "Volumes "}
      {grants.map((g, i) => (
        <span key={g.name}>
          {i > 0 && ", "}
          {admin ? <a href={`/volumes#volume-${g.name}`}>{g.name}</a> : <span class="text-ink">{g.name}</span>}
          {g.readOnly && " (read only)"}
        </span>
      ))}
    </div>
  );
}

export function Tile({ environment: c, job, now, admin }: { environment: Environment; job: Job | undefined; now: number; admin: boolean }) {
  const kind = statusKind(c);
  const busy = kind === "busy";
  return (
    <article class="relative">
      <Monitor environment={c} job={job} now={now} href={`/environments/${c.name}`} />
      <div class="px-0.5 pt-3">
        <div class="flex items-center gap-2.5">
          <div class="flex min-w-0 flex-col gap-0.5">
            <h2 class="truncate text-[17px] leading-tight font-semibold">
              <a href={`/environments/${c.name}`} class="text-ink no-underline hover:underline">
                {c.name}
              </a>
            </h2>
            <Status kind={kind}>{statusLine(c, now)}</Status>
          </div>
          {!busy && (
            <div class="ml-auto flex gap-1">
              <EnvironmentMenu environment={c} />
            </div>
          )}
        </div>
        <NoticeLine environment={c} class="mt-2" />
        {c.state === "running" && (
          <div class="mt-2 flex gap-3 text-sm">
            <a href={`/${c.name}/`}>Desktop</a>
            <a href={`/${c.name}/terminal/`}>Terminal</a>
          </div>
        )}
        {c.state === "running" && <Meters environment={c} />}
        {c.state === "stopped" && <div class={cx("mt-2.5 text-[13px] text-muted")}>{limits(c.settings)}</div>}
        <div class="mt-2"><VPNStatus vpn={c.vpn} compact /></div>
        <VolumesLine environment={c} admin={admin} />
        {!busy && <ImageLine environment={c} admin={admin} />}
      </div>
    </article>
  );
}
