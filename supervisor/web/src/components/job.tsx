// Running a long operation (create, settings change, upgrade, image pull)
// from a dialog: start it, follow /api/jobs/{id}, show its progress.
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import type { Job, JobRef } from "../api.ts";
import { useLive } from "../live.tsx";
import { notify } from "./toast.tsx";
import { errorMessage, Progress } from "./ui.tsx";

export interface JobRun {
  /** null before starting. */
  job: Job | null;
  running: boolean;
  error: string | null;
  start: (begin: () => Promise<JobRef>) => Promise<void>;
}

/** `onDone` runs once the job finishes successfully. */
export function useJob(onDone: (job: Job) => void, computer?: string): JobRun {
  const { jobTracker } = useLive();
  const [id, setID] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);
  const job: Job | null = starting
    ? { status: "running", message: "Starting", progress: null }
    : computer ? jobTracker.forComputer(computer) : id ? jobTracker.get(id) : null;
  const [error, setError] = useState<string | null>(null);
  const mounted = useRef(true);
  const done = useRef(onDone);
  done.current = onDone;

  // Closing the dialog doesn't stop following the job: its progress stays on the computer's tile.
  useEffect(
    () => () => {
      mounted.current = false;
    },
    [],
  );

  const start = useCallback(
    async (begin: () => Promise<JobRef>) => {
      setError(null);
      setStarting(true);
      try {
        const { job: id } = await begin();
        setID(id);
        jobTracker.track(id, (j) => {
          if (!mounted.current) {
            if (j.status === "failed") notify(j.error || j.message || "A job failed", { error: true, ms: 8000 });
            return;
          }
          if (j.status === "done") done.current(j);
        }, computer);
      } catch (e) {
        setError(errorMessage(e));
      } finally {
        setStarting(false);
      }
    },
    [jobTracker, computer],
  );

  return { job, error: error || (job?.status === "failed" ? job.error || job.message || "Failed" : null), running: job?.status === "running", start };
}

/** The progress block shown in a dialog while its job runs. */
export function JobProgress({ job }: { job: Job | null }) {
  if (!job || job.status !== "running") return null;
  return (
    <div class="mb-[18px] rounded-lg bg-bezel px-4 py-3.5 text-sm text-[#c5cdd8]">
      <Progress value={job.progress} class="mb-2.5" />
      {job.message || "Working"}
      {job.progress !== null && <span class="text-[#8b95a3]"> · {Math.round(job.progress * 100)}%</span>}
    </div>
  );
}
