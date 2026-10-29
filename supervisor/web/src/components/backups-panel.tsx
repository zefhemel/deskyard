// A computer's or volume's backups: its policy, last result, snapshots,
// and the ways back (browse and download, restore to a folder, here, or as new).
import { useEffect, useState } from "preact/hooks";
import { api, type BackupItem, type ItemKind, type PolicyInput, type Repository, type RestoreResult, type Snapshot, type SnapshotEntry } from "../api.ts";
import { describeReason, describeRetention, describeSchedule, isTime, policyDefaults, retentionErrors } from "../lib/backup-policy.ts";
import { ago, bytes, day } from "../lib/format.ts";
import { useFetch } from "../lib/use-fetch.ts";
import { JobProgress, useJob } from "./job.tsx";
import { notify } from "./toast.tsx";
import { Button, Check, Dialog, ErrorText, Field, Note, Seg, errorMessage } from "./ui.tsx";

const DAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

function when(iso: string): string {
  return new Date(iso).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

/** `stoppers` are the computers a restore here stops: the computer itself, or those granted the volume. */
export function BackupsPanel({ kind, name, stoppers, hostPath }: { kind: ItemKind; name: string; stoppers: string[]; hostPath?: string }) {
  const item = useFetch(() => api.backup(kind, name), `backup-${kind}-${name}`);
  const overview = useFetch(() => api.backups(), "backups");
  const snaps = useFetch(() => api.snapshots(kind, name), `snapshots-${kind}-${name}`);
  const [editing, setEditing] = useState(false);
  const [browsing, setBrowsing] = useState<Snapshot | null>(null);
  const [restoring, setRestoring] = useState<{ snap: Snapshot; mode: "here" | "new" } | null>(null);
  const refresh = () => {
    item.reload();
    snaps.reload();
  };
  const run = useJob(() => {
    notify(`Backed up ${name}`);
    refresh();
  }, kind === "computer" ? name : undefined);
  // A scheduled backup or a restore elsewhere changes what's shown.
  useEffect(() => {
    if (!item.data?.activity) return;
    const t = setInterval(refresh, 5000);
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
              <p class="text-sm text-muted">
                {b.policy
                  ? `Keeps ${describeRetention(b.policy.retention)}`
                  : kind === "computer"
                    ? "Its settings and home folder, to a backup repository."
                    : "Its files, to a backup repository."}
              </p>
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
                <div class="font-semibold">{when(s.time)}</div>
                <div class="text-sm text-muted">
                  {[describeReason(s.reason), s.summary && bytes(s.summary.total_bytes_processed), manyRepos && s.repository].filter(Boolean).join(" · ")}
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
      {editing && b && (
        <PolicyDialog kind={kind} name={name} item={b} repositories={repos} onClose={() => setEditing(false)} onSaved={refresh} />
      )}
      {browsing && <BrowseDialog kind={kind} name={name} snapshot={browsing} onClose={() => setBrowsing(null)} />}
      {restoring?.mode === "here" && (
        <RestoreHereDialog kind={kind} name={name} snapshot={restoring.snap} hasPolicy={!!b?.policy} stoppers={stoppers} hostPath={hostPath} onClose={() => setRestoring(null)} onDone={refresh} />
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
          <dd>{when(b.nextRun)}</dd>
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

function PolicyDialog({
  kind,
  name,
  item,
  repositories,
  onClose,
  onSaved,
}: {
  kind: ItemKind;
  name: string;
  item: BackupItem;
  repositories: Repository[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [p, setP] = useState<PolicyInput>(item.policy ?? policyDefaults(repositories));
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const retErr = retentionErrors(p.retention);
  const valid = !!p.repository && !retErr && /^\d\d:\d\d$/.test(p.schedule.at);
  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      await api.putPolicy(kind, name, p);
      notify(`Saved ${name}'s backups`);
      onSaved();
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  const remove = async () => {
    setBusy(true);
    try {
      await api.deletePolicy(kind, name);
      notify(`${name} is no longer backed up; its snapshots are kept`);
      onSaved();
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  const num = (k: keyof PolicyInput["retention"], label: string) => (
    <label class="flex flex-col gap-1 text-sm">
      {label}
      <input
        type="number"
        min={0}
        class="w-full"
        value={p.retention[k]}
        onInput={(e) => setP({ ...p, retention: { ...p.retention, [k]: Number(e.currentTarget.value) } })}
      />
    </label>
  );
  return (
    <Dialog
      title={`Back up ${name}`}
      wide
      onClose={onClose}
      onSubmit={() => valid && void save()}
      footer={
        <>
          {item.policy && (
            <Button variant="quiet" class="mr-auto" disabled={busy} onClick={() => void remove()}>
              Stop backing up
            </Button>
          )}
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={!valid || busy}>
            Save
          </Button>
        </>
      }
    >
      <Field label="Repository" htmlFor="policy-repo">
        <select id="policy-repo" value={p.repository} onChange={(e) => setP({ ...p, repository: e.currentTarget.value })}>
          <option value="" disabled>
            Choose a repository
          </option>
          {repositories.map((r) => (
            <option key={r.name} value={r.name}>
              {r.name} ({r.remote}:{r.folder})
            </option>
          ))}
        </select>
      </Field>
      <Field label="When">
        <div class="flex flex-wrap items-center gap-2">
          <Seg
            label="How often"
            options={[
              { value: "hourly" as const, label: "Hourly" },
              { value: "daily" as const, label: "Daily" },
              { value: "weekly" as const, label: "Weekly" },
            ]}
            value={p.schedule.every}
            onChange={(every) => setP({ ...p, schedule: { ...p.schedule, every } })}
          />
          {p.schedule.every === "weekly" && (
            <select aria-label="Day" class="w-auto" value={p.schedule.weekday} onChange={(e) => setP({ ...p, schedule: { ...p.schedule, weekday: Number(e.currentTarget.value) } })}>
              {DAYS.map((d, i) => (
                <option key={d} value={i}>
                  {d}
                </option>
              ))}
            </select>
          )}
          <input
            type="time"
            aria-label="Time"
            class="w-auto"
            value={p.schedule.at}
            onInput={(e) => setP({ ...p, schedule: { ...p.schedule, at: e.currentTarget.value } })}
          />
        </div>
      </Field>
      <Field label="Keep" hint={retErr ? <span class="text-danger">{retErr}</span> : "Older snapshots are forgotten after each backup."}>
        <div class="grid grid-cols-3 gap-2 sm:grid-cols-6">
          {num("last", "Last")}
          {num("hourly", "Hourly")}
          {num("daily", "Daily")}
          {num("weekly", "Weekly")}
          {num("monthly", "Monthly")}
          {num("yearly", "Yearly")}
        </div>
      </Field>
      <Check id="policy-enabled" checked={p.enabled} onChange={(enabled) => setP({ ...p, enabled })} title="Back up on this schedule">
        Off pauses scheduled backups.
      </Check>
      <ErrorText error={error} />
    </Dialog>
  );
}

function BrowseDialog({ kind, name, snapshot: s, onClose }: { kind: ItemKind; name: string; snapshot: Snapshot; onClose: () => void }) {
  const [dir, setDir] = useState("");
  const [entries, setEntries] = useState<SnapshotEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [picked, setPicked] = useState<string[]>([]);
  const [done, setDone] = useState<RestoreResult | null>(null);
  const run = useJob((j) => setDone((j.result as RestoreResult) ?? {}));
  useEffect(() => {
    let live = true;
    setEntries(null);
    setError(null);
    api.snapshotFiles(kind, name, s.repository, s.id, dir).then(
      (r) => live && setEntries([...r.entries].sort((a, b) => Number(b.type === "dir") - Number(a.type === "dir") || a.name.localeCompare(b.name))),
      (e) => live && setError(errorMessage(e)),
    );
    return () => {
      live = false;
    };
  }, [dir]);
  const parts = dir ? dir.split("/") : [];
  const toggle = (path: string, on: boolean) => setPicked(on ? [...picked, path] : picked.filter((p) => p !== path));
  const download = (path: string, folder: boolean) => api.snapshotDownloadURL(kind, name, s.repository, s.id, path, folder);
  if (done) {
    return (
      <Dialog title="Files restored" onClose={onClose} onSubmit={onClose} footer={<Button type="submit" variant="primary">Done</Button>}>
        <p class="text-sm" role="status">
          In <span class="mono break-all">{done.folder}</span> {kind === "computer" ? "in the home folder" : "in the volume"}.
        </p>
      </Dialog>
    );
  }
  return (
    <Dialog
      title={`${name} on ${when(s.time)}`}
      wide
      onClose={onClose}
      onSubmit={() => picked.length && void run.start(() => api.restore(kind, name, { snapshot: s.id, repository: s.repository, mode: "folder", paths: picked }))}
      footer={
        <>
          <a class="mr-auto self-center text-sm" href={download("", true)} download>
            Download all as zip
          </a>
          <Button variant="quiet" onClick={onClose}>
            Close
          </Button>
          <Button type="submit" variant="primary" disabled={!picked.length || run.running}>
            {picked.length ? `Restore ${picked.length} to a folder` : "Restore to a folder"}
          </Button>
        </>
      }
    >
      <nav aria-label="Folder" class="mb-3 flex flex-wrap items-center gap-1 text-sm">
        <button type="button" class="cursor-pointer border-0 bg-transparent p-0 font-semibold text-accent" onClick={() => setDir("")}>
          {kind === "computer" ? "Home" : name}
        </button>
        {parts.map((p, i) => (
          <span key={i}>
            {" / "}
            <button type="button" class="cursor-pointer border-0 bg-transparent p-0 font-semibold text-accent" onClick={() => setDir(parts.slice(0, i + 1).join("/"))}>
              {p}
            </button>
          </span>
        ))}
      </nav>
      <ErrorText error={error} />
      {!entries ? (
        !error && <p class="text-sm text-muted">Loading…</p>
      ) : !entries.length ? (
        <p class="text-sm text-muted">This folder is empty.</p>
      ) : (
        <ul class="m-0 max-h-[360px] list-none divide-y divide-hairline overflow-auto rounded-lg border border-line p-0">
          {entries.map((e) => (
            <li key={e.path} class="flex items-center gap-2.5 px-3 py-2 text-sm">
              <input type="checkbox" aria-label={`Select ${e.name}`} checked={picked.includes(e.path)} onChange={(ev) => toggle(e.path, ev.currentTarget.checked)} />
              {e.type === "dir" ? (
                <button type="button" class="mr-auto min-w-0 cursor-pointer truncate border-0 bg-transparent p-0 text-left font-semibold" onClick={() => setDir(e.path)}>
                  📁 {e.name}
                </button>
              ) : (
                <span class="mr-auto min-w-0 truncate">{e.name}</span>
              )}
              <span class="shrink-0 text-muted max-sm:hidden">{e.type === "dir" ? "" : bytes(e.size)}</span>
              <a class="shrink-0" href={download(e.path, e.type === "dir")} download>
                Download
              </a>
            </li>
          ))}
        </ul>
      )}
      <p class="mt-3 text-sm text-muted">Selected items are restored into a new “Restored …” folder. Nothing is overwritten.</p>
      <JobProgress job={run.job} />
      <ErrorText error={run.error} />
    </Dialog>
  );
}

function RestoreHereDialog({
  kind,
  name,
  snapshot: s,
  hasPolicy,
  stoppers,
  hostPath,
  onClose,
  onDone,
}: {
  kind: ItemKind;
  name: string;
  snapshot: Snapshot;
  hasPolicy: boolean;
  stoppers: string[];
  hostPath?: string;
  onClose: () => void;
  onDone: () => void;
}) {
  const [first, setFirst] = useState(hasPolicy);
  const run = useJob(() => {
    notify(`Restored ${name} from ${day(s.time)}`);
    onDone();
    onClose();
  }, kind === "computer" ? name : undefined);
  return (
    <Dialog
      title={`Restore ${name} from ${when(s.time)}?`}
      onClose={onClose}
      onSubmit={() => void run.start(() => api.restore(kind, name, { snapshot: s.id, repository: s.repository, mode: "here", backupFirst: first }))}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="danger" disabled={run.running}>
            Replace the files
          </Button>
        </>
      }
    >
      <Note danger>
        {kind === "computer" ? `${name}'s home folder` : hostPath ? `The host folder ${hostPath}` : `The volume ${name}`}
        {" "}goes back to this snapshot. Files added or changed since are lost.
        {stoppers.length > 0 && ` ${stoppers.join(", ")} ${stoppers.length === 1 ? "is" : "are"} stopped meanwhile.`}
      </Note>

      <Check id="restore-first" checked={first} disabled={!hasPolicy} onChange={setFirst} title="Back up the current files first">
        {hasPolicy ? "So this can be undone." : "Needs a backup policy."}
      </Check>
      <JobProgress job={run.job} />
      <ErrorText error={run.error} />
    </Dialog>
  );
}

const NAME = /^[a-z][a-z0-9-]{0,30}[a-z0-9]$/;

/** Restores a snapshot as a new computer or volume. Also used for items found in repositories. */
export function RestoreNewDialog({ kind, name, snapshot: s, onClose }: { kind: ItemKind; name: string; snapshot: { id: string; time: string; repository: string }; onClose: () => void }) {
  const [newName, setNewName] = useState(`${name}-restored`.slice(0, 32).replace(/-+$/, ""));
  const [image, setImage] = useState("");
  const images = useFetch(() => (kind === "computer" ? api.images() : Promise.resolve([])), "images");
  const [result, setResult] = useState<RestoreResult | null>(null);
  const run = useJob((j) => setResult((j.result as RestoreResult) ?? {}));
  const valid = NAME.test(newName);
  if (result) {
    const href = kind === "computer" ? `/environments/${result.created}` : `/volumes/${result.created}`;
    return (
      <Dialog title={`Restored as ${result.created}`} onClose={onClose} onSubmit={() => (location.href = href)} footer={<><Button variant="quiet" onClick={onClose}>Close</Button><Button type="submit" variant="primary">Open {result.created}</Button></>}>
        <p class="text-sm" role="status">
          {kind === "computer" ? `${result.created} is ready, and stopped.` : `${result.created} is ready.`}
        </p>
        {!!result.dropped?.length && (
          <Note>
            Not copied: {result.dropped.join("; ")}.
          </Note>
        )}
      </Dialog>
    );
  }
  return (
    <Dialog
      title={`Restore ${name} as new`}
      intro={`From ${when(s.time)}.`}
      onClose={onClose}
      onSubmit={() => valid && void run.start(() => api.restore(kind, name, { snapshot: s.id, repository: s.repository, mode: "new", newName, newImage: image || undefined }))}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={!valid || run.running}>
            Restore
          </Button>
        </>
      }
    >
      <Field label={kind === "computer" ? "New environment's name" : "New volume's name"} htmlFor="restore-name" hint={newName && !valid ? <span class="text-danger">Use 2–32 lowercase letters, digits and dashes, starting with a letter.</span> : undefined}>
        <input type="text" id="restore-name" value={newName} autocomplete="off" spellcheck={false} onInput={(e) => setNewName(e.currentTarget.value.toLowerCase())} />
      </Field>
      {kind === "computer" && (
        <Field label="Image" htmlFor="restore-image" hint="VPN, Docker access and missing volumes aren't copied.">
          <select id="restore-image" value={image} onChange={(e) => setImage(e.currentTarget.value)}>
            <option value="">The saved image</option>
            {(images.data ?? []).map((i) => (
              <option key={i.ref} value={i.ref}>
                {i.title || i.ref}
              </option>
            ))}
          </select>
        </Field>
      )}
      <JobProgress job={run.job} />
      <ErrorText error={run.error && run.error.includes("exists") ? `${run.error}. Choose another name.` : run.error} />
    </Dialog>
  );
}

