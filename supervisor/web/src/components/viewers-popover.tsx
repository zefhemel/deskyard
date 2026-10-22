// Who is viewing a desktop, and the port mappings agents have set up with
// `deskyard forward`. The popover is the desktop bar's; ForwardLines is also
// the details page's "viewing" card.
import { useEffect, useRef } from "preact/hooks";
import type { Forward } from "../api.ts";
import { Avatar } from "./avatar.tsx";

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

export function ViewersPopover({ viewers, forwards, onClose }: { viewers: string[]; forwards: Forward[]; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);

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
      class="absolute top-[50px] left-2.5 z-40 w-[300px] max-w-[calc(100vw-20px)] rounded-[10px] bg-surface p-3.5 text-ink shadow-[0_18px_40px_-12px_rgba(0,0,0,.6)]"
    >
      <h3 class="mb-1.5 text-[13px] font-semibold text-muted">Viewing</h3>
      {viewers.length ? (
        <ul class="m-0 mb-1 list-none p-0">
          {viewers.map((v) => (
            <li key={v} class="flex items-center gap-2 py-1 text-sm">
              <Avatar name={v} size={22} />
              <span class="truncate">{v}</span>
            </li>
          ))}
        </ul>
      ) : (
        <p class="m-0 mb-1 text-sm text-muted">Nobody</p>
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
