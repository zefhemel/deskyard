import { VPNStatus } from "./vpn.tsx";
import { api, type Environment, type Job } from "../api.ts";
import { limits, memoryOf, notice, statusKind, statusLine } from "../lib/format.ts";
import { EnvironmentMenu } from "./environment-menu.tsx";
import { notifyError } from "./toast.tsx";
import { loadOrder, orderTabs } from "../lib/tab-order.ts";
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

/**
 * A running environment's ways in: its desktop, each terminal session in this
 * viewer's tab order (joining, never creating), and + to open a terminal.
 */
function SessionLinks({ environment: c }: { environment: Environment }) {
  const sessions = c.terminals ?? [];
  const order = orderTabs(sessions.map((t) => t.name), loadOrder(c.name));
  const shown = order.length > 4 ? order.slice(0, 3) : order;
  const clients = (name: string) => sessions.find((t) => t.name === name)?.clients ?? 0;
  const hasMain = sessions.some((t) => t.name === "main");
  // With main open, + makes a new session; the router mustn't see a link.
  const openNew = async () => {
    try {
      const { name } = await api.createTerminal(c.name);
      location.href = `/${c.name}/?terminal=${encodeURIComponent(name)}`;
    } catch (err) {
      notifyError(err);
    }
  };
  return (
    <div class="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm" aria-label="Open">
      <a href={`/${c.name}/`}>Desktop</a>
      {shown.map((t) => (
        <a key={t} href={`/${c.name}/?terminal=${encodeURIComponent(t)}`} class="inline-flex items-center gap-1" title={`Terminal ${t}`}>
          {t}
          {clients(t) > 0 && <span class="rounded-full bg-hairline px-1.5 text-[11px] text-muted" title={`${clients(t)} attached`}>{clients(t)}</span>}
        </a>
      ))}
      {order.length > shown.length && <a href={`/${c.name}/`}>+{order.length - shown.length} more</a>}
      {c.terminal &&
        (hasMain ? (
          <button type="button" class="cursor-pointer border-0 bg-transparent p-0 font-[inherit] text-accent hover:underline" onClick={() => void openNew()} title="Open a new terminal session" aria-label="Open a new terminal session">
            +
          </button>
        ) : (
          <a href={`/${c.name}/?terminal=main&create=1`} title="Open a terminal" aria-label="Open a terminal">
            {sessions.length ? "+" : "Terminal"}
          </a>
        ))}
    </div>
  );
}

export function Tile({ environment: c, job, now, admin, backupFailed }: { environment: Environment; job: Job | undefined; now: number; admin: boolean; backupFailed?: boolean }) {
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
        {backupFailed && (
          <a href={`/environments/${c.name}?tab=backups`} class="mt-2 block text-[13px] text-danger">
            Its last backup failed
          </a>
        )}
        {c.state === "running" && (
          <SessionLinks environment={c} />
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
