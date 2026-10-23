import { useEffect, useRef, useState } from "preact/hooks";
import { api, type CatalogImage, type Recipe } from "../api.ts";
import { JobProgress, useJob } from "../components/job.tsx";
import { notify, notifyError } from "../components/toast.tsx";
import { Page, PageHead } from "../components/topbar.tsx";
import { Button, Dialog, ErrorText, Field, LockBadge, Tag } from "../components/ui.tsx";
import { BuildLogDialog } from "../dialogs/build-log.tsx";
import { RecipeDialog } from "../dialogs/recipe.tsx";
import { UPGRADE_NOTE, UpgradeDialog } from "../dialogs/upgrade.tsx";
import { useLive } from "../live.tsx";
import { ago, plural } from "../lib/format.ts";
import { followJob } from "../lib/follow-job.ts";
import { useFetch } from "../lib/use-fetch.ts";

const th = "border-b border-line px-4 py-[11px] text-left text-[13px] font-semibold text-muted";
const td = "border-b border-hairline px-4 py-[13px] align-middle";

export function ImagesPage() {
  const images = useFetch(() => api.images(), "images");
  const saved = useFetch(() => api.recipes(), "recipes");
  const [adding, setAdding] = useState(false);
  const [upgrading, setUpgrading] = useState<CatalogImage | null>(null);
  const { desktops } = useLive();
  // Accounts that can use the desktops an upgrade moves onto an image with secrets.
  const secretWho = (img: CatalogImage) =>
    img.secrets ? [...new Set((desktops ?? []).filter((c) => img.behind.includes(c.name)).flatMap((c) => c.access))].sort() : [];
  // The recipe dialog: `recipe` set means editing it.
  const [recipe, setRecipe] = useState<{ recipe?: Recipe } | null>(null);
  const [building, setBuilding] = useState<{ job: string; title: string } | null>(null);
  const reload = () => {
    images.reload();
    saved.reload();
  };

  const rebuild = (img: CatalogImage) => {
    if (!img.recipe) return;
    api.rebuildRecipe(img.recipe).then(({ job }) => setBuilding({ job, title: img.title || img.ref }), notifyError);
  };
  const edit = (img: CatalogImage) => {
    api.recipes().then((all) => {
      const r = all.find((x) => x.slug === img.recipe);
      if (r) setRecipe({ recipe: r });
      else notifyError(new Error("This image's recipe is missing."));
    }, notifyError);
  };
  // Closing the log doesn't stop the build: one watcher per build says when it ends.
  const watchers = useRef(new Map<string, () => void>());
  useEffect(() => {
    const all = watchers.current;
    return () => {
      for (const stop of all.values()) stop();
      all.clear();
    };
  }, []);
  const hideLog = (job: string, title: string, stillRunning: boolean) => {
    setBuilding(null);
    if (!stillRunning || watchers.current.has(job)) return;
    const end = () => {
      watchers.current.get(job)?.();
      watchers.current.delete(job);
    };
    watchers.current.set(
      job,
      followJob(job, {
        onUpdate: (j) => {
          if (j.status === "running") return;
          end();
          reload();
          if (j.status === "done") notify(`${title} is built`);
          else if (j.error === "cancelled") notify(`The build of ${title} was cancelled`);
          else notify(`The build of ${title} failed: ${j.error ?? j.message}`, { error: true, ms: 8000 });
        },
        onReplay: () => undefined,
        onConnection: () => undefined,
        onGone: () => {
          end();
          reload();
          notify(`Lost track of the build of ${title} — check the Images list`, { error: true, ms: 8000 });
        },
      }),
    );
  };
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
        <Button variant="primary" onClick={() => setRecipe({})}>
          New image
        </Button>
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
                <ImageRow key={img.ref} image={img} onUpgrade={() => setUpgrading(img)} onRemoved={reload} onEdit={() => edit(img)} onRebuild={() => rebuild(img)} />
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
      {images.data && saved.data && (
        <UnbuiltRecipes
          recipes={saved.data.filter((r) => !images.data?.some((i) => i.ref === `deskyard-custom/${r.slug}:latest`))}
          onEdit={(r) => setRecipe({ recipe: r })}
          onRemoved={reload}
        />
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
      {recipe && images.data && (
        <RecipeDialog
          {...recipe}
          images={images.data}
          onClose={() => setRecipe(null)}
          onStarted={(job, title) => {
            setRecipe(null);
            setBuilding({ job, title });
            reload();
          }}
        />
      )}
      {building && (
        <BuildLogDialog
          title={building.title}
          job={building.job}
          onClose={(running) => hideLog(building.job, building.title, running)}
          onDone={reload}
          onLost={reload}
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
          secrets={secretWho(upgrading).length ? { image: upgrading.title || upgrading.ref, who: secretWho(upgrading) } : undefined}
          begin={() => api.upgradeImage(upgrading.ref, secretWho(upgrading).length > 0)}
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
  onEdit,
  onRebuild,
}: {
  image: CatalogImage;
  onUpgrade: () => void;
  onRemoved: () => void;
  onEdit: () => void;
  onRebuild: () => void;
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
          {img.secrets && <LockBadge />}
          {img.baseUpdated && <Tag kind="update">Base updated</Tag>}
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
        {!img.installed && !img.custom && (
          <Button variant="primary" class="mr-2 h-[30px]! px-[11px]!" disabled={install.running}
            onClick={() => void install.start(() => api.installImage(img.ref))}>
            {install.running ? "Installing…" : "Install"}
          </Button>
        )}
        {img.custom && (
          <>
            <Button class="mr-2 h-[30px]! px-[11px]!" onClick={onEdit}>
              Edit
            </Button>
            <Button variant={img.baseUpdated ? "primary" : "default"} class="mr-2 h-[30px]! px-[11px]!" onClick={onRebuild}>
              Rebuild
            </Button>
          </>
        )}
        {img.installed && img.behind.length > 0 ? (
          <Button variant="primary" class="h-[30px]! px-[11px]!" onClick={onUpgrade}>
            Upgrade {plural(img.behind.length, "desktop")}…
          </Button>
        ) : (
          img.desktops.length === 0 && !img.official && (
            <Button class="h-[30px]! px-[11px]!" onClick={remove}>
              {img.custom ? "Delete image" : "Remove from catalog"}
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

/** Recipes saved without an image yet (a recipe on an image made from a desktop): built when first edited. */
function UnbuiltRecipes({ recipes, onEdit, onRemoved }: { recipes: Recipe[]; onEdit: (r: Recipe) => void; onRemoved: () => void }) {
  if (!recipes.length) return null;
  const remove = (r: Recipe) => api.removeRecipe(r.slug).then(onRemoved, notifyError);
  return (
    <div class="mt-6 overflow-x-auto rounded-[10px] border border-line bg-surface">
      <table class="w-full border-collapse text-sm">
        <thead>
          <tr>
            <th class={th}>Saved recipes, not built yet</th>
            <th class={th}>Based on</th>
            <th class={th} />
          </tr>
        </thead>
        <tbody>
          {recipes.map((r) => (
            <tr key={r.slug}>
              <td class={td}>
                <b class="font-semibold">{r.name}</b> {r.secrets && <LockBadge />}
              </td>
              <td class={`${td} mono text-[12.5px]`}>{r.base}</td>
              <td class={`${td} text-right whitespace-nowrap`}>
                <Button variant="quiet" onClick={() => void remove(r)}>
                  Delete
                </Button>{" "}
                <Button onClick={() => onEdit(r)}>Edit and build</Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
