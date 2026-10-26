import { VPNFields, VPNStatus } from "../../components/vpn.tsx";
import { desktopSettingsChanged, saveDesktopSettings, exceptionRows, type ExceptionRow } from "../../lib/vpn-form.ts";
import { useEffect, useRef, useState } from "preact/hooks";
import { api, type Desktop, type Host } from "../../api.ts";
import { DockerAccessField, cpuOptions, ImagePick, memoryOptions } from "../../components/desktop-fields.tsx";
import { JobProgress, useJob } from "../../components/job.tsx";
import { notify } from "../../components/toast.tsx";
import { Button, Check, ErrorText, Field, Note, Seg } from "../../components/ui.tsx";
import { useLive } from "../../live.tsx";
import { useFetch } from "../../lib/use-fetch.ts";
import { HostMountFields } from "../../components/host-mounts.tsx";
import { UPGRADE_NOTE } from "../../dialogs/upgrade.tsx";

export function SettingsTab({ desktop: c }: { desktop: Desktop }) {
  const vpn = useFetch(() => api.vpnConnections(), "vpn-connections");
  const [vpnID, setVPNID] = useState(c.settings.vpnConnectionId ?? "");
  const [lan, setLAN] = useState<ExceptionRow[]>(exceptionRows(c.settings.lanExceptions ?? []));
  const [vpnError, setVPNError] = useState<string | null>(null);

  const images = useFetch(() => api.images(), "images");
  const [image, setImage] = useState(c.image);
  const [memory, setMemory] = useState(c.settings.memoryBytes);
  const [cpus, setCpus] = useState(c.settings.nanoCpus);
  const [camera, setCamera] = useState(c.settings.camera);
  const [dockerAccess, setDockerAccess] = useState(c.settings.dockerAccess ?? false);
  const savedMounts = c.settings.mounts ?? [];
  const [mounts, setMounts] = useState(savedMounts);
  const { host } = useLive();
  const run = useJob(() => {
    notify(`${c.name}'s settings are saved`);
  }, c.name);
  const job = run.job;
  const saving = run.running;
  const progressPanel = useRef<HTMLElement>(null);
  useEffect(() => {
    if (saving) progressPanel.current?.scrollIntoView({ block: "nearest" });
  }, [saving]);

  const draft = { image, dockerAccess, memoryBytes: memory, nanoCpus: cpus, camera, vpnConnectionId: vpnID, lan, mounts };
  const changed = desktopSettingsChanged(c, draft);
  const save = () => {
    try {
      setVPNError(null);
      void saveDesktopSettings(c, draft, (body) => run.start(() => api.updateSettings(c.name, body)));
    } catch (e) {
      setVPNError(e instanceof Error ? e.message : String(e));
    }
  };
  // The current image may have left the catalog; keep it pickable.
  const catalog = images.data ?? [];
  const list = catalog.some((i) => i.ref === c.image)
    ? catalog
    : [{ ref: c.image, title: c.imageTitle, version: c.imageVersion, description: "Not in the catalog any more.", installed: true, official: false, desktops: [], behind: [], checked: null }, ...catalog];

  return (
    <>
      <form
        class="max-w-[620px] px-5 py-5"
        onSubmit={(e) => {
          e.preventDefault();
          save();
        }}
      >
        {saving && (
          <section ref={progressPanel} aria-label="Desktop operation" aria-live="polite">
            <h2 class="mb-2 text-sm font-semibold">Desktop operation</h2>
            <JobProgress job={job} />
          </section>
        )}
        <fieldset disabled={saving} class="m-0 min-w-0 border-0 p-0">
          <Field label="Image">{images.data ? <ImagePick images={list} value={image} onChange={setImage} /> : <ErrorText error={images.error} />}</Field>
          <Field label="Memory">
            <Seg label="Memory" options={memoryOptions(memory)} value={memory} onChange={setMemory} />
          </Field>
          <Field label="CPUs">
            <Seg label="CPUs" options={cpuOptions(cpus)} value={cpus} onChange={setCpus} />
          </Field>
          <div class="mb-4">
            <VPNStatus vpn={c.vpn} />
          </div>
          {vpn.data ? (
            <VPNFields connections={vpn.data.connections} value={vpnID} onChange={setVPNID} rows={lan} onRows={setLAN} />
          ) : (
            <ErrorText error={vpn.error} />
          )}
          <Field label="Camera">
            <CameraPick desktop={c} host={host} value={camera} onChange={setCamera} />
          </Field>
          <DockerAccessField value={dockerAccess} onChange={setDockerAccess} />
          <HostMountFields mounts={mounts} onChange={setMounts} />
        </fieldset>
        {changed && (
          <Note>
            Saving recreates {c.name}'s container{c.state === "running" ? ", which restarts it and briefly disconnects open sessions; files are kept" : ""}.{" "}
            {image !== c.image && UPGRADE_NOTE}
          </Note>
        )}
        <ErrorText error={vpnError || run.error} />
        <div class="flex gap-2.5">
          <Button type="submit" variant="primary" disabled={!changed || saving}>
            {saving ? "Saving…" : c.state === "running" ? "Save and restart" : "Save"}
          </Button>
          {changed && !saving && (
            <Button
              variant="quiet"
              onClick={() => {
                setImage(c.image);
                setMemory(c.settings.memoryBytes);
                setCpus(c.settings.nanoCpus);
                setCamera(c.settings.camera);
                setDockerAccess(c.settings.dockerAccess ?? false);
                setVPNID(c.settings.vpnConnectionId ?? "");
                setLAN(exceptionRows(c.settings.lanExceptions ?? []));
                setVPNError(null);
                setMounts(savedMounts);
              }}
            >
              Undo changes
            </Button>
          )}
        </div>
      </form>
      <MemoryPolicy desktop={c} host={host} />
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
          Reclaim once after 15 minutes with no viewers, when CLI activity and desktop operations are idle. A returning viewer resets the timer. Apps keep running; their next interaction may be slower. This preference saves immediately without restarting the desktop.
        </Check>
      </fieldset>
      {host && !host.memoryReclaim.enabled && <Note>Unavailable on this host: {host.memoryReclaim.reason}</Note>}
      <ErrorText error={error} />
    </section>
  );
}
