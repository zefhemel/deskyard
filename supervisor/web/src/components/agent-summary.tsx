// What an agent is doing now and did in the last 15 minutes.
import { useEffect, useState } from "preact/hooks";
import { api, type AgentSummary as Summary } from "../api.ts";
import { useLive, useNow } from "../live.tsx";
import { nowLine, recentLine } from "../lib/activity-format.ts";

/** The agents' summaries of a desktop: loaded on first use, then kept current by activity events. */
export type ActivityState = { status: "loading" | "failed"; agents: null } | { status: "ready"; agents: Summary[] };

export function useActivity(name: string): ActivityState {
  const { onActivity } = useLive();
  const [state, setState] = useState<ActivityState>({ status: "loading", agents: null });
  useEffect(() => {
    let live = true;
    setState({ status: "loading", agents: null });
    api.activity(name).then(
      (a) => live && setState((cur) => (cur.status === "ready" ? cur : { status: "ready", agents: a.agents })),
      () => live && setState((cur) => (cur.status === "ready" ? cur : { status: "failed", agents: null })),
    );
    const off = onActivity((e) => {
      if (e.name === name) setState({ status: "ready", agents: e.agents });
    });
    return () => {
      live = false;
      off();
    };
  }, [name, onActivity]);
  return state;
}

const MAX_RECENT = 20;

/** `summary` is undefined when the agent has none (yet): nothing running, nothing recent. */
export function AgentSummary({ summary, status = "ready" }: { summary: Summary | undefined; status?: ActivityState["status"] }) {
  const now = useNow(1000);
  const heading = "mt-2 mb-1 text-xs font-semibold text-muted first:mt-0";
  const list = "m-0 list-none p-0 text-[13px]";
  if (status === "loading") return <p class="m-0 text-[13px] text-muted">Loading…</p>;
  if (status === "failed") return <p class="m-0 text-[13px] text-muted">Couldn't load activity</p>;
  return (
    <div data-agent-summary={summary?.id}>
      <h4 class={heading}>Now</h4>
      {summary?.now.length ? (
        <ul class={list}>
          {summary!.now.map((n, i) => (
            <li key={i} class="mono py-px [overflow-wrap:anywhere]">
              {nowLine(n, now)}
            </li>
          ))}
        </ul>
      ) : (
        <p class="m-0 text-[13px] text-muted">Nothing running</p>
      )}
      <h4 class={heading}>Recently</h4>
      {summary?.recent.length ? (
        <ul class={list}>
          {summary!.recent.slice(0, MAX_RECENT).map((r, i) => (
            <li key={i} class="py-px [overflow-wrap:anywhere]">
              {recentLine(r)}
            </li>
          ))}
        </ul>
      ) : (
        <p class="m-0 text-[13px] text-muted">Nothing in the last 15 minutes</p>
      )}
    </div>
  );
}
