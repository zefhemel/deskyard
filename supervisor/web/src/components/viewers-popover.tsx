// Who is viewing an environment's desktop, and the port mappings agents set up with
// `deskyard forward`. The popover is the desktop bar's; ForwardLines is also
// the details page's "viewing" card.
import { useEffect, useRef, useState } from "preact/hooks";
import { api, type AgentPresence, type Forward } from "../api.ts";
import { useLive } from "../live.tsx";
import { agentTitle } from "../lib/activity-format.ts";
import { selfFirst } from "../lib/format.ts";
import { mayManageShare } from "../lib/permissions.ts";
import { AgentAvatar } from "./agent-avatar.tsx";
import { AgentSummary, useActivity } from "./agent-summary.tsx";
import { Avatar } from "./avatar.tsx";
import { RevokeShare } from "./revoke-share.tsx";

/** One line per token, "deploy-bot: 3000 in · 5173 out", in first-seen order. */
export function forwardLines(forwards: Forward[]): { token: string; text: string }[] {
  const byToken = new Map<string, string[]>();
  for (const f of forwards) {
    const parts = byToken.get(f.token) ?? [];
    parts.push(`${f.port} ${f.direction}`);
    byToken.set(f.token, parts);
  }
  return [...byToken].map(([token, parts]) => ({ token, text: `${token}: ${parts.join(" · ")}` }));
}

export function ForwardLines({ forwards, class: extra }: { forwards: Forward[]; class?: string }) {
  if (!forwards.length) return null;
  return (
    <ul class={`m-0 list-none p-0 ${extra ?? ""}`} aria-label="Port mappings">
      {forwardLines(forwards).map((l) => (
        <li key={l.token} class="mono text-[13px] text-muted">
          {l.text}
        </li>
      ))}
    </ul>
  );
}

const hasHover = () => window.matchMedia("(hover: hover)").matches;

/** The agents, one row each. Hovering a row (where there is hover) or clicking/tapping it opens its summary. */
function AgentRows({ name, agents }: { name: string; agents: AgentPresence[] }) {
  const activity = useActivity(name);
  // Hovering opens a row for as long as the pointer is on it; a click keeps it open.
  const [open, setOpen] = useState<{ id: string; pinned: boolean } | null>(null);
  return (
    <ul class="m-0 mb-1 list-none p-0">
      {agents.map((a) => {
        const expanded = open?.id === a.id;
        return (
          <li
            key={a.id}
            onMouseEnter={() => hasHover() && setOpen((o) => (o?.id === a.id ? o : { id: a.id, pinned: false }))}
            onMouseLeave={() => setOpen((o) => (o?.id === a.id && !o.pinned ? null : o))}
          >
            <button
              type="button"
              aria-expanded={expanded}
              class="flex w-full cursor-pointer items-center gap-2 rounded-md border-0 bg-transparent px-0 py-1 text-left text-sm text-ink hover:bg-hairline"
              onClick={() => setOpen(expanded && open?.pinned ? null : { id: a.id, pinned: true })}
            >
              <AgentAvatar agent={a} size={22} />
              <span class="min-w-0 flex-1 truncate">{agentTitle(a)}</span>
              <span aria-hidden="true" class="text-[10px] text-muted">
                {expanded ? "▴" : "▾"}
              </span>
            </button>
            {expanded && (
              <div class="mt-0.5 mb-1.5 ml-[30px]">
                <AgentSummary summary={activity.agents?.find((s) => s.id === a.id)} status={activity.status} />
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
}

export function ViewersPopover({
  name,
  viewers,
  observers,
  mics,
  forwards,
  agents,
  canShare,
  onClose,
}: {
  name: string;
  viewers: string[];
  /** The viewers watching at observe level: they can't use the desktop. */
  observers: string[];
  mics: string[];
  forwards: Forward[];
  agents: AgentPresence[];
  /** Whether the reader may make shares (can(me, environment).share), so may manage some. */
  canShare?: boolean;
  onClose: () => void;
}) {
  const { me } = useLive();
  const ref = useRef<HTMLDivElement>(null);
  const revocable = useRevocableShares(name, canShare === true);

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node;
      // The toggle button handles its own clicks.
      if (ref.current && !ref.current.contains(t) && !(t instanceof Element && t.closest("[data-viewers-toggle]"))) onClose();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    // A click into the desktop's frame blurs this window.
    const onBlur = () => onClose();
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    window.addEventListener("blur", onBlur);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
      window.removeEventListener("blur", onBlur);
    };
  }, [onClose]);

  return (
    <div
      ref={ref}
      class="absolute top-[50px] right-2.5 z-40 w-[320px] max-w-[calc(100vw-20px)] max-h-[calc(100dvh-70px)] overflow-y-auto rounded-[10px] bg-surface p-3.5 text-ink shadow-[0_18px_40px_-12px_rgba(0,0,0,.6)]"
    >
      <h3 class="mb-1.5 text-[13px] font-semibold text-muted">Viewing</h3>
      {viewers.length ? (
        <ul class="m-0 mb-1 list-none p-0">
          {selfFirst(viewers, me.username).map((v) => (
            <li key={v} class="flex items-center gap-2 py-1 text-sm">
              <Avatar name={v} size={22} mic={mics.includes(v)} />
              <span class="truncate">{v}</span>
              <span class="ml-auto flex flex-none items-center gap-1.5">
                {(mics.includes(v) || observers.includes(v)) && (
                  <span class="text-[12px] text-muted" title={observers.includes(v) ? "Watching, without keyboard or mouse" : undefined}>
                    {[...(observers.includes(v) ? ["observing"] : []), ...(mics.includes(v) ? ["mic on"] : [])].join(", ")}
                  </span>
                )}
                {revocable.has(v) && <RevokeShare username={v} compact />}
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <p class="m-0 mb-1 text-sm text-muted">Nobody</p>
      )}
      {agents.length > 0 && (
        <>
          <h3 class="mt-3 mb-1.5 text-[13px] font-semibold text-muted">CLI activity</h3>
          <AgentRows name={name} agents={agents} />
        </>
      )}
      {forwards.length > 0 && (
        <>
          <h3 class="mt-3 mb-1.5 text-[13px] font-semibold text-muted">Port mappings</h3>
          <ForwardLines forwards={forwards} class="[&_li]:py-0.5 [&_li]:text-ink" />
        </>
      )}
    </div>
  );
}


/** The shares of the environment the reader may revoke: an admin all of them, others their own. */
function useRevocableShares(name: string, enabled: boolean): Set<string> {
  const { me } = useLive();
  const [names, setNames] = useState<Set<string>>(new Set());
  useEffect(() => {
    if (!enabled) return;
    let live = true;
    api.shares(name).then(
      (shares) => live && setNames(new Set(shares.filter((s) => mayManageShare(me, s)).map((s) => s.username))),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [name, enabled, me.admin, me.username]);
  return names;
}
