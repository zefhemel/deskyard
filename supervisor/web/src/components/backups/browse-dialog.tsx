// A snapshot's files: browse, download, or restore some into a new folder.
import { useEffect, useState } from "preact/hooks";
import { api, type ItemKind, type RestoreResult, type Snapshot, type SnapshotEntry } from "../../api.ts";
import { itemCopy } from "../../lib/backup-policy.ts";
import { bytes, dateTime } from "../../lib/format.ts";
import { JobProgress, useJob } from "../job.tsx";
import { Button, Dialog, ErrorText, errorMessage } from "../ui.tsx";

export function BrowseDialog({ kind, name, snapshot: s, onClose }: { kind: ItemKind; name: string; snapshot: Snapshot; onClose: () => void }) {
  const copy = itemCopy(kind, name);
  const [dir, setDir] = useState("");
  const [entries, setEntries] = useState<SnapshotEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [picked, setPicked] = useState<string[]>([]);
  const [done, setDone] = useState<RestoreResult | null>(null);
  const run = useJob<RestoreResult>((j) => setDone(j.result ?? {}));
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
          In <span class="mono break-all">{done.folder}</span> {copy.restoredInto}.
        </p>
      </Dialog>
    );
  }
  return (
    <Dialog
      title={`${name} on ${dateTime(s.time)}`}
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
          {copy.root}
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
