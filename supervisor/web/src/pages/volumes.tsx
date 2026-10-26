import { useState } from "preact/hooks";
import { api, ApiError, type Volume, type VPNReference } from "../api.ts";
import { Page, PageHead } from "../components/topbar.tsx";
import { notify } from "../components/toast.tsx";
import { Button, Check, Dialog, ErrorText, Field, Note, errorMessage } from "../components/ui.tsx";
import { useFetch } from "../lib/use-fetch.ts";

const NAME = /^[a-z][a-z0-9-]{0,30}[a-z0-9]$/;

export function VolumesPage() {
  const list = useFetch(() => api.volumes(), "volumes");
  const [creating, setCreating] = useState(false);
  const [removing, setRemoving] = useState<Volume | null>(null);
  const volumes = list.data?.volumes;
  return (
    <Page>
      <PageHead title="Volumes" sub="Folders shared between environments, at /share/<volume> in each.">
        <Button variant="primary" disabled={!list.data} onClick={() => setCreating(true)}>
          Create volume
        </Button>
      </PageHead>
      <ErrorText error={list.error} />
      {!volumes ? (
        <p class="text-muted">Loading…</p>
      ) : !volumes.length ? (
        <p class="text-muted">No volumes yet. Create one, then grant it to environments in their settings.</p>
      ) : (
        <div class="grid gap-3">
          {volumes.map((v) => (
            <article key={v.name} class="flex flex-wrap items-center gap-4 rounded-[10px] border border-line bg-surface p-5">
              <div class="mr-auto min-w-0">
                <h2 class="text-lg font-semibold">{v.name}</h2>
                <p class="mono text-sm break-all text-muted">{v.folder}</p>
                <p class="mt-1 text-sm">
                  {v.environments.length ? (
                    <>
                      Used by{" "}
                      {v.environments.map((g, i) => (
                        <span key={g.name}>
                          {i > 0 ? ", " : ""}
                          <a href={`/environments/${g.name}?tab=settings`}>{g.name}</a> ({g.readOnly ? "read only" : "read and write"})
                        </span>
                      ))}
                    </>
                  ) : (
                    "No environments use this volume."
                  )}
                </p>
              </div>
              <Button onClick={() => setRemoving(v)}>Delete</Button>
            </article>
          ))}
        </div>
      )}
      <p class="mt-5 max-w-[760px] text-sm text-muted">
        Grant a volume to an environment in its settings, read only or read and write. Removing an environment never touches its volumes.
      </p>
      {creating && <CreateVolume onClose={() => setCreating(false)} onCreated={list.reload} />}
      {removing && <DeleteVolume volume={removing} onClose={() => setRemoving(null)} onDeleted={list.reload} />}
    </Page>
  );
}

function CreateVolume({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [adopted, setAdopted] = useState<Volume | null>(null);
  const valid = NAME.test(name);
  const submit = async () => {
    if (!valid || busy) return;
    setBusy(true);
    setError(null);
    try {
      const created = await api.createVolume(name);
      onCreated();
      if (created.adopted) {
        // Say which folder it took over before closing.
        setAdopted(created.volume);
      } else {
        notify(`Created volume ${created.volume.name}`);
        onClose();
      }
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  if (adopted) {
    return (
      <Dialog
        title={`Created ${adopted.name}`}
        onClose={onClose}
        onSubmit={onClose}
        footer={
          <Button type="submit" variant="primary">
            Done
          </Button>
        }
      >
        <p class="text-sm" role="status">
          Using the existing folder <span class="mono break-all">{adopted.folder}</span>. Its files are kept as they are.
        </p>
      </Dialog>
    );
  }
  return (
    <Dialog
      title="Create volume"
      onClose={onClose}
      onSubmit={() => void submit()}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={!valid || busy}>
            {busy ? "Creating…" : "Create volume"}
          </Button>
        </>
      }
    >
      <Field
        label="Name"
        htmlFor="volume-name"
        hint={
          name && !valid ? (
            <span class="text-danger">Use 2–32 lowercase letters, digits and dashes, starting with a letter and not ending with a dash.</span>
          ) : (
            <>
              Lowercase letters, digits and dashes; it can't be changed later.{valid && <> Environments see it at <span class="mono">/share/{name}</span>.</>}
            </>
          )
        }
      >
        <input id="volume-name" type="text" autocomplete="off" spellcheck={false} value={name} onInput={(e) => setName(e.currentTarget.value.toLowerCase())} />
      </Field>
      <ErrorText error={error} />
    </Dialog>
  );
}

function DeleteVolume({ volume: v, onClose, onDeleted }: { volume: Volume; onClose: () => void; onDeleted: () => void }) {
  const [files, setFiles] = useState(false);
  const [refs, setRefs] = useState<VPNReference[]>(v.environments.map((g) => ({ id: g.name, name: g.name })));
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const remove = async () => {
    setBusy(true);
    setError(null);
    try {
      await api.deleteVolume(v.name, files);
      notify(`Deleted volume ${v.name}`);
      onDeleted();
      onClose();
    } catch (e) {
      setError(errorMessage(e));
      if (e instanceof ApiError && e.status === 409) setRefs(e.environments);
      onDeleted();
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      title={`Delete ${v.name}?`}
      onClose={onClose}
      onSubmit={() => void remove()}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Close
          </Button>
          <Button type="submit" variant="danger" disabled={busy || refs.length > 0}>
            {files ? "Delete volume and files" : "Delete volume"}
          </Button>
        </>
      }
    >
      {refs.length ? (
        <p class="mb-4 text-sm">
          Remove the volume from these environments' settings first:{" "}
          {refs.map((d, i) => (
            <span key={d.id}>
              {i ? ", " : ""}
              <a href={`/environments/${d.name}?tab=settings`}>{d.name}</a>
            </span>
          ))}
          .
        </p>
      ) : (
        <>
          <p class="mb-4 text-sm">
            {files ? "The volume and its files in " : "The volume is deleted. Its files stay in "}
            <span class="mono break-all">{v.folder}</span>
            {files ? " are deleted." : ", and creating a volume with this name again uses them."}
          </p>
          <Check id="volume-delete-files" checked={files} onChange={setFiles} title={<>Delete the files in <span class="mono break-all">{v.folder}</span></>}>
            Everything stored in the volume. This can't be undone.
          </Check>
        </>
      )}
      {files && !refs.length && <Note danger>The files are deleted for good.</Note>}
      <ErrorText error={error} />
    </Dialog>
  );
}
