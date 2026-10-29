import type { ComponentChildren } from "preact";
import { VPNFields } from "../components/vpn.tsx";
import { parseExceptions, type ExceptionRow } from "../lib/vpn-form.ts";
import { useEffect, useState } from "preact/hooks";
import { api, type AccessGrant, type NameCheck, type VolumeGrant } from "../api.ts";
import { AccessPicks, DockerAccessField, cpuOptions, ImagePick, memoryOptions } from "../components/computer-fields.tsx";
import { JobProgress, useJob } from "../components/job.tsx";
import { VolumeGrantFields } from "../components/volume-grants.tsx";
import { Button, Dialog, ErrorText, Field, Seg } from "../components/ui.tsx";
import { useLive } from "../live.tsx";
import { useFetch } from "../lib/use-fetch.ts";

const NAME = /^[a-z][a-z0-9-]{0,30}[a-z0-9]$/;

/** Where the computer's files will be: from the server when it says, else next to the other computers' folders. */
function folderFor(name: string, check: NameCheck | null, folders: string[]): string | null {
  if (check?.folder) return check.folder;
  const first = folders[0];
  if (!first) return null;
  return `${first.slice(0, first.lastIndexOf("/"))}/${name}`;
}

export function NewComputerDialog({ onClose }: { onClose: () => void }) {
  const { computers } = useLive();
  const images = useFetch(() => api.images(), "images");
  const vpn = useFetch(() => api.vpnConnections(), "vpn-connections");
  const [vpnID, setVPNID] = useState("");
  const [lan, setLAN] = useState<ExceptionRow[]>([]);
  const [vpnError, setVPNError] = useState<string | null>(null);
  const accounts = useFetch(() => api.accounts(), "accounts");
  const volumes = useFetch(() => api.volumes(), "volumes");
  const [grants, setGrants] = useState<VolumeGrant[]>([]);

  const [name, setName] = useState("");
  const [check, setCheck] = useState<NameCheck | null>(null);
  const [image, setImage] = useState("");
  const [memory, setMemory] = useState(0);
  const [cpus, setCpus] = useState(0);
  const [dockerAccess, setDockerAccess] = useState(false);
  const [access, setAccess] = useState<AccessGrant[]>([]);
  const run = useJob(() => onClose());

  // Default to the catalog's first image.
  useEffect(() => {
    const first = images.data?.[0];
    if (!image && first) setImage(first.ref);
  }, [images.data, image]);

  // Live name validation, a moment after typing stops.
  useEffect(() => {
    setCheck(null);
    if (!NAME.test(name)) return;
    let current = true;
    const t = window.setTimeout(() => {
      api.validateName(name).then(
        (c) => current && setCheck(c),
        (e: unknown) => current && setCheck({ ok: false, error: e instanceof Error ? e.message : String(e), folderExists: false }),
      );
    }, 250);
    return () => {
      current = false;
      window.clearTimeout(t);
    };
  }, [name]);

  let hint: ComponentChildren = "Lowercase letters, digits and dashes; it can't be changed later.";
  let hintOk = false;
  if (name && !NAME.test(name)) {
    hint = <span class="text-danger">Use 2–32 lowercase letters, digits and dashes, starting with a letter and not ending with a dash.</span>;
  } else if (check && !check.ok) {
    hint = <span class="text-danger">{check.error ?? "This name can't be used."}</span>;
  } else if (check?.ok) {
    const folder = folderFor(
      name,
      check,
      (computers ?? []).map((c) => c.folder),
    );
    hintOk = true;
    hint = (
      <>
        Its desktop will be at <span class="mono">/{name}/</span>
        {folder && (
          <>
            {" "}
            and its files in <span class="mono">{folder}</span>
          </>
        )}
        {check.folderExists && <span class="text-busy">. That folder already exists: the environment will reuse the existing folder.</span>}
      </>
    );
  }

  const ready = check?.ok === true && !!image && !run.running;
  const create = () => {
    try {
      const lanExceptions = vpnID ? parseExceptions(lan) : [];
      setVPNError(null);
      void run.start(() => api.createComputer({ name, image, memoryBytes: memory, nanoCpus: cpus, access, dockerAccess, volumes: grants, vpnConnectionId:vpnID || null, lanExceptions }));
    } catch(e) {setVPNError(e instanceof Error ? e.message : String(e));}
  };
  const submit = () => {
    if (!ready) return;
    create();
  };

  return (
    <>
      <Dialog
        title="New environment"
        onClose={onClose}
        onSubmit={submit}
        footer={
          <>
            <Button variant="quiet" onClick={onClose}>
              {run.running ? "Close" : "Cancel"}
            </Button>
            <Button type="submit" variant="primary" disabled={!ready}>
              {run.running ? "Creating…" : "Create environment"}
            </Button>
          </>
        }
      >
        <fieldset disabled={run.running} class="m-0 min-w-0 border-0 p-0">
          <Field label="Name" htmlFor="nc-name" hint={hint} hintOk={hintOk}>
            <input
              id="nc-name"
              type="text"
              autocomplete="off"
              spellcheck={false}
              value={name}
              onInput={(e) => setName(e.currentTarget.value.toLowerCase())}
            />
          </Field>
          <Field label="Image">
            {images.data ? <ImagePick images={images.data} value={image} onChange={setImage} /> : <ErrorText error={images.error} />}
          </Field>
          <Field label="Memory">
            <Seg label="Memory" options={memoryOptions(memory)} value={memory} onChange={setMemory} />
          </Field>
          <Field label="CPUs">
            <Seg label="CPUs" options={cpuOptions(cpus)} value={cpus} onChange={setCpus} />
          </Field>
          {vpn.data ? <VPNFields connections={vpn.data.connections} value={vpnID} onChange={setVPNID} rows={lan} onRows={setLAN} /> : <ErrorText error={vpn.error} />}
          <DockerAccessField value={dockerAccess} onChange={setDockerAccess} />
          {volumes.data ? <VolumeGrantFields grants={grants} volumes={volumes.data.volumes} onChange={setGrants} creating /> : <ErrorText error={volumes.error} />}
          <Field label="Who can use it">
            {accounts.data ? <AccessPicks accounts={accounts.data} value={access} onChange={setAccess} /> : <ErrorText error={accounts.error} />}
          </Field>
        </fieldset>
        <JobProgress job={run.job} />
        <ErrorText error={vpnError || run.error} />
      </Dialog>
    </>
  );
}
