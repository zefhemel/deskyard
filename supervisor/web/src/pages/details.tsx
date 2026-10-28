import { useEffect, useState } from "preact/hooks";
import { useLocation, useRoute } from "preact-iso";
import { api, type AgentPresence, type Environment, type FolderSize, type Forward } from "../api.ts";
import { agentLabel } from "../lib/activity-format.ts";
import { VPNStatus } from "../components/vpn.tsx";
import { Avatars } from "../components/avatar.tsx";
import { ChartsPanel } from "../components/charts-panel.tsx";
import { EnvironmentMenu, power } from "../components/environment-menu.tsx";
import { Monitor } from "../components/screen.tsx";
import { NoticeLine } from "../components/tile.tsx";
import { useReclaimMemoryItem } from "../components/reclaim-memory.tsx";
import { ForwardLines } from "../components/viewers-popover.tsx";
import { Page, useTitle } from "../components/topbar.tsx";
import { btnClass, Button, cx, Meter, Status, Tag } from "../components/ui.tsx";
import { UpgradeDialog } from "../dialogs/upgrade.tsx";
import { useEnvironment, useLive, useNow } from "../live.tsx";
import { bytes, day, limits, names, statusKind, statusLine } from "../lib/format.ts";
import { AccessTab } from "./details/access-tab.tsx";
import { WebDAVAccess } from "./details/webdav-access.tsx";
import { can, type EnvironmentPermissions } from "../lib/permissions.ts";
import { ProcessesTab } from "./details/processes-tab.tsx";
import { SettingsTab } from "./details/settings-tab.tsx";
import { SharingTab } from "./details/sharing-tab.tsx";
import { BackupsPanel } from "../components/backups/panel.tsx";

type Tab = "overview" | "processes" | "settings" | "access" | "backups";

/** The tabs, each shown to those it is for. */
const TABS: { id: Tab; label: string; shown: (p: EnvironmentPermissions) => boolean }[] = [
  { id: "overview", label: "Overview", shown: () => true },
  { id: "processes", label: "Processes", shown: (p) => p.use },
  { id: "settings", label: "Settings", shown: (p) => p.configure },
  // Who can use it: accounts' grants (admins) and the shares (everyone with full access).
  { id: "access", label: "Access", shown: (p) => p.seeShares },
  { id: "backups", label: "Backups", shown: (p) => p.configure },
];

export function DetailsPage() {
  const { params } = useRoute();
  const name = params.name ?? "";
  const c = useEnvironment(name);
  if (c === undefined) {
    return (
      <Page>
        <p class="text-muted">Loading…</p>
      </Page>
    );
  }
  if (c === null) {
    return (
      <Page>
        <Crumbs />
        <h1 class="mb-3 text-[30px] font-bold">{name}</h1>
        <p class="text-muted">
          There's no environment with this name, or you can't use it.
        </p>
      </Page>
    );
  }
  return <EnvironmentDetail environment={c} />;
}

function Crumbs() {
  return (
    <div class="mb-2 text-sm">
      <a href="/" class="text-muted no-underline hover:underline">
        Environments
      </a>
    </div>
  );
}

