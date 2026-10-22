// The live log of an image build: follows the job's event stream (each
// update is one log line), keeps itself scrolled to the end unless the reader
// scrolled up, and marks the step that failed.
import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import { api, watchJob, type Job } from "../api.ts";
import { notifyError } from "../components/toast.tsx";
import { Button, cx, Dialog } from "../components/ui.tsx";

const STEP = /^Step \d+\/\d+ : /;
const CANCELLED = "cancelled";

/** The index of the last "Step n/m" line: the one a failed build stopped in. */
export function failedStep(lines: string[]): number {
  for (let i = lines.length - 1; i >= 0; i--) if (STEP.test(lines[i] ?? "")) return i;
  return -1;
}

export function BuildLogDialog({ title, job: id, onClose, onDone }: { title: string; job: string; onClose: (stillRunning: boolean) => void; onDone: () => void }) {
  const [lines, setLines] = useState<string[]>([]);
  const [final, setFinal] = useState<Job | null>(null);
  const [cancelling, setCancelling] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const stuck = useRef(true);
  const done = useRef(onDone);
  done.current = onDone;

  useEffect(() => {
    // Lines arrive in bursts (the stream replays the history first): batch the renders.
    let pending: string[] = [];
    let timer: number | undefined;
    const flush = () => {
      timer = undefined;
      const add = pending;
      pending = [];
      if (add.length) setLines((l) => l.concat(add));
    };
    const stop = watchJob(id, (j) => {
      if (j.status === "running") {
        if (j.message) pending.push(j.message);
        if (timer === undefined) timer = window.setTimeout(flush, 60);
        return;
      }
      window.clearTimeout(timer);
      flush();
      setFinal(j);
      if (j.status === "done") done.current();
    });
    return () => {
      stop();
      window.clearTimeout(timer);
    };
  }, [id]);

  useLayoutEffect(() => {
    const el = box.current;
    if (el && stuck.current) el.scrollTop = el.scrollHeight;
  }, [lines, final]);

  const running = final === null;
  const failed = final?.status === "failed";
  const wasCancelled = failed && final.error === CANCELLED;
  const step = failed && !wasCancelled ? failedStep(lines) : -1;
  const cancel = () => {
    setCancelling(true);
    api.cancelJob(id).catch((e: unknown) => {
      setCancelling(false);
      notifyError(e);
    });
  };

  return (
    <Dialog
      wide
      title={`Building ${title}`}
      intro={running ? "You can close this: the build keeps going." : undefined}
      onClose={() => onClose(running)}
      footer={
        <>
          {running && (
            <Button variant="danger" disabled={cancelling} onClick={cancel}>
              {cancelling ? "Cancelling…" : "Cancel build"}
            </Button>
          )}
          <Button variant={running ? "quiet" : "primary"} onClick={() => onClose(running)}>
            Close
          </Button>
        </>
      }
    >
      <div
        ref={box}
        role="log"
        aria-label="Build log"
        class="mono mb-3.5 h-[320px] overflow-auto rounded-lg bg-bezel px-3 py-2.5 text-[12.5px] leading-[1.5] text-[#c5cdd8]"
        onScroll={(e) => {
          const el = e.currentTarget;
          stuck.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
        }}
      >
        {!lines.length && <div class="text-[#8b95a3]">Waiting for the build to start…</div>}
        {lines.map((l, i) => (
          <div key={i} class={cx("break-words whitespace-pre-wrap", i === step && "-mx-1 rounded bg-[#5c1f1a] px-1 font-semibold text-[#ffd6d1]")}>
            {l}
          </div>
        ))}
      </div>
      {final?.status === "done" && (
        <p class="mb-3.5 text-sm text-run" role="status">
          {title} is ready to use.
        </p>
      )}
      {wasCancelled && (
        <p class="mb-3.5 text-sm text-muted" role="status">
          Build cancelled. Nothing was changed.
        </p>
      )}
      {failed && !wasCancelled && (
        <div role="alert" class="mb-3.5 text-sm text-danger">
          {step >= 0 && (
            <p class="mb-1 font-semibold">
              Failed in <span class="mono font-normal">{lines[step]}</span>
            </p>
          )}
          <p class="[overflow-wrap:anywhere]">{final.error || final.message || "The build failed."}</p>
        </div>
      )}
    </Dialog>
  );
}
