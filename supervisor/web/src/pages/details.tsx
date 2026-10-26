import { useEffect, useState } from "preact/hooks";
import { useLocation, useRoute } from "preact-iso";
import { api, type AgentPresence, type Environment, type Forward } from "../api.ts";
import { agentLabel } from "../lib/activity-format.ts";
import { VPNStatus } from "../components/vpn.tsx";
import { Avatars } from "../components/avatar.tsx";
import { ChartsPanel } from "../components/charts-panel.tsx";
import { EnvironmentMenu, power } from "../components/environment-menu.tsx";
import { Monitor } from "../components/screen.tsx";
import { NoticeLine } from "../components/tile.tsx";
import { ReclaimMemoryButton } from "../components/reclaim-memory.tsx";
import { ForwardLines } from "../components/viewers-popover.tsx";
import { Page } from "../components/topbar.tsx";
import { btnClass, Button, cx, Status, Tag } from "../components/ui.tsx";
import { UpgradeDialog } from "../dialogs/upgrade.tsx";
import { useEnvironment, useLive, useNow } from "../live.tsx";
import { day, limits, names, statusKind, statusLine, toolsLine } from "../lib/format.ts";
import { AccessTab } from "./details/access-tab.tsx";
import { AgentsTab } from "./details/agents-tab.tsx";
import { ProcessesTab } from "./details/processes-tab.tsx";
import { SettingsTab } from "./details/settings-tab.tsx";

type Tab = "overview" | "processes" | "agents" | "settings" | "access";

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
  const tabs: { id: Tab; label: string }[] = [
    { id: "overview", label: "Overview" },
    { id: "processes", label: "Processes" },
    { id: "agents", label: "Remote access" },
    ...(me.admin
      ? [
          { id: "settings" as const, label: "Settings" },
          { id: "access" as const, label: "Access" },
        ]
      : []),
  ];
  const tab: Tab = tabs.find((t) => t.id === query.tab)?.id ?? "overview";
  const setTab = (t: Tab) =>
    route(`/environments/${c.name}${t === "overview" ? "" : `?tab=${t}`}`, true);

  return (
    <Page>
      <Crumbs />
      <div class="mb-[22px] flex flex-wrap items-center gap-x-3.5 gap-y-2">
        <h1 class="text-[30px] leading-tight font-bold tracking-[-0.02em]">
          {c.name}
        </h1>
        <Status kind={statusKind(c)}>{statusLine(c, now)}</Status>
        <div class="ml-auto flex flex-wrap gap-2">
          {c.state === "running" && (
            <>
              <a href={`/${c.name}/`} class={btnClass("primary")}>
                Desktop
              </a>
              <a href={`/${c.name}/terminal/`} class={btnClass()}>Terminal</a>
              <Button onClick={() => power(c.name, "restart")}>Restart</Button>
              <Button onClick={() => power(c.name, "stop")}>Stop</Button>
              {me.admin && <ReclaimMemoryButton name={c.name} disabled={!!c.job} />}
            </>
          )}
          {c.state === "stopped" && (
            <Button variant="primary" onClick={() => power(c.name, "start")}>
              Start
            </Button>
          )}
          <EnvironmentMenu
            environment={c}
            power={false}
            onRemoved={() => route("/")}
          />
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
            <ViewersPanel viewers={c.viewers} mics={c.mics} forwards={c.forwards} agents={c.agents} />
            <div class="rounded-[10px] border border-line bg-surface px-5 py-[18px]"><VPNStatus vpn={c.vpn} /></div>
            <ChartsPanel environment={c} />
            <Facts environment={c} admin={me.admin} onTab={setTab} />
          </div>
        </div>
      ) : (
        <div class="overflow-hidden rounded-[10px] border border-line bg-surface">
          {tab === "processes" && <ProcessesTab environment={c} />}
          {tab === "agents" && <AgentsTab environment={c} />}
          {tab === "settings" && <SettingsTab environment={c} />}
          {tab === "access" && <AccessTab environment={c} />}
        </div>
      )}
    </Page>
  );
}

function ViewersPanel({
  viewers,
  mics,
  forwards,
  agents,
}: {
  viewers: string[];
  mics: string[];
  forwards: Forward[];
  agents: AgentPresence[];
}) {
  return (
    <div class="flex items-center gap-2.5 rounded-[10px] border border-line bg-surface px-5 py-[18px] text-sm">
      {(viewers.length > 0 || agents.length > 0) && <Avatars names={viewers} mics={mics} agents={agents} />}
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

/** The environment's mise tools as one line, or "" when it isn't running, has none, or can't say. */
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
  environment: c,
  admin,
  onTab,
}: {
  environment: Environment;
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
        {c.revision > 0 && (
          <>
            <dt class="text-muted">Revision</dt>
            <dd class="m-0">
              {c.revision}
              {c.appliedRevision === 0
                ? " (no container yet)"
                : c.appliedRevision !== c.revision && ` (container built from ${c.appliedRevision})`}
            </dd>
          </>
        )}
        <dt class="text-muted">Limits</dt>
        <dd class="m-0">{limits(c.settings)}</dd>
        <dt class="text-muted">Access tokens</dt>
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
