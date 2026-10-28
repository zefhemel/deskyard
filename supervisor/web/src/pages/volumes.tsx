import { useEffect, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { api, ApiError, type BackupItem, type Environment, type Volume, type VolumeUsage, type VPNReference } from "../api.ts";
import { Page, PageHead } from "../components/topbar.tsx";
import { notify } from "../components/toast.tsx";
import { EnvironmentChip } from "../components/environment-chip.tsx";
import { Button, Check, Dialog, ErrorText, Field, Meter, Note, Seg, Tag, errorMessage } from "../components/ui.tsx";
import { bytes } from "../lib/format.ts";
import { useBackups, useFetch } from "../lib/use-fetch.ts";
import { cx } from "../components/ui.tsx";
import { backupStatus } from "../lib/backup-policy.ts";
import { BackupsTab } from "./storage/backups-tab.tsx";
import { RemotesTab } from "./storage/remotes-tab.tsx";
import { useLive } from "../live.tsx";

type StorageTab = "volumes" | "remotes" | "backups";
const TABS: { id: StorageTab; label: string }[] = [
  { id: "volumes", label: "Volumes" },
  { id: "remotes", label: "Remotes" },
  { id: "backups", label: "Backups" },
];

/** The Storage area: volumes, storage remotes and backups. */
export function StoragePage() {
  const { query, route } = useLocation();
  const tab: StorageTab = TABS.find((t) => t.id === query.tab)?.id ?? "volumes";
  return (
    <Page>
      <PageHead title="Storage" tab={TABS.find((t) => t.id === tab)?.label} />
      <nav class="mb-6 flex gap-1 overflow-x-auto border-b border-line [scrollbar-width:none] [&::-webkit-scrollbar]:hidden" role="tablist">
        {TABS.map((t) => (
          <button
            key={t.id}
            type="button"
            role="tab"
            aria-selected={t.id === tab}
            onClick={() => route(`/volumes${t.id === "volumes" ? "" : `?tab=${t.id}`}`, true)}
            class={cx(
              "-mb-px cursor-pointer border-0 border-b-2 bg-transparent px-3.5 py-2.5 text-sm font-semibold",
              t.id === tab ? "border-ink text-ink" : "border-transparent text-muted hover:text-ink",
            )}
          >
            {t.label}
          </button>
        ))}
      </nav>
      {tab === "volumes" && <VolumesTab />}
      {tab === "remotes" && <RemotesTab />}
      {tab === "backups" && <BackupsTab />}
    </Page>
  );
}

const NAME = /^[a-z][a-z0-9-]{0,30}[a-z0-9]$/;

function VolumesTab() {
  const list = useFetch(() => api.volumes(), "volumes");
  const { environments } = useLive();
  const backups = useBackups();
  const [creating, setCreating] = useState(false);
  const [removing, setRemoving] = useState<Volume | null>(null);
  const volumes = list.data?.volumes;
  // A link to one volume (/volumes#volume-<name>) lands on it once loaded.
  const loaded = volumes !== undefined;
  useEffect(() => {
    if (loaded && location.hash) document.getElementById(decodeURIComponent(location.hash.slice(1)))?.scrollIntoView({ block: "start" });
  }, [loaded]);
  return (
    <>
      <div class="mb-4 flex justify-end">
        <Button variant="primary" disabled={!list.data} onClick={() => setCreating(true)}>
          Create volume
        </Button>
      </div>
      <ErrorText error={list.error} />
      {!volumes ? (
        <p class="text-muted">Loading…</p>
      ) : !volumes.length ? (
        <p class="text-muted">No volumes yet.</p>
      ) : (
        <div class="grid gap-3">
          {volumes.map((v) => (
            <VolumeCard key={v.name} volume={v} environments={environments ?? undefined} backup={backups.data?.items.find((i) => i.item === `volume:${v.name}`)} onDelete={() => setRemoving(v)} />
          ))}
        </div>
      )}
      {creating && <CreateVolume onClose={() => setCreating(false)} onCreated={list.reload} />}
      {removing && <DeleteVolume volume={removing} onClose={() => setRemoving(null)} onDeleted={list.reload} />}
    </>
  );
}

/** A volume's backup state as a tag: failing, or when it last ran. */
export function BackupTag({ backup }: { backup: BackupItem | undefined }) {
  const st = backupStatus(backup);
  return st && <Tag kind={st.kind === "update" ? "update" : st.kind}>{st.label}</Tag>;
}

/** A volume's card; `bare` leaves out its name and actions, for its own page. */
export function VolumeCard({ volume: v, environments, backup, onDelete, bare }: { volume: Volume; environments: Environment[] | undefined; backup?: BackupItem; onDelete?: () => void; bare?: boolean }) {
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
      {!bare && (
        <div class="mb-4 flex flex-wrap items-start gap-x-4 gap-y-2">
          <h2 class="mr-auto min-w-0 text-lg font-semibold">
            <a href={`/volumes/${v.name}`}>{v.name}</a> {v.kind === "host" && <Tag>Host folder</Tag>} <BackupTag backup={backup} />
          </h2>
          <Button onClick={onDelete}>Delete</Button>
        </div>
      )}
      <dl class="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[7.5rem_1fr] sm:gap-y-3">
        <dt class="text-muted">Size</dt>
        <dd class="min-w-0" aria-label="Size">
          {!usage ? (
            <span class="text-muted">Measuring…</span>
          ) : (
            <>
              <span class="font-semibold">
                {usage.bytes === 0 && usage.complete ? "Empty" : usage.bytes === 0 ? "Unknown" : `${usage.complete ? "" : "At least "}${bytes(usage.bytes)}`}
              </span>
              {disk && (
                <>
                  <span class="text-muted"> · {bytes(disk.diskFree ?? 0)} free of {bytes(disk.diskSize ?? 0)}</span>
                  <Meter class="mt-1.5 max-w-[360px]" percent={(100 * ((disk.diskSize ?? 0) - (disk.diskFree ?? 0))) / (disk.diskSize ?? 1)} />
                </>
              )}
            </>
          )}
        </dd>
        <dt class="mt-2 text-muted sm:mt-0">{v.kind === "host" ? "Host folder" : "Folder"}</dt>
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
                      <span class="mono text-xs text-muted">{c?.settings.volumes.find((x) => x.name === v.name)?.path ?? ""}</span>
                      <Tag kind={g.readOnly ? "plain" : "ok"}>{g.readOnly ? "Read only" : "Read & write"}</Tag>
                    </EnvironmentChip>
                  </li>
                );
              })}
            </ul>
          ) : (
            <span class="text-muted">None. Grant it in an environment's settings.</span>
          )}
        </dd>
      </dl>
    </article>
  );
}

