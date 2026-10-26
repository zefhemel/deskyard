import { useEffect, useState } from "preact/hooks";
import { api, ApiError, type Environment, type Volume, type VolumeUsage, type VPNReference } from "../api.ts";
import { Page, PageHead } from "../components/topbar.tsx";
import { notify } from "../components/toast.tsx";
import { EnvironmentChip } from "../components/environment-chip.tsx";
import { Button, Check, Dialog, ErrorText, Field, Meter, Note, Tag, errorMessage } from "../components/ui.tsx";
import { bytes } from "../lib/format.ts";
import { useFetch } from "../lib/use-fetch.ts";

const NAME = /^[a-z][a-z0-9-]{0,30}[a-z0-9]$/;

export function VolumesPage() {
  const list = useFetch(() => api.volumes(), "volumes");
  const envs = useFetch(() => api.environments(), "environments");
  const [creating, setCreating] = useState(false);
  const [removing, setRemoving] = useState<Volume | null>(null);
  const volumes = list.data?.volumes;
  // A link to one volume (/volumes#volume-<name>) lands on it once loaded.
  const loaded = volumes !== undefined;
  useEffect(() => {
    if (loaded && location.hash) document.getElementById(decodeURIComponent(location.hash.slice(1)))?.scrollIntoView({ block: "start" });
  }, [loaded]);
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
            <VolumeCard key={v.name} volume={v} environments={envs.data?.environments} onDelete={() => setRemoving(v)} />
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

function VolumeCard({ volume: v, environments, onDelete }: { volume: Volume; environments: Environment[] | undefined; onDelete: () => void }) {
  // Sizing walks the whole folder, so each card asks for its own.
  const [usage, setUsage] = useState<VolumeUsage | null>(null);
  useEffect(() => {
    let live = true;
    api.volumeUsage(v.name).then((u) => live && setUsage(u), () => {});
    return () => {
      live = false;
    };
  }, [v.name]);
  const disk = usage?.diskSize ? usage : null;
  return (
    <article id={`volume-${v.name}`} class="scroll-mt-20 rounded-[10px] border border-line bg-surface p-5 target:outline-3 target:outline-offset-2 target:outline-accent">
      <div class="flex flex-wrap items-start gap-x-4 gap-y-2">
        <div class="mr-auto min-w-0">
          <h2 class="text-lg font-semibold">{v.name}</h2>
          <p class="mono text-sm text-muted">/share/{v.name}</p>
        </div>
        <Button onClick={onDelete}>Delete</Button>
      </div>
      <dl class="mt-4 grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[7.5rem_1fr] sm:gap-y-3">
        <dt class="text-muted">Size</dt>
        <dd class="min-w-0" aria-label="Size">
          {!usage ? (
            <span class="text-muted">Measuring…</span>
          ) : (
            <>
              <span class="font-semibold">
                {usage.bytes === 0 && usage.complete ? "Empty" : `${usage.complete ? "" : "At least "}${bytes(usage.bytes)}`}
              </span>
              {disk && (
                <>
                  <span class="text-muted"> · {bytes(disk.diskFree ?? 0)} free of {bytes(disk.diskSize ?? 0)} on its disk</span>
                  <Meter class="mt-1.5 max-w-[360px]" percent={(100 * ((disk.diskSize ?? 0) - (disk.diskFree ?? 0))) / (disk.diskSize ?? 1)} />
                </>
              )}
            </>
          )}
        </dd>
        <dt class="mt-2 text-muted sm:mt-0">Folder</dt>
        <dd class="mono min-w-0 break-all">{v.folder}</dd>
        <dt class="mt-2 text-muted sm:mt-0">Environments</dt>
        <dd class="min-w-0">
          {v.environments.length ? (
            <ul class="m-0 flex list-none flex-wrap gap-2 p-0" aria-label={`Environments using ${v.name}`}>
              {v.environments.map((g) => {
                const c = environments?.find((e) => e.name === g.name);
                return (
                  <li key={g.name}>
                    <EnvironmentChip name={g.name} environment={c} href={`/environments/${g.name}?tab=settings&section=volumes`}>
                      <Tag kind={g.readOnly ? "plain" : "ok"}>{g.readOnly ? "Read only" : "Read & write"}</Tag>
                    </EnvironmentChip>
                  </li>
                );
              })}
            </ul>
          ) : (
            <span class="text-muted">No environment yet. Grant it in an environment's settings.</span>
          )}
        </dd>
      </dl>
    </article>
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
