import type { Volume, VolumeGrant } from "../api.ts";
import { addGrant, canAddGrant, grantPathError, removeGrant, updateGrant } from "../lib/volume-grants.ts";
import { Button, Check, Field } from "./ui.tsx";

/** Grants of volumes to an environment; `creating` drops the note about recreating its container. */
export function VolumeGrantFields({
  grants,
  volumes,
  onChange,
  creating,
}: {
  grants: VolumeGrant[];
  volumes: Volume[];
  onChange: (grants: VolumeGrant[]) => void;
  creating?: boolean;
}) {
  return (
    <section class="mt-6 border-t border-line pt-5 first:mt-0 first:border-t-0 first:pt-0" aria-label="Volumes">
      <h2 class="mb-1 text-base font-semibold">Volumes</h2>
      <p class="mb-4 text-sm text-muted">
        {creating ? "Volumes shared with this environment." : "Saving recreates the container."}
      </p>
      {!volumes.length && !grants.length && (
        <p class="mb-4 text-sm text-muted">
          No volumes yet. <a href="/volumes">Create one</a>.
        </p>
      )}
      {grants.map((grant, index) => {
        // A volume deleted since it was granted stays selectable, so the grant can be seen and removed.
        const gone = !volumes.some((v) => v.name === grant.name);
        const pathError = grantPathError(grant.path, grants, index);
        return (
          <fieldset key={index} class="mb-4 min-w-0 rounded-lg border border-line p-4">
            <legend class="px-1 text-sm font-semibold">Volume {index + 1}</legend>
            <Field label="Volume" htmlFor={`volume-name-${index}`}>
              <select id={`volume-name-${index}`} value={grant.name} onChange={(e) => onChange(updateGrant(grants, index, { name: e.currentTarget.value }))}>
                {volumes.map((v) => (
                  <option key={v.name} value={v.name} disabled={v.name !== grant.name && grants.some((g) => g.name === v.name)}>
                    {v.name}
                  </option>
                ))}
                {gone && <option value={grant.name}>{grant.name} (no such volume)</option>}
              </select>
            </Field>
            <Field
              label="Path in the environment"
              htmlFor={`volume-path-${index}`}
              hint={pathError ? <span class="text-danger">{pathError}</span> : undefined}
            >
              <input
                type="text"
                id={`volume-path-${index}`}
                class="mono"
                value={grant.path}
                required
                maxLength={4096}
                spellcheck={false}
                aria-invalid={pathError ? true : undefined}
                onInput={(e) => onChange(updateGrant(grants, index, { path: e.currentTarget.value }))}
              />
            </Field>
            <Check id={`volume-readonly-${index}`} checked={grant.readOnly} onChange={(readOnly) => onChange(updateGrant(grants, index, { readOnly }))} title="Read only" />
            <Button variant="quiet" aria-label={`Remove volume ${grant.name}`} onClick={() => onChange(removeGrant(grants, index))}>
              Remove volume
            </Button>
          </fieldset>
        );
      })}
      {volumes.length > 0 && (
        <Button class="mb-5" disabled={!canAddGrant(grants, volumes)} onClick={() => onChange(addGrant(grants, volumes))}>
          Add volume
        </Button>
      )}
    </section>
  );
}
