// What an agent is doing now and did in the last 15 minutes.
import { useEffect, useState } from "preact/hooks";
import { api, type AgentSummary as Summary } from "../api.ts";
import { useLive, useNow } from "../live.tsx";
import { nowLine, recentLine } from "../lib/activity-format.ts";

/** The agents' summaries of a desktop: loaded on first use, then kept current by activity events. */
export function useActivity(name: string): Summary[] | null {
  const { onActivity } = useLive();
  const [agents, setAgents] = useState<Summary[] | null>(null);
  useEffect(() => {
    let live = true;
    setAgents(null);
    api.activity(name).then(
      (a) => live && setAgents((cur) => cur ?? a.agents),
      () => live && setAgents((cur) => cur ?? []),
    );
    const off = onActivity((e) => {
      if (e.name === name) setAgents(e.agents);
    });
    return () => {
      live = false;
      off();
    };
  }, [name, onActivity]);
  return agents;
}

const MAX_RECENT = 20;

export function AgentSummary({ summary }: { summary: Summary | undefined }) {
  const now = useNow(1000);
  const heading = "mt-2 mb-1 text-xs font-semibold text-muted first:mt-0";
  const list = "m-0 list-none p-0 text-[13px]";
  if (!summary) return <p class="m-0 text-[13px] text-muted">Loading…</p>;
  return (
    <div data-agent-summary={summary.id}>
      <h4 class={heading}>Now</h4>
      {summary.now.length ? (
        <ul class={list}>
          {summary.now.map((n, i) => (
            <li key={i} class="mono py-px [overflow-wrap:anywhere]">
              {nowLine(n, now)}
            </li>
          ))}
        </ul>
      ) : (
        <p class="m-0 text-[13px] text-muted">Nothing running</p>
      )}
      <h4 class={heading}>Recently</h4>
      {summary.recent.length ? (
        <ul class={list}>
          {summary.recent.slice(0, MAX_RECENT).map((r, i) => (
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
