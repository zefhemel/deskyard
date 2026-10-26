import { VPNFields, VPNStatus } from "../../components/vpn.tsx";
import { environmentSettingsChanged, saveEnvironmentSettings, exceptionRows, type ExceptionRow } from "../../lib/vpn-form.ts";
import { useEffect, useRef, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { api, type Environment, type Host } from "../../api.ts";
import { DockerAccessField, cpuOptions, ImagePick, memoryOptions } from "../../components/environment-fields.tsx";
import { JobProgress, useJob } from "../../components/job.tsx";
import { notify } from "../../components/toast.tsx";
import { Button, Check, cx, ErrorText, Field, Note, Seg } from "../../components/ui.tsx";
import { useLive } from "../../live.tsx";
import { useFetch } from "../../lib/use-fetch.ts";
import { HostMountFields } from "../../components/host-mounts.tsx";
import { VolumeGrantFields } from "../../components/volume-grants.tsx";
import { UPGRADE_NOTE } from "../../dialogs/upgrade.tsx";

export function SettingsTab({ environment: c }: { environment: Environment }) {
  const vpn = useFetch(() => api.vpnConnections(), "vpn-connections");
  const [vpnID, setVPNID] = useState(c.settings.vpnConnectionId ?? "");
  const [lan, setLAN] = useState<ExceptionRow[]>(exceptionRows(c.settings.lanExceptions ?? []));
  const [vpnError, setVPNError] = useState<string | null>(null);

  const images = useFetch(() => api.images(), "images");
  const [image, setImage] = useState(c.image);
  const [memory, setMemory] = useState(c.settings.memoryBytes);
  const [cpus, setCpus] = useState(c.settings.nanoCpus);
  const [dockerAccess, setDockerAccess] = useState(c.settings.dockerAccess ?? false);
  const savedMounts = c.settings.mounts ?? [];
  const [mounts, setMounts] = useState(savedMounts);
  const volumes = useFetch(() => api.volumes(), "volumes");
  const savedVolumes = c.settings.volumes ?? [];
  const [grants, setGrants] = useState(savedVolumes);
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

  const draft = { image, dockerAccess, memoryBytes: memory, nanoCpus: cpus, vpnConnectionId: vpnID, lan, mounts, volumes: grants };
  const { query, route } = useLocation();
  const section: Section = SECTIONS.find((x) => x.id === query.section)?.id ?? "general";
  const pick = (id: Section) => route(`/environments/${c.name}?tab=settings${id === "general" ? "" : `&section=${id}`}`, true);
  const edited: Record<Section, boolean> = {
    general: image !== c.image || memory !== c.settings.memoryBytes || cpus !== c.settings.nanoCpus || dockerAccess !== (c.settings.dockerAccess ?? false),
    networking: environmentSettingsChanged(c, { ...draft, image: c.image, memoryBytes: c.settings.memoryBytes, nanoCpus: c.settings.nanoCpus, dockerAccess: c.settings.dockerAccess ?? false, mounts: savedMounts, volumes: savedVolumes }),
    volumes: JSON.stringify(mounts) !== JSON.stringify(savedMounts) || JSON.stringify(grants) !== JSON.stringify(savedVolumes),
  };
  const changed = environmentSettingsChanged(c, draft);
  const save = () => {
    try {
      setVPNError(null);
      void saveEnvironmentSettings(c, draft, (body) => run.start(() => api.updateSettings(c.name, body)));
    } catch (e) {
      setVPNError(e instanceof Error ? e.message : String(e));
    }
  };
  // The current image may have left the catalog; keep it pickable.
  const catalog = images.data ?? [];
  const list = catalog.some((i) => i.ref === c.image)
    ? catalog
    : [{ ref: c.image, title: c.imageTitle, version: c.imageVersion, description: "Not in the catalog any more.", installed: true, official: false, environments: [], behind: [], checked: null }, ...catalog];

  return (
    <div class="flex flex-col gap-4 px-5 py-5 md:flex-row md:gap-8">
      <nav aria-label="Settings sections" class="md:w-44 md:flex-none">
        <ul class="m-0 flex list-none gap-1 overflow-x-auto p-0 md:flex-col">
          {SECTIONS.map((x) => (
            <li key={x.id}>
              <button
                type="button"
                aria-current={x.id === section ? "page" : undefined}
                onClick={() => pick(x.id)}
                class={cx(
                  "flex w-full items-center gap-2 rounded-md px-3 py-1.5 text-left text-sm whitespace-nowrap",
                  x.id === section ? "bg-hairline font-semibold text-ink" : "text-muted hover:bg-hairline/60 hover:text-ink",
                )}
              >
                {x.label}
                {edited[x.id] && <span class="h-1.5 w-1.5 rounded-full bg-accent" title="Unsaved changes" />}
              </button>
            </li>
          ))}
        </ul>
      </nav>
      <div class="min-w-0 max-w-[620px] flex-1">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            save();
          }}
        >
          {saving && (
            <section ref={progressPanel} aria-label="Environment operation" aria-live="polite">
              <h2 class="mb-2 text-sm font-semibold">Environment operation</h2>
              <JobProgress job={job} />
            </section>
          )}
          <fieldset disabled={saving} class="m-0 min-w-0 border-0 p-0">
            {section === "general" && (
              <>
                <Field label="Image">{images.data ? <ImagePick images={list} value={image} onChange={setImage} /> : <ErrorText error={images.error} />}</Field>
                <Field label="Memory">
                  <Seg label="Memory" options={memoryOptions(memory)} value={memory} onChange={setMemory} />
                </Field>
                <Field label="CPUs">
                  <Seg label="CPUs" options={cpuOptions(cpus)} value={cpus} onChange={setCpus} />
                </Field>
                <DockerAccessField value={dockerAccess} onChange={setDockerAccess} />
              </>
            )}
            {section === "networking" && (
              <>
                <div class="mb-4">
                  <VPNStatus vpn={c.vpn} />
                </div>
                {vpn.data ? (
                  <VPNFields connections={vpn.data.connections} value={vpnID} onChange={setVPNID} rows={lan} onRows={setLAN} />
                ) : (
                  <ErrorText error={vpn.error} />
                )}
              </>
            )}
            {section === "volumes" && (
              <>
                {volumes.data ? <VolumeGrantFields grants={grants} volumes={volumes.data.volumes} onChange={setGrants} /> : <ErrorText error={volumes.error} />}
                <HostMountFields mounts={mounts} onChange={setMounts} />
              </>
            )}
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
                  setDockerAccess(c.settings.dockerAccess ?? false);
                  setVPNID(c.settings.vpnConnectionId ?? "");
                  setLAN(exceptionRows(c.settings.lanExceptions ?? []));
                  setVPNError(null);
                  setMounts(savedMounts);
                  setGrants(savedVolumes);
                }}
              >
                Undo changes
              </Button>
            )}
          </div>
        </form>
        {section === "general" && <MemoryPolicy environment={c} host={host} />}
      </div>
    </div>
  );
}

type Section = "general" | "networking" | "volumes";
const SECTIONS: { id: Section; label: string }[] = [
  { id: "general", label: "General" },
  { id: "networking", label: "Networking" },
  { id: "volumes", label: "Volumes" },
];

/** This preference saves independently of container settings. */
function MemoryPolicy({ environment: c, host }: { environment: Environment; host: Host | null }) {
  const [enabled, setEnabled] = useState(c.memoryReclaim);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => setEnabled(c.memoryReclaim), [c.name, c.memoryReclaim]);
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
    <section class="mt-6 border-t border-line pt-5">
      <fieldset disabled={saving} class="m-0 min-w-0 border-0 p-0">
        <Check id="auto-reclaim" checked={enabled} onChange={(v) => void save(v)} title="Automatically reclaim idle memory">
          Reclaim once after 15 minutes with no viewers, when CLI activity and environment operations are idle. A returning viewer resets the timer. Apps keep running; their next interaction may be slower. This preference saves immediately without restarting the environment.
        </Check>
      </fieldset>
      {host && !host.memoryReclaim.enabled && <Note>Unavailable on this host: {host.memoryReclaim.reason}</Note>}
      <ErrorText error={error} />
    </section>
  );
}