function EnvironmentDetail({ environment: c }: { environment: Environment }) {
  const { me, jobs } = useLive();
  const { query, route } = useLocation();
  const now = useNow(10000);
  // Observers watch the desktop and nothing else: no other tabs, no actions.
  const may = can(me, c);
  const full = may.use;
  const tabs = TABS.filter((t) => t.shown(may));
  // The Sharing tab became part of Access.
  const asked = query.tab === "sharing" ? "access" : query.tab;
  const tab: Tab = tabs.find((t) => t.id === asked)?.id ?? "overview";
  const setTab = (t: Tab) =>
    route(`/environments/${c.name}${t === "overview" ? "" : `?tab=${t}`}`, true);
  const reclaim = useReclaimMemoryItem(c.name, !!c.job);
  useTitle(tab === "overview" ? undefined : tabs.find((t) => t.id === tab)?.label, c.name);

  return (
    <Page>
      <Crumbs />
      <div class="mb-[22px] flex flex-wrap items-center gap-x-3.5 gap-y-2">
        <h1 class="text-[30px] leading-tight font-bold tracking-[-0.02em]">
          {c.name}
        </h1>
        <Status kind={statusKind(c)}>{statusLine(c, now)}</Status>
        {!full && <Tag>Observing</Tag>}
        <div class="ml-auto flex flex-wrap gap-2">
          {c.state === "running" && !full && (
            <a href={`/${c.name}/`} class={btnClass("primary")}>
              Watch desktop
            </a>
          )}
          {c.state === "running" && full && (
            <>
              <a href={`/${c.name}/`} class={btnClass("primary")}>
                Open
              </a>
              <Button onClick={() => power(c.name, "restart")}>Restart</Button>
              <Button onClick={() => power(c.name, "stop")}>Stop</Button>
            </>
          )}
          {c.state === "stopped" && full && (
            <Button variant="primary" onClick={() => power(c.name, "start")}>
              Start
            </Button>
          )}
          {may.configure && <Button onClick={() => setTab("settings")}>Settings</Button>}
          {full && (
            <EnvironmentMenu
              environment={c}
              power={false}
              settings={false}
              adminItems={c.state === "running" ? [reclaim] : []}
              onRemoved={() => route("/")}
            />
          )}
        </div>
      </div>

      <NoticeLine environment={c} class="-mt-3 mb-5" />
      <nav
        class="mb-6 flex gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden border-b border-line"
        role="tablist"
      >
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            role="tab"
            aria-selected={t.id === tab}
            onClick={() => setTab(t.id)}
            class={cx(
              "-mb-px cursor-pointer border-0 border-b-2 bg-transparent px-3.5 py-2.5 text-sm font-semibold",
              t.id === tab
                ? "border-ink text-ink"
                : "border-transparent text-muted hover:text-ink",
            )}
          >
            {t.label}
          </button>
        ))}
      </nav>
      {tab === "overview" ? (
        <div class="grid grid-cols-[minmax(0,1.55fr)_minmax(320px,1fr)] items-start gap-8 max-[900px]:grid-cols-1">
          <Monitor environment={c} job={jobs[c.name]} now={now} big />
          <div class="flex flex-col gap-4">
            <ViewersPanel viewers={c.viewers} observers={c.observers} mics={c.mics} forwards={c.forwards} agents={c.agents} observing={!full} />
            {full && <div class="rounded-[10px] border border-line bg-surface px-5 py-[18px]"><VPNStatus vpn={c.vpn} /></div>}
            <ChartsPanel environment={c} />
            <Facts environment={c} may={may} onTab={setTab} />
          </div>
        </div>
      ) : tab === "backups" ? (
        <BackupsPanel kind="environment" name={c.name} stoppers={c.state === "running" ? [c.name] : []} />
      ) : (
        <div class="overflow-hidden rounded-[10px] border border-line bg-surface">
          {tab === "processes" && <ProcessesTab environment={c} />}
          {tab === "settings" && <SettingsTab environment={c} />}
          {tab === "access" && (
            <>
              {may.configure && <AccessTab environment={c} />}
              <WebDAVAccess environment={c} />
              <div class={may.configure || may.webdav ? "border-t border-line" : undefined}>
                <SharingTab environment={c} />
              </div>
            </>
          )}
        </div>
      )}
    </Page>
  );
}

function ViewersPanel({
  viewers,
  observers,
  mics,
  forwards,
  agents,
  observing,
}: {
  viewers: string[];
  observers: string[];
  mics: string[];
  forwards: Forward[];
  agents: AgentPresence[];
  /** The signed-in account only observes. */
  observing: boolean;
}) {
  const watching = observers.filter((o) => viewers.includes(o));
  return (
    <div class="flex items-center gap-2.5 rounded-[10px] border border-line bg-surface px-5 py-[18px] text-sm">
      {(viewers.length > 0 || agents.length > 0) && <Avatars names={viewers} mics={mics} agents={agents} />}
      <div>
        <b class="font-semibold">
          {viewers.length
            ? `${names(viewers)} ${viewers.length === 1 ? "is" : "are"} viewing`
            : "Nobody is viewing"}
        </b>
        {watching.length > 0 && (
          <p class="mt-0 text-[13px] text-muted" aria-label="Observing">
            {names(watching)} {watching.length === 1 ? "is" : "are"} observing: watching, without keyboard or mouse.
          </p>
        )}
        <p class="mt-0 text-[13px] text-muted">
          {observing
            ? "You can watch this desktop, but not use it."
            : "Same desktop, one mouse and keyboard. It follows the window of whoever resized last."}
        </p>
        {agents.length > 0 && (
          <p class="mt-1.5 mb-0 text-[13px]" aria-label="CLI activity">
            {names(agents.map(agentLabel))} {agents.length === 1 ? "is" : "are"} working here.
          </p>
        )}
        <ForwardLines forwards={forwards} class="mt-1.5" />
      </div>
    </div>
  );
}

