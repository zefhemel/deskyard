// A desktop's screen in a monitor bezel: the live thumbnail when it runs,
// Start when it's stopped, progress while it's busy.
import type { ComponentChildren } from "preact";
import { useState } from "preact/hooks";
import { api, type AgentPresence, type Desktop, type Job } from "../api.ts";
import { agentLabel } from "../lib/activity-format.ts";
import { names } from "../lib/format.ts";
import { Avatars } from "./avatar.tsx";
import { Button, cx, errorMessage, Progress } from "./ui.tsx";

const glassRing = "rgba(15,21,29,.78)";

/** The thumbnail URL changes every 10 s, so the browser fetches a fresh one. */
export function thumbBust(now: number): number {
  return Math.floor(now / 10000);
}

export function ViewersBadge({ viewers, agents = [], long }: { viewers: string[]; agents?: AgentPresence[]; long?: boolean }) {
  if (!viewers.length && !agents.length) return null;
  const people = long
    ? `${names(viewers)} ${viewers.length === 1 ? "is" : "are"} viewing`
    : viewers.length === 1
      ? viewers[0]
      : `${viewers.length} viewing`;
  const bots = agents.length === 1 ? (long ? `${agentLabel(agents[0]!)} is here` : agents[0]!.name) : `${agents.length} agents${long ? " here" : ""}`;
  const label = [viewers.length ? people : "", agents.length ? bots : ""].filter(Boolean).join(" · ");
  return (
    <span class="absolute top-2.5 left-2.5 inline-flex items-center gap-1.5 rounded-full bg-[rgba(15,21,29,.78)] py-[3px] pr-[9px] pl-1 text-[12.5px] font-semibold text-white">
      <Avatars names={viewers} agents={agents} ring={glassRing} size={22} />
      {label}
    </span>
  );
}

interface MonitorProps {
  desktop: Desktop;
  job?: Job | undefined;
  now: number;
  big?: boolean;
}

export function Monitor({ desktop: c, job, now, big }: MonitorProps) {
  const [startError, setStartError] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);
  const [broken, setBroken] = useState(false);

  const bezel = cx(
    "block bg-bezel shadow-[0_1px_0_rgba(255,255,255,.6)_inset,0_10px_24px_-14px_rgba(23,34,49,.55)]",
    big ? "rounded-[14px] p-[9px]" : "rounded-xl p-[7px]",
  );
  const glass = "relative aspect-[16/10] overflow-hidden rounded-[5px] bg-glass";

  let inner: ComponentChildren;
  if (c.state === "running") {
    const src = api.screenUrl(c.name, { bust: thumbBust(now) });
    inner = (
      <>
        <img
          src={src}
          alt=""
          class={cx("block h-full w-full object-cover object-top-left", broken && "invisible")}
          onError={() => setBroken(true)}
          onLoad={() => setBroken(false)}
        />
        <ViewersBadge viewers={c.viewers} agents={c.agents} long={big} />
        <span
          class={cx(
            "absolute rounded-[7px] bg-accent font-semibold text-white shadow-[0_6px_16px_-6px_rgba(10,16,24,.6)] transition-opacity",
            big ? "right-3.5 bottom-3.5 px-4 py-[9px] text-[15px]" : "right-2.5 bottom-2.5 px-3 py-[7px] text-sm opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100 max-sm:opacity-100",
          )}
        >
          Open desktop
        </span>
      </>
    );
    return (
      <a
        href={`/${c.name}/`}
        title={`Open ${c.name}'s desktop`}
        class={cx(bezel, "group hover:outline-3 hover:outline-offset-2 hover:outline-accent focus-visible:outline-3 focus-visible:outline-offset-2 focus-visible:outline-accent")}
      >
        <div class={glass}>{inner}</div>
      </a>
    );
  }

  const blank = "absolute inset-0 flex flex-col items-center justify-center gap-3 px-4 text-center text-sm text-[#9aa6b5]";
  if (c.state === "stopped") {
    const start = async () => {
      setStarting(true);
      setStartError(null);
      try {
        await api.power(c.name, "start");
      } catch (e) {
        setStartError(errorMessage(e));
      } finally {
        setStarting(false);
      }
    };
    inner = (
      <div class={blank}>
        Stopped
        <Button variant="primary" class="h-8!" disabled={starting} onClick={start}>
          {starting ? "Starting…" : "Start"}
        </Button>
        {startError && <span class="text-[#f0a49b]">{startError}</span>}
      </div>
    );
  } else {
    const text = job?.message || { starting: "Starting", creating: "Creating", upgrading: "Upgrading", removing: "Removing" }[c.state];
    inner = (
      <div class={blank}>
        <Progress value={job?.progress ?? null} class="w-[62%]" />
        {text}
      </div>
    );
  }
  return (
    <div class={bezel}>
      <div class={glass}>{inner}</div>
    </div>
  );
}
