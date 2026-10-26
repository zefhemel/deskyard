import type { HostMount } from "../api.ts";
import { Button, Check, Field } from "./ui.tsx";

export function HostMountFields({ mounts, onChange }: { mounts: HostMount[]; onChange: (mounts: HostMount[]) => void }) {
  const update = (index: number, change: Partial<HostMount>) => onChange(mounts.map((mount, i) => i === index ? { ...mount, ...change } : mount));
  return (
    <section class="mt-6 border-t border-line pt-5 first:mt-0 first:border-t-0 first:pt-0" aria-label="Host folders">
      <h2 class="mb-1 text-base font-semibold">Host folders</h2>
      <p class="mb-4 text-sm text-muted">
        Mount an existing folder from the machine running your container engine. Everyone who can use this environment can access it. Changes apply when you save, which recreates its container.
      </p>
      {mounts.map((mount, index) => (
        <fieldset key={index} class="mb-4 min-w-0 rounded-lg border border-line p-4">
          <legend class="px-1 text-sm font-semibold">Folder {index + 1}</legend>
          <Field label="Host folder" htmlFor={`mount-source-${index}`}>
            <input id={`mount-source-${index}`} value={mount.source} required maxLength={4096} placeholder="/path/on/host" onInput={(e) => update(index, { source: e.currentTarget.value })} />
          </Field>
          <Field label="Path in the environment" htmlFor={`mount-target-${index}`} hint="Use a path below /mnt/, such as /mnt/photos.">
            <input id={`mount-target-${index}`} value={mount.target} required maxLength={4096} pattern="/mnt/.+" placeholder="/mnt/photos" onInput={(e) => update(index, { target: e.currentTarget.value })} />
          </Field>
          <Check id={`mount-readonly-${index}`} checked={mount.readOnly} onChange={(readOnly) => update(index, { readOnly })} title="Read only">
            Turn off to allow this environment to change files in the host folder.
          </Check>
          <Button variant="quiet" aria-label={`Remove host folder ${index + 1}`} onClick={() => onChange(mounts.filter((_, i) => i !== index))}>Remove folder</Button>
        </fieldset>
      ))}
      <Button class="mb-5" disabled={mounts.length >= 16} onClick={() => onChange([...mounts, { source: "", target: "", readOnly: true }])}>Add host folder</Button>
    </section>
  );
}