function Facts({ environment: c, may, onTab }: { environment: Environment; may: EnvironmentPermissions; onTab: (t: Tab) => void }) {
  // Observers aren't told the environment's configuration.
  const full = may.use;
  const admin = may.configure;
  const [upgrading, setUpgrading] = useState(false);
  const link =
    "cursor-pointer border-0 bg-transparent p-0 font-[inherit] text-accent hover:underline";
  return (
    <div class="rounded-[10px] border border-line bg-surface px-5 py-[18px]">
      <dl class="m-0 grid grid-cols-[max-content_1fr] gap-x-[18px] gap-y-[9px] text-sm">
        <dt class="text-muted">Desktop</dt>
        <dd class="m-0 min-w-0">
          <a href={`/${c.name}/`} class="mono">
            /{c.name}/
          </a>
        </dd>
        {full && (
          <>
            <dt class="text-muted">Files</dt>
            <dd class="mono m-0 min-w-0 [overflow-wrap:anywhere]">{c.folder}</dd>
            <dt class="text-muted">Disk</dt>
            <dd class="m-0 min-w-0" aria-label="Disk">
              <DiskUsage name={c.name} />
            </dd>
          </>
        )}
        <dt class="text-muted">Image</dt>
        <dd class="m-0 min-w-0 [overflow-wrap:anywhere]">
          {c.imageTitle || c.image} {c.imageVersion}{" "}
          {admin && c.updateAvailable && (
            <>
              <Tag kind="update">{c.updateAvailable} available</Tag>{" "}
              <button
                type="button"
                class={link}
                onClick={() => setUpgrading(true)}
              >
                Upgrade…
              </button>
            </>
          )}
        </dd>
        {admin && (
          <>
            <dt class="text-muted">Access</dt>
            <dd class="m-0 min-w-0">
              {c.access.length
                ? `${c.access.map((g) => (g.level === "observe" ? `${g.username} (observe)` : g.username)).join(", ")}, and admins`
                : "Admins only"}{" "}
              <button
                type="button"
                class={link}
                onClick={() => onTab("access")}
              >
                Change
              </button>
            </dd>
          </>
        )}
        {full && (
          <>
            <dt class="text-muted">Limits</dt>
            <dd class="m-0">{limits(c.settings)}</dd>
            <dt class="text-muted">Sharing</dt>
            <dd class="m-0">
              <button type="button" class={link} onClick={() => onTab("access")}>
                Shares and links
              </button>
            </dd>
          </>
        )}
        {c.created && (
          <>
            <dt class="text-muted">Created</dt>
            <dd class="m-0">{day(c.created)}</dd>
          </>
        )}
        {c.state === "running" && c.startedAt && (
          <>
            <dt class="text-muted">Started</dt>
            <dd class="m-0">{day(c.startedAt)}</dd>
          </>
        )}
      </dl>
      {upgrading && <UpgradeEnvironment environment={c} onClose={() => setUpgrading(false)} />}
    </div>
  );
}

/** Upgrades one environment. */
function UpgradeEnvironment({ environment: c, onClose }: { environment: Environment; onClose: () => void }) {
  return (
    <UpgradeDialog
      title={`Upgrade ${c.name}?`}
      intro={
        <>
          {c.name} is recreated on {c.imageTitle} {c.updateAvailable}
          {c.state === "running" ? " and restarted." : "."}
        </>
      }
      action={`Upgrade ${c.name}`}
      begin={() => api.upgrade(c.name)}
      onClose={onClose}
    />
  );
}

/** The home folder's size and the disk holding it. Sizing walks the whole
 * folder, so it's asked for once per page. */
function DiskUsage({ name }: { name: string }) {
  const [size, setSize] = useState<FolderSize | null>(null);
  useEffect(() => {
    let live = true;
    api.folder(name).then((f) => live && setSize(f), () => {});
    return () => {
      live = false;
    };
  }, [name]);
  if (!size) return <span class="text-muted">Measuring…</span>;
  const used = size.bytes === 0 && size.complete ? "Empty" : size.bytes === 0 ? "Unknown" : `${size.complete ? "" : "At least "}${bytes(size.bytes)}`;
  const disk = size.diskSize ? { free: size.diskFree ?? 0, total: size.diskSize } : null;
  return (
    <>
      <span class="font-semibold">{used}</span>
      {disk && (
        <>
          <span class="text-muted"> · {bytes(disk.free)} free of {bytes(disk.total)}</span>
          <Meter class="mt-1.5 max-w-[360px]" percent={(100 * (disk.total - disk.free)) / disk.total} />
        </>
      )}
    </>
  );
}
