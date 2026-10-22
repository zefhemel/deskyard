import { useState } from "preact/hooks";
import { useLocation, useRoute } from "preact-iso";
import { api, type Computer } from "../api.ts";
import { Avatars } from "../components/avatar.tsx";
import { ChartsPanel } from "../components/charts-panel.tsx";
import { ComputerMenu, power } from "../components/computer-menu.tsx";
import { Monitor } from "../components/screen.tsx";
import { Page } from "../components/topbar.tsx";
import { Button, cx, Status, Tag } from "../components/ui.tsx";
import { UpgradeDialog } from "../dialogs/upgrade.tsx";
import { useComputer, useLive, useNow } from "../live.tsx";
import { day, limits, names, statusKind, statusLine } from "../lib/format.ts";
import { AccessTab } from "./computer/access-tab.tsx";
import { AgentsTab } from "./computer/agents-tab.tsx";
import { ProcessesTab } from "./computer/processes-tab.tsx";
import { SettingsTab } from "./computer/settings-tab.tsx";

type Tab = "processes" | "agents" | "settings" | "access";

export function ComputerPage() {
  const { params } = useRoute();
  const name = params.name ?? "";
  const c = useComputer(name);
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
        <p class="text-muted">There's no computer with this name, or you can't use it.</p>
      </Page>
    );
  }
  return <ComputerDetail computer={c} />;
}

function Crumbs() {
  return (
    <div class="mb-2 text-sm">
      <a href="/" class="text-muted no-underline hover:underline">
        Computers
      </a>
    </div>
  );
}

function ComputerDetail({ computer: c }: { computer: Computer }) {
  const { me, jobs } = useLive();
  const { query, route } = useLocation();
  const now = useNow(10000);
  const tabs: { id: Tab; label: string }[] = [
    { id: "processes", label: "Processes" },
    { id: "agents", label: "Agents" },
    ...(me.admin
      ? [
          { id: "settings" as const, label: "Settings" },
          { id: "access" as const, label: "Access" },
        ]
      : []),
  ];
  const tab: Tab = tabs.find((t) => t.id === query.tab)?.id ?? "processes";
  const setTab = (t: Tab) => route(`/computers/${c.name}${t === "processes" ? "" : `?tab=${t}`}`, true);

  return (
    <Page>
      <Crumbs />
      <div class="mb-[22px] flex flex-wrap items-center gap-x-3.5 gap-y-2">
        <h1 class="text-[30px] leading-tight font-bold tracking-[-0.02em]">{c.name}</h1>
        <Status kind={statusKind(c)}>{statusLine(c, now)}</Status>
        <div class="ml-auto flex gap-2">
          {c.state === "running" && (
            <>
              <Button onClick={() => power(c.name, "restart")}>Restart</Button>
              <Button onClick={() => power(c.name, "stop")}>Stop</Button>
            </>
          )}
          {c.state === "stopped" && (
            <Button variant="primary" onClick={() => power(c.name, "start")}>
              Start
            </Button>
          )}
          <ComputerMenu computer={c} power={false} onRemoved={() => route("/")} />
        </div>
      </div>

      <div class="grid grid-cols-[minmax(0,1.55fr)_minmax(320px,1fr)] items-start gap-8 max-[900px]:grid-cols-1">
        <Monitor computer={c} job={jobs[c.name]} now={now} big />
        <div class="flex flex-col gap-4">
          <ViewersPanel viewers={c.viewers} />
          <ChartsPanel computer={c} />
          <Facts computer={c} admin={me.admin} onTab={setTab} />
        </div>
      </div>

      <nav class="mt-[34px] flex gap-1 overflow-x-auto border-b border-line" role="tablist">
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            role="tab"
            aria-selected={t.id === tab}
            onClick={() => setTab(t.id)}
            class={cx(
              "-mb-px cursor-pointer border-0 border-b-2 bg-transparent px-3.5 py-2.5 text-sm font-semibold",
              t.id === tab ? "border-ink text-ink" : "border-transparent text-muted hover:text-ink",
            )}
          >
            {t.label}
          </button>
        ))}
      </nav>
      <div class="overflow-hidden rounded-b-[10px] border border-t-0 border-line bg-surface">
        {tab === "processes" && <ProcessesTab computer={c} />}
        {tab === "agents" && <AgentsTab computer={c} />}
        {tab === "settings" && <SettingsTab computer={c} />}
        {tab === "access" && <AccessTab computer={c} />}
      </div>
    </Page>
  );
}

function ViewersPanel({ viewers }: { viewers: string[] }) {
  return (
    <div class="flex items-center gap-2.5 rounded-[10px] border border-line bg-surface px-5 py-[18px] text-sm">
      {viewers.length > 0 && <Avatars names={viewers} />}
      <div>
        <b class="font-semibold">{viewers.length ? `${names(viewers)} ${viewers.length === 1 ? "is" : "are"} viewing` : "Nobody is viewing"}</b>
        <p class="mt-0 text-[13px] text-muted">Same desktop, one mouse and keyboard. It follows the window of whoever resized last.</p>
      </div>
    </div>
  );
}

function Facts({ computer: c, admin, onTab }: { computer: Computer; admin: boolean; onTab: (t: Tab) => void }) {
  const [upgrading, setUpgrading] = useState(false);
  const link = "cursor-pointer border-0 bg-transparent p-0 font-[inherit] text-accent hover:underline";
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
              <button type="button" class={link} onClick={() => setUpgrading(true)}>
                Upgrade…
              </button>
            </>
          )}
        </dd>
        {admin && (
          <>
            <dt class="text-muted">Access</dt>
            <dd class="m-0 min-w-0">
              {c.access.length ? `${c.access.join(", ")}, and admins` : "Admins only"}{" "}
              <button type="button" class={link} onClick={() => onTab("access")}>
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
