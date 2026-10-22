import { useEffect, useState } from "preact/hooks";
import { useLocation, useRoute } from "preact-iso";
import { api, type Desktop, type Forward } from "../api.ts";
import { Avatars } from "../components/avatar.tsx";
import { ChartsPanel } from "../components/charts-panel.tsx";
import { DesktopMenu, power } from "../components/desktop-menu.tsx";
import { Monitor } from "../components/screen.tsx";
import { ForwardLines } from "../components/viewers-popover.tsx";
import { Page } from "../components/topbar.tsx";
import { btnClass, Button, cx, LockBadge, Status, Tag } from "../components/ui.tsx";
import { BuildLogDialog } from "../dialogs/build-log.tsx";
import { ImageFromDesktopDialog } from "../dialogs/image-from-desktop.tsx";
import { UpgradeDialog } from "../dialogs/upgrade.tsx";
import { notify } from "../components/toast.tsx";
import { useDesktop, useLive, useNow } from "../live.tsx";
import { day, limits, names, statusKind, statusLine, toolsLine } from "../lib/format.ts";
import { AccessTab } from "./details/access-tab.tsx";
import { AgentsTab } from "./details/agents-tab.tsx";
import { ProcessesTab } from "./details/processes-tab.tsx";
import { SettingsTab } from "./details/settings-tab.tsx";

type Tab = "overview" | "processes" | "agents" | "settings" | "access";

export function DetailsPage() {
  const { params } = useRoute();
  const name = params.name ?? "";
  const c = useDesktop(name);
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
          There's no desktop with this name, or you can't use it.
        </p>
      </Page>
    );
  }
  return <DesktopDetail desktop={c} />;
}

function Crumbs() {
  return (
    <div class="mb-2 text-sm">
      <a href="/" class="text-muted no-underline hover:underline">
        Desktops
      </a>
    </div>
  );
}

function DesktopDetail({ desktop: c }: { desktop: Desktop }) {
  const { me, jobs } = useLive();
  const { query, route } = useLocation();
  const now = useNow(10000);
  const [imaging, setImaging] = useState(false);
  const [building, setBuilding] = useState<{ job: string; title: string } | null>(null);
  const tabs: { id: Tab; label: string }[] = [
    { id: "overview", label: "Overview" },
    { id: "processes", label: "Processes" },
    { id: "agents", label: "Agents" },
    ...(me.admin
      ? [
          { id: "settings" as const, label: "Settings" },
          { id: "access" as const, label: "Access" },
        ]
      : []),
  ];
  const tab: Tab = tabs.find((t) => t.id === query.tab)?.id ?? "overview";
  const setTab = (t: Tab) =>
    route(`/desktops/${c.name}${t === "overview" ? "" : `?tab=${t}`}`, true);

  return (
    <Page>
      <Crumbs />
      <div class="mb-[22px] flex flex-wrap items-center gap-x-3.5 gap-y-2">
        <h1 class="text-[30px] leading-tight font-bold tracking-[-0.02em]">
          {c.name}
        </h1>
        <Status kind={statusKind(c)}>{statusLine(c, now)}</Status>
        <div class="ml-auto flex gap-2">
          {c.state === "running" && (
            <>
              <a href={`/${c.name}/`} class={btnClass("primary")}>
                Open
              </a>
              <Button onClick={() => power(c.name, "restart")}>Restart</Button>
              <Button onClick={() => power(c.name, "stop")}>Stop</Button>
            </>
          )}
          {c.state === "stopped" && (
            <Button variant="primary" onClick={() => power(c.name, "start")}>
              Start
            </Button>
          )}
          <DesktopMenu
            desktop={c}
            power={false}
            adminItems={[{ label: "New image from this desktop…", onSelect: () => setImaging(true) }]}
            onRemoved={() => route("/")}
          />
        </div>
      </div>

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
          <Monitor desktop={c} job={jobs[c.name]} now={now} big />
          <div class="flex flex-col gap-4">
            <ViewersPanel viewers={c.viewers} forwards={c.forwards} />
            <ChartsPanel desktop={c} />
            <Facts desktop={c} admin={me.admin} onTab={setTab} />
          </div>
        </div>
      ) : (
        <div class="overflow-hidden rounded-[10px] border border-line bg-surface">
          {tab === "processes" && <ProcessesTab desktop={c} />}
          {tab === "agents" && <AgentsTab desktop={c} />}
          {tab === "settings" && <SettingsTab desktop={c} />}
          {tab === "access" && <AccessTab desktop={c} />}
        </div>
      )}
      {imaging && (
        <ImageFromDesktopDialog
          desktop={c}
          onClose={() => setImaging(false)}
          onStarted={(job, title) => {
            setImaging(false);
            setBuilding({ job, title });
          }}
        />
      )}
      {building && (
        <BuildLogDialog
          title={building.title}
          job={building.job}
          onClose={(running) => {
            if (running) notify(`${building.title} keeps building: the Images page shows when it's ready`);
            setBuilding(null);
          }}
          onDone={() => undefined}
          onLost={() => undefined}
        />
      )}
    </Page>
  );
}

