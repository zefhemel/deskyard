import { useState } from "preact/hooks";
import { api, type CatalogImage } from "../api.ts";
import { JobProgress, useJob } from "../components/job.tsx";
import { notify, notifyError } from "../components/toast.tsx";
import { Page, PageHead } from "../components/topbar.tsx";
import { Button, Dialog, ErrorText, Field, Tag } from "../components/ui.tsx";
import { UPGRADE_NOTE, UpgradeDialog } from "../dialogs/upgrade.tsx";
import { useLive } from "../live.tsx";
import { ago, plural } from "../lib/format.ts";
import { useFetch } from "../lib/use-fetch.ts";

const th = "border-b border-line px-4 py-[11px] text-left text-[13px] font-semibold text-muted";
const td = "border-b border-hairline px-4 py-[13px] align-middle";

export function ImagesPage() {
  const images = useFetch(() => api.images(), "images");
  const [adding, setAdding] = useState(false);
  const [upgrading, setUpgrading] = useState<CatalogImage | null>(null);
  const reload = () => images.reload();
  const check = useJob(() => {
    images.reload();
    notify("Checked every image for updates");
  });

  const checked = (images.data ?? []).map((i) => i.checked).filter((c): c is string => !!c).sort().pop();
  return (
    <Page>
      <PageHead
        title="Images"
        sub={check.running ? (check.job?.message ?? "Checking…") : checked ? `Checked for updates ${ago(checked).toLowerCase()}` : undefined}
      >
        <Button disabled={check.running} onClick={() => void check.start(() => api.checkImages())}>
          {check.running ? "Checking…" : "Check now"}
        </Button>
        <Button onClick={() => setAdding(true)}>Add image</Button>
      </PageHead>
      <ErrorText error={images.error ?? check.error} />
      {images.data && (
        <div class="overflow-x-auto rounded-[10px] border border-line bg-surface">
          <table class="w-full border-collapse text-sm">
            <thead>
              <tr>
                <th class={th}>Image</th>
                <th class={th}>Status / version</th>
                <th class={th}>Desktops</th>
                <th class={th} />
              </tr>
            </thead>
            <tbody>
              {images.data.map((img) => (
                <ImageRow key={img.ref} image={img} onUpgrade={() => setUpgrading(img)} onRemoved={reload} />
              ))}
              {!images.data.length && (
                <tr>
                  <td class={`${td} text-muted`} colSpan={4}>
                    The catalog is empty: add an image to create desktops from.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      )}
      <p class="mt-4 text-sm text-muted">Upgrading recreates a desktop on the newest version and restarts it if it's running. {UPGRADE_NOTE}</p>
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
          title={`Upgrade ${plural(upgrading.behind.length, "desktop")}?`}
          intro={
            <>
              {upgrading.behind.join(", ")} {upgrading.behind.length === 1 ? "is" : "are"} recreated on {upgrading.title} {upgrading.version}; running
              ones restart.
            </>
          }
          action={`Upgrade ${plural(upgrading.behind.length, "desktop")}`}
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

function ImageRow({
  image: img,
  onUpgrade,
  onRemoved,
}: {
  image: CatalogImage;
  onUpgrade: () => void;
  onRemoved: () => void;
}) {
  const { desktops } = useLive();
  const version = (name: string) => desktops?.find((c) => c.name === name)?.imageVersion;
  const current = img.desktops.filter((n) => !img.behind.includes(n));
  const install = useJob(onRemoved);
  const remove = () => {
    api.removeImage(img.ref).then(onRemoved, notifyError);
  };
  return (
    <tr class="last:[&>td]:border-b-0">
      <td class={td}>
        <div class="flex flex-wrap items-center gap-x-2 gap-y-1 font-semibold">
          {img.title || img.ref}
          {img.official && <Tag>Official</Tag>}
        </div>
        <div class="mt-0.5 text-[13px] text-muted">
          {img.description}
          {img.description && " · "}
          <span class="mono break-all">{img.ref}</span>
        </div>
      </td>
      <td class={`${td} whitespace-nowrap`}>
        <Tag kind={img.installed && img.behind.length > 0 ? "update" : undefined}>
          {!img.installed ? "Available" : img.behind.length > 0 ? "Update available" : "Installed"}
        </Tag>
        {img.version && <div class="mt-1 text-[13px]">{img.version}</div>}
        {!img.installed && <div class="mt-1 text-[13px] text-muted">Not downloaded</div>}
        {img.checked && <div class="mt-0.5 text-[13px] text-muted">pulled {ago(img.checked).toLowerCase()}</div>}
        <JobProgress job={install.job} />
        <ErrorText error={install.error} />
      </td>
      <td class={td}>
        {img.desktops.length ? (
          <div class="flex flex-wrap gap-[5px]">
            {current.map((n) => (
              <Tag key={n}>{n}</Tag>
            ))}
            {img.behind.map((n) => (
              <Tag key={n} kind="update">
                {n}
                {version(n) ? ` · ${version(n)}` : ""}
              </Tag>
            ))}
          </div>
        ) : (
          <span class="text-muted">None</span>
        )}
      </td>
      <td class={`${td} text-right whitespace-nowrap`}>
        {!img.installed && (
          <Button variant="primary" class="mr-2 h-[30px]! px-[11px]!" disabled={install.running}
            onClick={() => void install.start(() => api.installImage(img.ref))}>
            {install.running ? "Installing…" : "Install"}
          </Button>
        )}
        {img.installed && img.behind.length > 0 ? (
          <Button variant="primary" class="h-[30px]! px-[11px]!" onClick={onUpgrade}>
            Upgrade {plural(img.behind.length, "desktop")}…
          </Button>
        ) : (
          img.desktops.length === 0 && !img.official && (
            <Button class="h-[30px]! px-[11px]!" onClick={remove}>
              {"Remove from catalog"}
            </Button>
          )
        )}
      </td>
    </tr>
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