function CreateVolume({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [name, setName] = useState("");
  const [onHost, setOnHost] = useState(false);
  const [hostPath, setHostPath] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [adopted, setAdopted] = useState<Volume | null>(null);
  const hostPathValid = !onHost || (hostPath.startsWith("/") && !hostPath.endsWith("/") && !hostPath.split("/").some((p) => p === "." || p === ".."));
  const valid = NAME.test(name) && hostPathValid;
  const submit = async () => {
    if (!valid || busy) return;
    setBusy(true);
    setError(null);
    try {
      const created = await api.createVolume(name, onHost ? hostPath : undefined);
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
            <span class="text-danger">Use 2–32 lowercase letters, digits and dashes, starting with a letter.</span>
          ) : (
            "Can't be changed later."
          )
        }
      >
        <input id="volume-name" type="text" autocomplete="off" spellcheck={false} value={name} onInput={(e) => setName(e.currentTarget.value.toLowerCase())} />
      </Field>
      <Field label="Files in">
        <Seg
          label="Files in"
          options={[
            { value: false, label: "Data folder" },
            { value: true, label: "Host folder" },
          ]}
          value={onHost}
          onChange={setOnHost}
        />
      </Field>
      {onHost && (
        <Field
          label="Path"
          htmlFor="volume-host-path"
          hint={
            hostPath && !hostPathValid ? (
              <span class="text-danger">Use an absolute path without a trailing slash, . or ..</span>
            ) : (
              "An existing folder on the engine host. Deskyard never changes or deletes it."
            )
          }
        >
          <input id="volume-host-path" class="mono" type="text" autocomplete="off" spellcheck={false} placeholder="/srv/photos" value={hostPath} onInput={(e) => setHostPath(e.currentTarget.value)} />
        </Field>
      )}
      <ErrorText error={error} />
    </Dialog>
  );
}

export function DeleteVolume({ volume: v, onClose, onDeleted }: { volume: Volume; onClose: () => void; onDeleted: () => void }) {
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
        v.kind === "host" ? (
          <p class="mb-4 text-sm">
            The volume is forgotten. Its host folder <span class="mono break-all">{v.folder}</span> is never touched.
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
        )
      )}
      {files && !refs.length && <Note danger>The files are deleted for good.</Note>}
      <ErrorText error={error} />
    </Dialog>
  );
}
