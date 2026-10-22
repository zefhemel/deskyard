import { useState } from "preact/hooks";
import { api, type Desktop } from "../../api.ts";
import { cpuOptions, ImagePick, memoryOptions } from "../../components/desktop-fields.tsx";
import { JobProgress, useJob } from "../../components/job.tsx";
import { notify } from "../../components/toast.tsx";
import { SecretImageNote } from "../../components/secret-access.tsx";
import { Button, Dialog, ErrorText, Field, Note, Seg } from "../../components/ui.tsx";
import { useFetch } from "../../lib/use-fetch.ts";
import { UPGRADE_NOTE } from "../../dialogs/upgrade.tsx";

export function SettingsTab({ desktop: c }: { desktop: Desktop }) {
  const images = useFetch(() => api.images(), "images");
  const [image, setImage] = useState(c.image);
  const [memory, setMemory] = useState(c.settings.memoryBytes);
  const [cpus, setCpus] = useState(c.settings.nanoCpus);
  const run = useJob(() => notify(`${c.name}'s settings are saved`));

  const [confirming, setConfirming] = useState(false);
  const changed = image !== c.image || memory !== c.settings.memoryBytes || cpus !== c.settings.nanoCpus;
  // Moving a desktop others use onto an image with secrets: ask first, as giving access does.
  const picked = (images.data ?? []).find((i) => i.ref === image);
  const askFirst = image !== c.image && !!picked?.secrets && c.access.length > 0;
  const save = () => {
    setConfirming(false);
    const body: { image?: string; memoryBytes?: number; nanoCpus?: number; confirmSecrets?: boolean } = {};
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
    : [{ ref: c.image, title: c.imageTitle, version: c.imageVersion, description: "Not in the catalog any more.", desktops: [], behind: [], checked: null, custom: false, secrets: false, baseUpdated: false, recipe: null }, ...catalog];

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
              }}
            >
              Undo changes
            </Button>
          )}
        </div>
      </form>
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
