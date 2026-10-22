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
export function useJob(onDone: (job: Job) => void): JobRun {
  const { trackJob } = useLive();
  const [job, setJob] = useState<Job | null>(null);
  const [error, setError] = useState<string | null>(null);
  const mounted = useRef(true);
  const done = useRef(onDone);
  done.current = onDone;

  // Closing the dialog doesn't stop following the job: its progress stays on the desktop's tile.
  useEffect(
    () => () => {
      mounted.current = false;
    },
    [],
  );

  const start = useCallback(
    async (begin: () => Promise<JobRef>) => {
      setError(null);
      setJob({ status: "running", message: "Starting", progress: null });
      try {
        const { job: id } = await begin();
        trackJob(id, (j) => {
          if (!mounted.current) {
            if (j.status === "failed") notify(j.error || j.message || "A job failed", { error: true, ms: 8000 });
            return;
          }
          setJob(j);
          if (j.status === "failed") setError(j.error || j.message || "Failed");
          if (j.status === "done") done.current(j);
        });
      } catch (e) {
        setJob(null);
        setError(errorMessage(e));
      }
    },
    [trackJob],
  );

  return { job, error, running: job?.status === "running", start };
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
