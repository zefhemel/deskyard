// The desktop bar's Clipboard popover: the desktop's clipboard (from the
// frame's clipboardrx) with Copy, and a box to send text to it (clipboardsnd).
import { useEffect, useRef, useState } from "preact/hooks";
import { copyText } from "../lib/clipboard.ts";
import { Button } from "../components/ui.tsx";

export function ClipboardPopover({
  name,
  frame,
  remote,
  onSend,
  onClose,
}: {
  name: string;
  frame: HTMLIFrameElement | null;
  /** The desktop's clipboard text, or null before it has sent any. */
  remote: string | null;
  onSend: (text: string) => void;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [text, setText] = useState("");
  const [copied, setCopied] = useState(false);

  useEffect(() => setCopied(false), [remote]);

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node;
      // The toggle button handles its own clicks.
      if (ref.current && !ref.current.contains(t) && !(t instanceof Element && t.closest("[data-clipboard-toggle]"))) onClose();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    // Mobile keyboard/browser focus changes can blur the window too. Close
    // only after focus actually enters the desktop frame, keeping the draft.
    let blurTimer: number | undefined;
    const onBlur = () => {
      window.clearTimeout(blurTimer);
      blurTimer = window.setTimeout(() => {
        if (document.activeElement instanceof HTMLIFrameElement) onClose();
      }, 0);
    };
    // Selkies prevents default touch focus, so a tap can leave the parent's
    // textarea active. Observe the actual screen interaction as well as blur.
    let screenWindow: Window | null | undefined;
    const attach = () => {
      screenWindow?.removeEventListener("pointerdown", onClose, true);
      screenWindow = frame?.contentWindow;
      screenWindow?.addEventListener("pointerdown", onClose, true);
    };
    attach();
    frame?.addEventListener("load", attach);
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    window.addEventListener("blur", onBlur);
    return () => {
      frame?.removeEventListener("load", attach);
      screenWindow?.removeEventListener("pointerdown", onClose, true);
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
      window.removeEventListener("blur", onBlur);
      window.clearTimeout(blurTimer);
    };
  }, [frame, onClose]);

  return (
    <div
      ref={ref}
      class="absolute top-[50px] right-2.5 z-40 w-[340px] max-w-[calc(100vw-20px)] rounded-[10px] bg-surface p-3.5 text-ink shadow-[0_18px_40px_-12px_rgba(0,0,0,.6)]"
    >
      <h3 class="mb-1.5 text-[13px] font-semibold text-muted">On {name}'s clipboard</h3>
      <div class="mono max-h-[70px] overflow-auto rounded-[7px] border border-line bg-white px-2.5 py-2 text-[13px]! leading-[1.45] break-all whitespace-pre-wrap">
        {remote ?? <span class="font-sans text-muted">Nothing copied on {name} yet.</span>}
      </div>
      <div class="mt-2 mb-3.5 flex justify-end gap-2">
        <Button class="h-[30px]! px-3! text-[13px]!" disabled={!remote} onClick={async () => setCopied(await copyText(remote ?? ""))}>
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
      <h3 class="mb-1.5 text-[13px] font-semibold text-muted">Send to {name}</h3>
      <textarea
        class="mono h-16 resize-none px-2.5 py-2 text-[13px]! leading-[1.45]"
        placeholder={`Text to put on ${name}'s clipboard`}
        value={text}
        onInput={(e) => setText(e.currentTarget.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && text) onSend(text);
        }}
      />
      <div class="mt-2 flex justify-end gap-2">
        <Button variant="primary" class="h-[30px]! px-3! text-[13px]!" disabled={!text} onClick={() => onSend(text)}>
          Paste into {name}
        </Button>
      </div>
    </div>
  );
}
