import { useEffect, useState } from "preact/hooks";
import { api, type Desktop, type Host } from "../../api.ts";
import { cpuOptions, ImagePick, memoryOptions } from "../../components/desktop-fields.tsx";
import { JobProgress, useJob } from "../../components/job.tsx";
import { notify } from "../../components/toast.tsx";
import { SecretImageNote } from "../../components/secret-access.tsx";
import { Button, Check, Dialog, ErrorText, Field, Note, Seg } from "../../components/ui.tsx";
import { useLive } from "../../live.tsx";
import { useFetch } from "../../lib/use-fetch.ts";
import { UPGRADE_NOTE } from "../../dialogs/upgrade.tsx";

export function SettingsTab({ desktop: c }: { desktop: Desktop }) {
  const images = useFetch(() => api.images(), "images");
  const [image, setImage] = useState(c.image);
  const [memory, setMemory] = useState(c.settings.memoryBytes);
  const [cpus, setCpus] = useState(c.settings.nanoCpus);
  const [camera, setCamera] = useState(c.settings.camera);
  const { host } = useLive();
  const run = useJob(() => notify(`${c.name}'s settings are saved`));

  const [confirming, setConfirming] = useState(false);
  const changed = image !== c.image || memory !== c.settings.memoryBytes || cpus !== c.settings.nanoCpus || camera !== c.settings.camera;
  // Moving a desktop others use onto an image with secrets: ask first, as giving access does.
  const picked = (images.data ?? []).find((i) => i.ref === image);
  const askFirst = image !== c.image && !!picked?.secrets && c.access.length > 0;
  const save = () => {
    setConfirming(false);
    const body: { image?: string; memoryBytes?: number; nanoCpus?: number; camera?: string; confirmSecrets?: boolean } = {};
    if (camera !== c.settings.camera) body.camera = camera;
    if (image !== c.image) body.image = image;
    if (askFirst) body.confirmSecrets = true;
    if (memory !== c.settings.memoryBytes) body.memoryBytes = memory;
    if (cpus !== c.settings.nanoCpus) body.nanoCpus = cpus;
    void run.start(() => api.updateSettings(c.name, body));
  };
  // The current image may have left the catalog; keep it pickable.
  const catalog = images.data ?? [];
  const list = catalog.some((i) => i.ref === c.image)
    ? catalog
    : [{ ref: c.image, title: c.imageTitle, version: c.imageVersion, description: "Not in the catalog any more.", installed: true, official: false, desktops: [], behind: [], checked: null, custom: false, secrets: false, baseUpdated: false, recipe: null }, ...catalog];

  return (
    <>
      <form
        class="max-w-[620px] px-5 py-5"
        onSubmit={(e) => {
          e.preventDefault();
          if (askFirst) setConfirming(true);
          else save();
        }}
      >
        <fieldset disabled={run.running} class="m-0 min-w-0 border-0 p-0">
          <Field label="Image">{images.data ? <ImagePick images={list} value={image} onChange={setImage} /> : <ErrorText error={images.error} />}</Field>
          <Field label="Memory">
            <Seg label="Memory" options={memoryOptions(memory)} value={memory} onChange={setMemory} />
          </Field>
          <Field label="CPUs">
            <Seg label="CPUs" options={cpuOptions(cpus)} value={cpus} onChange={setCpus} />
          </Field>
          <Field label="Camera">
            <CameraPick desktop={c} host={host} value={camera} onChange={setCamera} />
          </Field>
        </fieldset>
        {changed && (
          <Note>
            Saving recreates {c.name}'s container{c.state === "running" ? ", which restarts it" : ""}. {image !== c.image && UPGRADE_NOTE}
          </Note>
        )}
        <JobProgress job={run.job} />
        <ErrorText error={run.error} />
        <div class="flex gap-2.5">
          <Button type="submit" variant="primary" disabled={!changed || run.running}>
            {run.running ? "Saving…" : c.state === "running" ? "Save and restart" : "Save"}
          </Button>
          {changed && !run.running && (
            <Button
              variant="quiet"
              onClick={() => {
                setImage(c.image);
                setMemory(c.settings.memoryBytes);
                setCpus(c.settings.nanoCpus);
                setCamera(c.settings.camera);
              }}
            >
              Undo changes
            </Button>
          )}
        </div>
      </form>
      <MemoryPolicy desktop={c} host={host} />
      {confirming && picked && (
        <Dialog
          title={`Move ${c.name} onto ${picked.title || picked.ref}?`}
          onClose={() => setConfirming(false)}
          onSubmit={save}
          footer={
            <>
              <Button variant="quiet" onClick={() => setConfirming(false)}>
                Cancel
              </Button>
              <Button type="submit" variant="danger">
                Move {c.name}
              </Button>
            </>
          }
        >
          <SecretImageNote image={picked.title || picked.ref} who={c.access} />
        </Dialog>
      )}
    </>
  );
}

/**
 * The host's virtual camera this desktop has to itself, which its viewers'
 * webcams feed. One desktop per camera: another desktop's can't be picked.
 */
function CameraPick({ desktop: c, host, value, onChange }: { desktop: Desktop; host: Host | null; value: string; onChange: (v: string) => void }) {
  const cameras = host?.cameras;
  if (cameras === null) {
    return <p class="m-0 text-sm text-muted">Cameras aren't available here: the container engine isn't on this machine.</p>;
  }
  if (!cameras?.length && !value) {
    return <p class="m-0 text-sm text-muted">This machine has no virtual camera for desktops (v4l2loopback).</p>;
  }
  const list = cameras ?? [];
  return (
    <select aria-label="Camera" value={value} onChange={(e) => onChange(e.currentTarget.value)}>
      <option value="">None</option>
      {list.map((cam) => {
        const other = cam.taken && cam.device !== c.settings.camera;
        return (
          <option key={cam.device} value={cam.device} disabled={other}>
            {cam.device}
            {other ? ` (${cam.desktop ?? "another desktop"} has it)` : ""}
          </option>
        );
      })}
      {value && !list.some((cam) => cam.device === value) && <option value={value}>{value} (gone from this machine)</option>}
    </select>
  );
}

/** This preference saves independently of container settings. */
function MemoryPolicy({ desktop: c, host }: { desktop: Desktop; host: Host | null }) {
  const [enabled, setEnabled] = useState(c.autoReclaim);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => setEnabled(c.autoReclaim), [c.name, c.autoReclaim]);
  const save = async (value: boolean) => {
    setSaving(true);
    setEnabled(value);
    setError(null);
    try {
      await api.setMemoryPolicy(c.name, value);
      notify("Automatic memory reclamation preference saved");
    } catch (err) {
      setEnabled(!value);
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };
  return (
    <section class="max-w-[620px] px-5 pb-5">
      <fieldset disabled={saving} class="m-0 min-w-0 border-0 p-0">
        <Check id="auto-reclaim" checked={enabled} onChange={(v) => void save(v)} title="Automatically reclaim idle memory">
          Reclaim once after 15 minutes with no viewers, when agents and desktop operations are idle. A returning viewer resets the timer. Apps keep running; their next interaction may be slower. This preference saves immediately without restarting the desktop.
        </Check>
      </fieldset>
      {host && !host.memoryReclaim.enabled && <Note>Unavailable on this host: {host.memoryReclaim.reason}</Note>}
      <ErrorText error={error} />
    </section>
  );
}
