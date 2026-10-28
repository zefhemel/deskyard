// An environment's or volume's backups: its policy, last result, snapshots,
// and the ways back (browse and download, restore to a folder, here, or as new).
import { useEffect, useState } from "preact/hooks";
import { api, type BackupItem, type ItemKind, type Snapshot } from "../../api.ts";
import { describeReason, describeRetention, describeSchedule, isTime, itemCopy } from "../../lib/backup-policy.ts";
import { ago, bytes, dateTime } from "../../lib/format.ts";
import { useBackupItem, useBackups, useFetch } from "../../lib/use-fetch.ts";
import { JobProgress, useJob } from "../job.tsx";
import { notify } from "../toast.tsx";
import { Button, ErrorText } from "../ui.tsx";
import { BrowseDialog } from "./browse-dialog.tsx";
import { PolicyDialog } from "./policy-dialog.tsx";
import { RestoreHereDialog, RestoreNewDialog } from "./restore-dialogs.tsx";

/** `stoppers` are the environments a restore here stops: the environment itself, or those granted the volume. */
export function BackupsPanel({ kind, name, stoppers, hostPath }: { kind: ItemKind; name: string; stoppers: string[]; hostPath?: string }) {
  const copy = itemCopy(kind, name);
  const item = useBackupItem(kind, name);
  const overview = useBackups();
  const snaps = useFetch(() => api.snapshots(kind, name), `snapshots-${kind}-${name}`);
  const [editing, setEditing] = useState(false);
  const [browsing, setBrowsing] = useState<Snapshot | null>(null);
  const [restoring, setRestoring] = useState<{ snap: Snapshot; mode: "here" | "new" } | null>(null);
  const poll = () => {
    item.reload();
    snaps.reload();
  };
  // After a change made here, the lists elsewhere (the overview) change too.
  const refresh = () => {
    poll();
    overview.reload();
  };
  const run = useJob(() => {
    notify(`Backed up ${name}`);
    refresh();
  }, copy.jobEnvironment);
  // A scheduled backup or a restore elsewhere changes what's shown.
  useEffect(() => {
    if (!item.data?.activity) return;
    const t = setInterval(poll, 5000);
    return () => clearInterval(t);
  }, [item.data?.activity]);

  const repos = overview.data?.repositories ?? [];
  const manyRepos = new Set(snaps.data?.snapshots.map((s) => s.repository)).size > 1;
  const b = item.data;
  return (
    <section aria-label="Backups">
      <ErrorText error={item.error || overview.error} />
      {b && (
        <div class="rounded-[10px] border border-line bg-surface p-5">
          <div class="flex flex-wrap items-start gap-x-4 gap-y-2">
            <div class="mr-auto min-w-0">
              <h2 class="text-lg font-semibold">
                {!b.policy ? "Not backed up" : `${b.policy.enabled ? describeSchedule(b.policy.schedule) : "Paused"} → ${b.policy.repository}`}
              </h2>
              <p class="text-sm text-muted">{b.policy ? `Keeps ${describeRetention(b.policy.retention)}` : copy.backsUp}</p>
            </div>
            {b.policy && (
              <Button variant="primary" disabled={run.running || !!b.activity} onClick={() => void run.start(() => api.runBackup(kind, name))}>
                Back up now
              </Button>
            )}
            <Button onClick={() => setEditing(true)} disabled={!repos.length && !b.policy}>
              {b.policy ? "Change" : "Set up backups"}
            </Button>
          </div>
          {!repos.length && !b.policy && overview.data && (
            <p class="mt-3 text-sm text-muted">
              <a href="/volumes?tab=backups">Add a backup repository</a> first.
            </p>
          )}
          <Status item={b} />
          <div class="mt-3">
            <JobProgress job={run.job} />
            <ErrorText error={run.error} />
          </div>
        </div>
      )}
      <h3 class="mt-6 mb-2 text-base font-semibold">Snapshots</h3>
      <ErrorText error={snaps.error} />
      {!snaps.data ? (
        !snaps.error && <p class="text-sm text-muted">Loading…</p>
      ) : !snaps.data.snapshots.length ? (
        <p class="text-sm text-muted">No snapshots yet.</p>
      ) : (
        <ul class="m-0 list-none divide-y divide-hairline rounded-[10px] border border-line bg-surface p-0">
          {snaps.data.snapshots.map((s) => (
            <li key={s.id} class="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
              <div class="mr-auto min-w-0">
                <div class="font-semibold">{dateTime(s.time)}</div>
                <div class="text-sm text-muted">
                  {[describeReason(s.reason), s.summary && bytes(s.summary.totalBytes), manyRepos && s.repository].filter(Boolean).join(" · ")}
                </div>
              </div>
              <div class="flex flex-wrap gap-2">
                <Button onClick={() => setBrowsing(s)}>Browse</Button>
                <Button onClick={() => setRestoring({ snap: s, mode: "new" })}>Restore as new</Button>
                <Button onClick={() => setRestoring({ snap: s, mode: "here" })}>Restore here</Button>
              </div>
            </li>
          ))}
        </ul>
      )}
      {editing && b && <PolicyDialog kind={kind} name={name} item={b} repositories={repos} onClose={() => setEditing(false)} onSaved={refresh} />}
      {browsing && <BrowseDialog kind={kind} name={name} snapshot={browsing} onClose={() => setBrowsing(null)} />}
      {restoring?.mode === "here" && (
        <RestoreHereDialog
          kind={kind}
          name={name}
          snapshot={restoring.snap}
          hasPolicy={!!b?.policy}
          stoppers={stoppers}
          hostPath={hostPath}
          onClose={() => setRestoring(null)}
          onDone={refresh}
        />
      )}
      {restoring?.mode === "new" && <RestoreNewDialog kind={kind} name={name} snapshot={restoring.snap} onClose={() => setRestoring(null)} />}
    </section>
  );
}

function Status({ item: b }: { item: BackupItem }) {
  const st = b.state;
  return (
    <dl class="mt-4 grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[9rem_1fr] sm:gap-y-2">
      <dt class="text-muted">Last backup</dt>
      <dd>{isTime(st.lastSuccess) ? ago(st.lastSuccess) : "Never"}</dd>
      {b.activity && (
        <>
          <dt class="text-muted">Now</dt>
          <dd class="capitalize">{b.activity}…</dd>
        </>
      )}
      {b.nextRun && isTime(b.nextRun) && (
        <>
          <dt class="text-muted">Next backup</dt>
          <dd>{dateTime(b.nextRun)}</dd>
        </>
      )}
      {!st.error && !!st.warnings?.length && (
        <>
          <dt class="text-[#8a5a12]">Left out</dt>
          <dd class="min-w-0 break-words text-[#8a5a12]">
            {st.warnings.length} file{st.warnings.length === 1 ? "" : "s"} couldn't be read: {st.warnings.join("; ")}
          </dd>
        </>
      )}
      {b.policy?.enabled && isTime(b.nextRun) && Date.parse(b.nextRun) < Date.now() - 3600_000 && !b.activity && (
        <>
          <dt class="text-danger">Overdue</dt>
          <dd class="text-danger">The backup due {ago(b.nextRun).toLowerCase()} hasn't run.</dd>
        </>
      )}
      {st.error && (
        <>
          <dt class="text-danger">Failed {isTime(st.lastAttempt) ? ago(st.lastAttempt).toLowerCase() : ""}</dt>
          <dd class="min-w-0 break-words text-danger">{st.error}</dd>
        </>
      )}
    </dl>
  );
}
