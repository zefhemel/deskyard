// An agent's avatar: the owner's initial with a robot badge (from outside), or a
// robot (inside the desktop), in the agent's color. A dashed ring pulses while
// the agent is active.
import type { AgentPresence } from "../api.ts";
import { agentTitle } from "../lib/activity-format.ts";
import { cx } from "./ui.tsx";

export function AgentAvatar({ agent, ring = "var(--color-surface)", size = 24 }: { agent: AgentPresence; ring?: string; size?: number }) {
  const px = `${size}px`;
  return (
    <span
      data-agent={agent.id}
      title={agentTitle(agent)}
      class={cx("relative inline-block flex-none rounded-full", !agent.active && "opacity-60")}
      style={{ width: px, height: px, minWidth: px }}
    >
      <span
        class="grid h-full w-full place-items-center rounded-full border-2 font-bold text-white uppercase"
        style={{ background: agent.color, borderColor: ring, fontSize: `${size * 0.48}px` }}
      >
        {agent.inside ? <span style={{ fontSize: `${size * 0.55}px`, lineHeight: 1 }}>🤖</span> : agent.owner.slice(0, 1)}
      </span>
      <span
        aria-hidden="true"
        class={cx("pointer-events-none absolute -inset-[3px] rounded-full border-2 border-dashed", agent.active && "animate-[agent-pulse_1.6s_ease-in-out_infinite]")}
        style={{ borderColor: agent.color }}
      />
      {!agent.inside && (
        <span aria-hidden="true" class="absolute -right-[3px] -bottom-[3px] leading-none" style={{ fontSize: `${size * 0.45}px` }}>
          🤖
        </span>
      )}
    </span>
  );
}
