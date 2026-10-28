// Restoring a snapshot over the item itself, or as a new environment or volume.
import { useState } from "preact/hooks";
import { api, type ItemKind, type RestoreResult, type Snapshot } from "../../api.ts";
import { itemCopy } from "../../lib/backup-policy.ts";
import { dateTime, day } from "../../lib/format.ts";
import { RESOURCE_NAME, resourceNameHint } from "../../lib/names.ts";
import { useFetch } from "../../lib/use-fetch.ts";
import { JobProgress, useJob } from "../job.tsx";
import { notify } from "../toast.tsx";
import { Button, Check, Dialog, ErrorText, Field, Note } from "../ui.tsx";

export function RestoreHereDialog({
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
  const copy = itemCopy(kind, name);
  const [first, setFirst] = useState(hasPolicy);
  const run = useJob(() => {
    notify(`Restored ${name} from ${day(s.time)}`);
    onDone();
    onClose();
  }, copy.jobEnvironment);
  return (
    <Dialog
      title={`Restore ${name} from ${dateTime(s.time)}?`}
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
        {copy.replaced(hostPath)}
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

/** Restores a snapshot as a new environment or volume. Also used for items found in repositories. */
export function RestoreNewDialog({ kind, name, snapshot: s, onClose }: { kind: ItemKind; name: string; snapshot: { id: string; time: string; repository: string }; onClose: () => void }) {
  const copy = itemCopy(kind, name);
  const [newName, setNewName] = useState(`${name}-restored`.slice(0, 32).replace(/-+$/, ""));
  const [image, setImage] = useState("");
  const images = useFetch(() => (kind === "environment" ? api.images() : Promise.resolve([])), "images");
  const [result, setResult] = useState<RestoreResult | null>(null);
  const run = useJob<RestoreResult>((j) => setResult(j.result ?? {}));
  const valid = RESOURCE_NAME.test(newName);
  if (result) {
    const created = result.created ?? "";
    const href = copy.pageOf(created);
    return (
      <Dialog title={`Restored as ${created}`} onClose={onClose} onSubmit={() => (location.href = href)} footer={<><Button variant="quiet" onClick={onClose}>Close</Button><Button type="submit" variant="primary">Open {created}</Button></>}>
        <p class="text-sm" role="status">
          {copy.ready(created)}
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
      intro={`From ${dateTime(s.time)}.`}
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
      <Field label={copy.newNameLabel} htmlFor="restore-name" hint={newName && !valid ? <span class="text-danger">{resourceNameHint}</span> : undefined}>
        <input type="text" id="restore-name" value={newName} autocomplete="off" spellcheck={false} onInput={(e) => setNewName(e.currentTarget.value.toLowerCase())} />
      </Field>
      {kind === "environment" && (
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