function ViewersPanel({
  viewers,
  forwards,
}: {
  viewers: string[];
  forwards: Forward[];
}) {
  return (
    <div class="flex items-center gap-2.5 rounded-[10px] border border-line bg-surface px-5 py-[18px] text-sm">
      {viewers.length > 0 && <Avatars names={viewers} />}
      <div>
        <b class="font-semibold">
          {viewers.length
            ? `${names(viewers)} ${viewers.length === 1 ? "is" : "are"} viewing`
            : "Nobody is viewing"}
        </b>
        <p class="mt-0 text-[13px] text-muted">
          Same desktop, one mouse and keyboard. It follows the window of whoever
          resized last.
        </p>
        <ForwardLines forwards={forwards} class="mt-1.5" />
      </div>
    </div>
  );
}

/** The desktop's mise tools as one line, or "" when it isn't running, has none, or can't say. */
function useTools(name: string, running: boolean): string {
  const [line, setLine] = useState("");
  useEffect(() => {
    setLine("");
    if (!running) return;
    let live = true;
    api.tools(name).then(
      (t) => live && setLine(Array.isArray(t) ? toolsLine(t) : ""),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [name, running]);
  return line;
}

function Facts({
  desktop: c,
  admin,
  onTab,
}: {
  desktop: Desktop;
  admin: boolean;
  onTab: (t: Tab) => void;
}) {
  const [upgrading, setUpgrading] = useState(false);
  const tools = useTools(c.name, c.state === "running");
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
        <dt class="text-muted">Files</dt>
        <dd class="mono m-0 min-w-0 [overflow-wrap:anywhere]">{c.folder}</dd>
        <dt class="text-muted">Image</dt>
        <dd class="m-0 min-w-0 [overflow-wrap:anywhere]">
          {c.imageTitle || c.image} {c.imageVersion}{" "}
          {c.imageSecrets && (
            <>
              <LockBadge />{" "}
            </>
          )}
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
        {tools && (
          <>
            <dt class="text-muted">Tools</dt>
            <dd class="m-0 min-w-0 [overflow-wrap:anywhere]">{tools}</dd>
          </>
        )}
        {admin && (
          <>
            <dt class="text-muted">Access</dt>
            <dd class="m-0 min-w-0">
              {c.access.length
                ? `${c.access.join(", ")}, and admins`
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
        <dt class="text-muted">Limits</dt>
        <dd class="m-0">{limits(c.settings)}</dd>
        <dt class="text-muted">Agent tokens</dt>
        <dd class="m-0">
          {c.tokens ? `${c.tokens} active` : "None"}{" "}
          <button type="button" class={link} onClick={() => onTab("agents")}>
            Manage
          </button>
        </dd>
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
      {upgrading && (
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
          onClose={() => setUpgrading(false)}
        />
      )}
    </div>
  );
}
