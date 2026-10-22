// New image / Edit and rebuild: a recipe for an image built on a catalog image.
import { useMemo, useState } from "preact/hooks";
import { api, type CatalogImage, type Flatpak, type Recipe, type Remote } from "../api.ts";
import { FlatpakRows, isRefUrl, newKey, RemoteRows, type FlatpakRow, type RemoteRow } from "../components/recipe-fields.tsx";
import { Button, Dialog, ErrorText, errorMessage, Field } from "../components/ui.tsx";

const SLUG = /^[a-z][a-z0-9-]{0,30}[a-z0-9]$/;
const area = "mono min-h-[84px] px-[11px] py-2 leading-[1.5]";

/** "My tools!" → "my-tools": what the image's tag and recipe are called. */
export function slugFor(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^[^a-z]+/, "")
    .slice(0, 32)
    .replace(/-+$/, "");
}

const lines = (text: string) => text.split("\n").map((l) => l.trim()).filter(Boolean);

function remoteRows(list: Remote[] | undefined): RemoteRow[] {
  return (list ?? []).map((r) =>
    "repoFile" in r
      ? { key: newKey(), name: r.name, mode: "repoFile", repoFile: r.repoFile, url: "", gpgKey: "" }
      : { key: newKey(), name: r.name, mode: "url", repoFile: "", url: r.url, gpgKey: r.gpgKey },
  );
}

function flatpakRows(list: Flatpak[] | undefined): FlatpakRow[] {
  return (list ?? []).map((f) =>
    "ref" in f ? { key: newKey(), value: f.ref, remote: "" } : { key: newKey(), value: f.id, remote: f.remote ?? "" },
  );
}

/** `recipe` set: editing it (its slug can't change). `onStarted` gets the build's job. */
export function RecipeDialog({
  recipe,
  images,
  onClose,
  onStarted,
}: {
  recipe?: Recipe;
  images: CatalogImage[];
  onClose: () => void;
  onStarted: (job: string, title: string) => void;
}) {
  const editing = !!recipe;
  const bases = useMemo(() => {
    const own = recipe ? `deskyard-custom/${recipe.slug}:` : null;
    const list = images.filter((i) => !(own && i.ref.startsWith(own)));
    // A base that left the catalog stays selectable (the server says what's wrong).
    if (recipe && !list.some((i) => i.ref === recipe.base)) {
      list.unshift({ ref: recipe.base, title: recipe.base } as CatalogImage);
    }
    return list;
  }, [images, recipe]);

  const [name, setName] = useState(recipe?.name ?? "");
  const [base, setBase] = useState(recipe?.base ?? bases[0]?.ref ?? "");
  const [mise, setMise] = useState((recipe?.mise ?? []).join("\n"));
  const [remotes, setRemotes] = useState<RemoteRow[]>(() => remoteRows(recipe?.remotes));
  const [flatpaks, setFlatpaks] = useState<FlatpakRow[]>(() => flatpakRows(recipe?.flatpaks));
  const [commands, setCommands] = useState(recipe?.commands ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const slug = recipe?.slug ?? slugFor(name);
  const ready = name.trim() !== "" && SLUG.test(slug) && !!base && !busy;

  const build = (): Recipe => {
    const r: Recipe = { slug, name: name.trim(), base };
    const tools = lines(mise);
    if (tools.length) r.mise = tools;
    const rms: Remote[] = remotes
      .filter((x) => x.name.trim())
      .map((x) =>
        x.mode === "repoFile"
          ? { name: x.name.trim(), repoFile: x.repoFile.trim() }
          : { name: x.name.trim(), url: x.url.trim(), gpgKey: x.gpgKey.trim() },
      );
    if (rms.length) r.remotes = rms;
    const fps: Flatpak[] = flatpaks
      .filter((x) => x.value.trim())
      .map((x) => {
        const v = x.value.trim();
        if (isRefUrl(v)) return { ref: v };
        return x.remote ? { id: v, remote: x.remote } : { id: v };
      });
    if (fps.length) r.flatpaks = fps;
    if (commands.trim()) r.commands = commands;
    // Not edited here: carried over.
    if (recipe?.homeFrom) r.homeFrom = recipe.homeFrom;
    if (recipe?.secrets) r.secrets = recipe.secrets;
    return r;
  };

  const submit = () => {
    if (!ready) return;
    setBusy(true);
    setError(null);
    const r = build();
    (editing ? api.updateRecipe(r) : api.createRecipe(r)).then(
      ({ job }) => onStarted(job, r.name),
      (e: unknown) => {
        setBusy(false);
        setError(errorMessage(e));
      },
    );
  };

  return (
    <Dialog
      wide
      title={editing ? `Edit ${recipe.name}` : "New image"}
      intro={
        editing
          ? "Saving rebuilds the image from scratch. Desktops on it show an update, and upgrading them keeps their files."
          : "Builds a new desktop image on top of one in the catalog, with the tools and apps below installed."
      }
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={!ready}>
            {editing ? "Save and rebuild" : "Build"}
          </Button>
        </>
      }
    >
      <fieldset disabled={busy} class="m-0 min-w-0 border-0 p-0">
        <Field
          label="Name"
          htmlFor="rc-name"
          hint={
            SLUG.test(slug) ? (
              <>
                Its reference will be <span class="mono">deskyard-custom/{slug}:latest</span>
              </>
            ) : name.trim() ? (
              <span class="text-danger">The name needs at least 2 letters or digits, starting with a letter: they make up the image's reference.</span>
            ) : (
              "Give the image a name."
            )
          }
        >
          <input id="rc-name" type="text" autocomplete="off" spellcheck={false} value={name} onInput={(e) => setName(e.currentTarget.value)} />
        </Field>
        <Field label="Based on" htmlFor="rc-base" hint="Anything the base has, this image has too.">
          <select id="rc-base" value={base} onChange={(e) => setBase(e.currentTarget.value)}>
            {bases.map((i) => (
              <option key={i.ref} value={i.ref}>
                {i.title && i.title !== i.ref ? `${i.title} (${i.ref})` : i.ref}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Tools (mise)" htmlFor="rc-mise" hint="One per line, like node@22, python or npm:cowsay.">
          <textarea id="rc-mise" class={area} spellcheck={false} value={mise} onInput={(e) => setMise(e.currentTarget.value)} />
        </Field>
        <Field label="Flatpak apps">
          <FlatpakRows rows={flatpaks} remotes={remotes.map((r) => r.name.trim()).filter(Boolean)} onChange={setFlatpaks} />
        </Field>
        <Field label="Flatpak remotes" hint="Besides Flathub, which is always there.">
          <RemoteRows rows={remotes} onChange={setRemotes} />
        </Field>
        <Field
          label="Build commands"
          htmlFor="rc-commands"
          hint="Saved in the image's labels: anyone who can inspect the image can read them. Don't put tokens here."
        >
          <textarea
            id="rc-commands"
            class={area}
            spellcheck={false}
            placeholder="Shell commands, run as root after the rest"
            value={commands}
            onInput={(e) => setCommands(e.currentTarget.value)}
          />
        </Field>
      </fieldset>
      <ErrorText error={error} />
    </Dialog>
  );
}
