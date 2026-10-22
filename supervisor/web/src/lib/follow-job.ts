// Follows a job's event stream like watchJob, but a dropped connection is not
// a result: it reconnects (the server replays the job's whole history to a new
// subscriber, so `onReplay` says to start the log over) and only reports a
// real terminal state. A job the server no longer knows is `onGone`.
import { goToLogin, type Job } from "../api.ts";

export interface JobFollower {
  onUpdate: (job: Job) => void;
  /** The stream is about to replay from the start: forget what was shown. */
  onReplay: () => void;
  /** True while the connection is down and being retried. */
  onConnection: (lost: boolean) => void;
  /** The server has forgotten the job (it finished long ago). */
  onGone: () => void;
}

const BACKOFF_MS = [500, 1000, 2000, 4000, 8000];

/** Returns a function that stops following. */
export function followJob(id: string, f: JobFollower): () => void {
  const url = `/api/jobs/${encodeURIComponent(id)}`;
  let stopped = false;
  let es: EventSource | null = null;
  let retry: number | undefined;
  let attempt = 0;
  // Set once the first connection opened: a later open replays the history.
  let reconnecting = false;

  const finish = () => {
    stopped = true;
    es?.close();
    window.clearTimeout(retry);
  };

  const lost = () => {
    if (stopped) return;
    es?.close();
    es = null;
    f.onConnection(true);
    // Why did it end? A 404 means the job is gone; 401 means we were signed out.
    fetch(url, { credentials: "same-origin", headers: { Accept: "text/event-stream" } }).then(
      (res) => {
        void res.body?.cancel();
        if (stopped) return;
        if (res.status === 404) {
          finish();
          f.onGone();
        } else if (res.status === 401) {
          finish();
          goToLogin();
        } else schedule();
      },
      () => schedule(),
    );
  };

  const schedule = () => {
    if (stopped) return;
    const delay = BACKOFF_MS[Math.min(attempt++, BACKOFF_MS.length - 1)] ?? 8000;
    retry = window.setTimeout(connect, delay);
  };

  const connect = () => {
    if (stopped) return;
    const source = new EventSource(url);
    es = source;
    source.onopen = () => {
      attempt = 0;
      if (reconnecting) f.onReplay();
      reconnecting = true;
      f.onConnection(false);
    };
    source.onmessage = (e: MessageEvent<string>) => {
      const job = JSON.parse(e.data) as Job;
      if (job.status !== "running") finish();
      f.onUpdate(job);
    };
    source.onerror = () => {
      if (!stopped && es === source) lost();
    };
  };

  connect();
  return finish;
}
