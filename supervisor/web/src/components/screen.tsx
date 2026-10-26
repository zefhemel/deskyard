// An environment's desktop in a monitor bezel: the live thumbnail when it runs,
// Start when it's stopped, progress while it's busy.
import type { ComponentChildren } from "preact";
import { useState } from "preact/hooks";
import { api, type AgentPresence, type Environment, type Job } from "../api.ts";
import { badgeLabel } from "../lib/activity-format.ts";
import { Avatars } from "./avatar.tsx";
import { Button, cx, errorMessage, Progress } from "./ui.tsx";

const glassRing = "rgba(15,21,29,.78)";

/** The thumbnail URL changes every 10 s, so the browser fetches a fresh one. */
export function thumbBust(now: number): number {
  return Math.floor(now / 10000);
}

export function ViewersBadge({
  viewers,
  mics = [],
  agents = [],
  long,
}: {
  viewers: string[];
  mics?: string[];
  agents?: AgentPresence[];
  long?: boolean;
}) {
  if (!viewers.length && !agents.length) return null;
  const label = badgeLabel(viewers, agents, long);
  return (
    <span class="absolute top-2.5 left-2.5 inline-flex items-center gap-1.5 rounded-full bg-[rgba(15,21,29,.78)] py-[3px] pr-[9px] pl-1 text-[12.5px] font-semibold text-white">
      <Avatars names={viewers} mics={mics} agents={agents} ring={glassRing} size={22} />
      {label}
    </span>
  );
}

interface MonitorProps {
  environment: Environment;
  job?: Job | undefined;
  now: number;
  big?: boolean;
  /** Where clicking the screen goes; the desktop when unset. The Open desktop button always opens the desktop. */
  href?: string;
}

export function Monitor({ environment: c, job, now, big, href }: MonitorProps) {
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
        <ViewersBadge viewers={c.viewers} mics={c.mics} agents={c.agents} long={big} />
      </>
    );
    const desktop = `/${c.name}/`;
    const button = cx(
      "absolute rounded-[7px] bg-accent font-semibold text-white no-underline shadow-[0_6px_16px_-6px_rgba(10,16,24,.6)] transition-opacity",
      big ? "right-3.5 bottom-3.5 px-4 py-[9px] text-[15px]" : "right-2.5 bottom-2.5 px-3 py-[7px] text-sm opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 max-sm:opacity-100",
    );
    const outline = "hover:outline-3 hover:outline-offset-2 hover:outline-accent focus-visible:outline-3 focus-visible:outline-offset-2 focus-visible:outline-accent";
    if (!href || href === desktop) {
      return (
        <a href={desktop} title={`Open ${c.name}'s desktop`} class={cx(bezel, "group", outline)}>
          <div class={glass}>
            {inner}
            <span class={button}>Open desktop</span>
          </div>
        </a>
      );
    }
    // The screen and the button are separate links: links can't nest.
    return (
      <div class="group relative">
        <a href={href} title={`${c.name}'s overview`} class={cx(bezel, outline)}>
          <div class={glass}>{inner}</div>
        </a>
        <a href={desktop} title={`Open ${c.name}'s desktop`} class={cx(button, "hover:brightness-110 focus-visible:outline-3 focus-visible:outline-offset-2 focus-visible:outline-white", big ? "" : "mr-[7px] mb-[7px]")}>
          Open desktop
        </a>
      </div>
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
