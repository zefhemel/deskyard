import { useState } from "preact/hooks";
import { api, type CatalogImage } from "../api.ts";
import { JobProgress, useJob } from "../components/job.tsx";
import { notify, notifyError } from "../components/toast.tsx";
import { ComputerChip } from "../components/computer-chip.tsx";
import { Page, PageHead } from "../components/topbar.tsx";
import { Button, Dialog, ErrorText, Field, Tag } from "../components/ui.tsx";
import { UPGRADE_NOTE, UpgradeDialog } from "../dialogs/upgrade.tsx";
import { useLive } from "../live.tsx";
import { ago, bytes, day, plural } from "../lib/format.ts";
import { useFetch } from "../lib/use-fetch.ts";

export function ImagesPage() {
  const images = useFetch(() => api.images(), "images");
  const [adding, setAdding] = useState(false);
  const [upgrading, setUpgrading] = useState<CatalogImage | null>(null);
  const reload = () => images.reload();
  const check = useJob(() => {
    images.reload();
    notify("Image updates pulled");
  });

  const checked = (images.data ?? []).map((i) => i.checked).filter((c): c is string => !!c).sort().pop();
  return (
    <Page>
      <PageHead
        title="Images"
        sub={checked ? `Last refreshed ${ago(checked).toLowerCase()}` : "Pull newer versions of installed images. Upgrade computers separately."}
      >
        <Button disabled={check.running} onClick={() => void check.start(() => api.checkImages())}>
          {check.running ? "Pulling updates…" : "Pull updates"}
        </Button>
        <Button onClick={() => setAdding(true)}>Add image</Button>
      </PageHead>
      <div role="status" aria-live="polite" aria-label="Image update progress">
        <JobProgress job={check.job} />
      </div>
      <ErrorText error={images.error ?? check.error} />
      {images.data &&
        (images.data.length ? (
          <div class="grid gap-3">
            {images.data.map((img) => (
              <ImageCard key={img.ref} image={img} onUpgrade={() => setUpgrading(img)} onRemoved={reload} />
            ))}
          </div>
        ) : (
          <p class="text-muted">The catalog is empty: add an image to create computers from.</p>
        ))}
      <p class="mt-4 text-sm text-muted">Upgrading recreates a computer's container on the newest version and restarts it if it's running. {UPGRADE_NOTE}</p>
      {adding && (
        <AddImageDialog
          onClose={() => setAdding(false)}
          onAdded={() => {
            setAdding(false);
            images.reload();
          }}
        />
      )}
      {upgrading && (
        <UpgradeDialog
          title={`Upgrade ${plural(upgrading.behind.length, "computer")}?`}
          intro={
            <>
              {upgrading.behind.join(", ")} {upgrading.behind.length === 1 ? "is" : "are"} recreated on {upgrading.title} {upgrading.version}; running
              ones restart.
            </>
          }
          action={`Upgrade ${plural(upgrading.behind.length, "computer")}`}
          begin={() => api.upgradeImage(upgrading.ref)}
          onClose={() => {
            setUpgrading(null);
            images.reload();
          }}
        />
      )}
    </Page>
  );
}

function ImageCard({
  image: img,
  onUpgrade,
  onRemoved,
}: {
  image: CatalogImage;
  onUpgrade: () => void;
  onRemoved: () => void;
}) {
  const { computers } = useLive();
  const env = (name: string) => computers?.find((c) => c.name === name);
  const install = useJob(onRemoved);
  const remove = () => {
    api.removeImage(img.ref).then(onRemoved, notifyError);
  };
  const behind = img.installed && img.behind.length > 0;
  const small = "h-[30px]! px-[11px]!";
  return (
    <article class="rounded-[10px] border border-line bg-surface p-5" aria-label={img.title || img.ref}>
      <div class="flex flex-wrap items-start gap-x-4 gap-y-2">
        <div class="mr-auto min-w-0">
          <h2 class="flex flex-wrap items-center gap-x-2 gap-y-1 text-lg font-semibold">
            {img.title || img.ref}
            {img.official && <Tag>Official</Tag>}
            <Tag kind={behind ? "update" : img.installed ? "ok" : undefined}>{!img.installed ? "Not downloaded" : behind ? "Update available" : "Installed"}</Tag>
          </h2>
          {img.description && <p class="mt-0.5 text-sm text-muted">{img.description}</p>}
        </div>
        <div class="flex flex-wrap gap-2">
          {!img.installed && (
            <Button variant="primary" class={small} disabled={install.running} onClick={() => void install.start(() => api.installImage(img.ref))}>
              {install.running ? "Installing…" : "Install"}
            </Button>
          )}
          {behind && (
            <Button variant="primary" class={small} onClick={onUpgrade}>
              Upgrade {plural(img.behind.length, "computer")}…
            </Button>
          )}
          {img.computers.length === 0 && !img.official && (
            <Button class={small} onClick={remove}>
              Remove from catalog
            </Button>
          )}
        </div>
      </div>
      <JobProgress job={install.job} />
      <ErrorText error={install.error} />
      <dl class="mt-4 grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[7.5rem_1fr] sm:gap-y-3">
        <dt class="text-muted">Version</dt>
        <dd class="min-w-0">
          {img.version ? <span class="font-semibold">{img.version}</span> : <span class="text-muted">{img.installed ? "Unknown" : "Not downloaded yet"}</span>}
          {img.built && <span class="text-muted"> · built {day(img.built)}</span>}
        </dd>
        <dt class="mt-2 text-muted sm:mt-0">Size</dt>
        <dd class="min-w-0">{img.size ? bytes(img.size) : <span class="text-muted">–</span>}</dd>
        <dt class="mt-2 text-muted sm:mt-0">Reference</dt>
        <dd class="mono min-w-0 break-all">{img.ref}</dd>
        <dt class="mt-2 text-muted sm:mt-0">Last refreshed</dt>
        <dd class="min-w-0">{img.checked ? ago(img.checked) : <span class="text-muted">Not yet</span>}</dd>
        <dt class="mt-2 text-muted sm:mt-0">Computers</dt>
        <dd class="min-w-0">
          {img.computers.length ? (
            <ul class="m-0 flex list-none flex-wrap gap-2 p-0" aria-label={`Computers using ${img.title || img.ref}`}>
              {img.computers.map((n) => {
                const late = img.behind.includes(n);
                const c = env(n);
                return (
                  <li key={n}>
                    <ComputerChip name={n} computer={c} href={`/computers/${n}?tab=settings`}>
                      <Tag kind={late ? "update" : "ok"}>{late ? `Behind${c?.imageVersion ? ` · ${c.imageVersion}` : ""}` : "Up to date"}</Tag>
                    </ComputerChip>
                  </li>
                );
              })}
            </ul>
          ) : (
            <span class="text-muted">No computer uses it.</span>
          )}
        </dd>
      </dl>
    </article>
  );
}

function AddImageDialog({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const [ref, setRef] = useState("");
  const run = useJob(() => onAdded());
  return (
    <Dialog
      title="Add image"
      intro="The supervisor pulls it and checks that it's a Deskyard desktop image before adding it to the catalog."
      onClose={onClose}
      onSubmit={() => ref.trim() && void run.start(() => api.addImage(ref.trim()))}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={!ref.trim() || run.running}>
            {run.running ? "Adding…" : "Add image"}
          </Button>
        </>
      }
    >
      <Field label="Image reference" htmlFor="img-ref" hint="A registry reference with a tag, like the ones already in the catalog.">
        <input
          id="img-ref"
          type="text"
          class="mono"
          autocomplete="off"
          spellcheck={false}
          placeholder="ghcr.io/zefhemel/deskyard-debian-xfce:latest"
          disabled={run.running}
          value={ref}
          onInput={(e) => setRef(e.currentTarget.value)}
        />
      </Field>
      <JobProgress job={run.job} />
      <ErrorText error={run.error} />
    </Dialog>
  );
}
