// New image from this desktop: its home folder, sorted into groups, becomes
// the starting home of a new image built on the desktop's own image.
// Secrets are listed apart and left out unless ticked one by one.
import { useEffect, useRef, useState } from "preact/hooks";
import { api, type Desktop, type HomeGroup, type HomeGroupName, type ImageScan } from "../api.ts";
import { power } from "../components/desktop-menu.tsx";
import { Button, cx, Dialog, ErrorText, errorMessage, Field, LockBadge, Note } from "../components/ui.tsx";
import { bytes } from "../lib/format.ts";
import { useFetch } from "../lib/use-fetch.ts";
import { SLUG, slugFor } from "./recipe.tsx";

/** Above this, the dialog warns (there's no hard limit). */
export const BIG = 5 * 1024 ** 3;

const groupInfo: Record<HomeGroupName, { label: string; hint: string }> = {
  tools: { label: "Installed tools", hint: "mise tools, ~/.local/bin, npm and uv tools" },
  flatpak: { label: "Flatpak apps", hint: "Installed again from their remotes; their data in ~/.var is copied" },
  settings: { label: "App settings", hint: "~/.config" },
  dotfiles: { label: "Dotfiles", hint: "Files and folders starting with a dot" },
  browser: { label: "Browser profile", hint: "Chromium's extensions, bookmarks and settings" },
  other: { label: "Other files", hint: "Everything else in the home folder" },
};

const UNTICKED: HomeGroupName[] = ["other"];

const home = (p: string) => `~/${p}`;

export function ImageFromDesktopDialog({
  desktop: c,
  onClose,
  onStarted,
}: {
  desktop: Desktop;
  onClose: () => void;
  onStarted: (job: string, title: string) => void;
}) {
  const running = c.state === "running";
  // Scanned again when the desktop stops (a stopped desktop's Flatpak apps are read from its folder).
  const scan = useFetch(() => api.imageScan(c.name), `${c.name}:${running ? "running" : "stopped"}`);
  const [name, setName] = useState(`${c.name} image`);
  const [groups, setGroups] = useState<Set<HomeGroupName> | null>(null);
  const [include, setInclude] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (scan.data && groups === null) {
      setGroups(new Set(scan.data.groups.map((g) => g.name).filter((g) => !UNTICKED.includes(g))));
    }
  }, [scan.data]);

  const slug = slugFor(name);
  const ticked = groups ?? new Set<HomeGroupName>();
  const selected = (scan.data?.groups ?? []).filter((g) => ticked.has(g.name)).reduce((n, g) => n + g.bytes, 0);
  const ready = !!scan.data && name.trim() !== "" && SLUG.test(slug) && !busy;

  const toggle = <T,>(set: Set<T>, v: T, on: boolean) => {
    const next = new Set(set);
    if (on) next.add(v);
    else next.delete(v);
    return next;
  };

  const submit = () => {
    if (!ready || !scan.data) return;
    setBusy(true);
    setError(null);
    const title = name.trim();
    api
      .imageFromDesktop(c.name, {
        name: title,
        slug,
        // In the scan's order.
        groups: scan.data.groups.map((g) => g.name).filter((g) => ticked.has(g)),
        include: [...include].sort(),
      })
      .then(
        ({ job }) => onStarted(job, title),
        (e: unknown) => {
          setBusy(false);
          setError(errorMessage(e));
        },
      );
  };

  return (
    <Dialog
      wide
      title={`New image from ${c.name}`}
      intro={
        <>
          Copies what you pick from {c.name}'s home folder into a new image built on {c.imageTitle || c.image}. Desktops made from it start with these
          files.
        </>
      }
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={!ready}>
            Build
          </Button>
        </>
      }
    >
      {running && (
        <Note>
          <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
            <span class="min-w-0 flex-1">{c.name} is running, so files can change while they're copied. Stop it first for a consistent copy.</span>
            <Button onClick={() => power(c.name, "stop")}>Stop it first</Button>
          </div>
        </Note>
      )}
      <fieldset disabled={busy} class="m-0 min-w-0 border-0 p-0">
        <Field
          label="Name"
          htmlFor="fd-name"
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
          <input id="fd-name" type="text" autocomplete="off" spellcheck={false} value={name} onInput={(e) => setName(e.currentTarget.value)} />
        </Field>
        <Field label="What to copy">
          {!scan.data ? (
            scan.error ? (
              <ErrorText error={scan.error} />
            ) : (
              <p class="m-0 text-sm text-muted" role="status">
                Reading {c.name}'s files…
              </p>
            )
          ) : (
            <Groups
              scan={scan.data}
              ticked={ticked}
              include={include}
              onGroup={(g, on) => setGroups(toggle(ticked, g, on))}
              onSecret={(p, on) => setInclude(toggle(include, p, on))}
            />
          )}
        </Field>
      </fieldset>
      {scan.data && (
        <p class="mt-0 mb-3.5 text-sm text-muted" data-testid="sizes">
          Home folder {bytes(scan.data.totalBytes)}, without caches · copying {bytes(selected)}
        </p>
      )}
      {selected > BIG && (
        <Note>
          That's more than {bytes(BIG)}: the image takes a while to build, and every desktop made from it starts with all of it. Leave out what new desktops
          don't need.
        </Note>
      )}
      {include.size > 0 && (
        <Note danger>
          <b class="font-semibold">This image will contain secrets.</b> Desktops made from it start signed in as whoever made it, and it carries a lock
          wherever images are picked.
        </Note>
      )}
      <ErrorText error={error} />
    </Dialog>
  );
}

function Groups({
  scan,
  ticked,
  include,
  onGroup,
  onSecret,
}: {
  scan: ImageScan;
  ticked: Set<HomeGroupName>;
  include: Set<string>;
  onGroup: (g: HomeGroupName, on: boolean) => void;
  onSecret: (path: string, on: boolean) => void;
}) {
  return (
    <div class="rounded-lg border border-line bg-white">
      {scan.groups.map((g) => (
        <GroupRow key={g.name} group={g} scan={scan} on={ticked.has(g.name)} include={include} onGroup={onGroup} onSecret={onSecret} />
      ))}
    </div>
  );
}

function GroupRow({
  group: g,
  scan,
  on,
  include,
  onGroup,
  onSecret,
}: {
  group: HomeGroup;
  scan: ImageScan;
  on: boolean;
  include: Set<string>;
  onGroup: (g: HomeGroupName, on: boolean) => void;
  onSecret: (path: string, on: boolean) => void;
}) {
  const info = groupInfo[g.name];
  const flatpak = g.name === "flatpak";
  const empty = g.paths.length === 0 && g.secrets.length === 0 && !(flatpak && scan.flatpaks.length > 0);
  // Ticked with secrets carved out: partly ticked.
  const partly = on && g.secrets.some((s) => !include.has(s));
  const box = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (box.current) box.current.indeterminate = partly;
  }, [partly]);
  const id = `fd-group-${g.name}`;
  const shown = g.paths.slice(0, 4).map(home).join(", ");
  const more = g.paths.length - 4;

  return (
    <div class="border-b border-hairline px-3 py-2.5 text-sm last:border-b-0" data-group={g.name}>
      <div class="flex items-start gap-2.5">
        <input
          ref={box}
          type="checkbox"
          id={id}
          class="mt-[3px]"
          checked={on}
          disabled={empty}
          aria-label={info.label}
          onChange={(e) => onGroup(g.name, e.currentTarget.checked)}
        />
        <label for={id} class={cx("min-w-0 flex-1", empty && "text-muted")}>
          <span class="flex flex-wrap items-baseline gap-x-2">
            <b class="font-semibold">{info.label}</b>
            <span class="text-[13px] text-muted">{empty ? "Nothing here" : bytes(g.bytes)}</span>
          </span>
          <span class="mt-0.5 block text-[13px] text-muted">{info.hint}</span>
          {shown && (
            <span class="mono mt-0.5 block text-[12.5px] [overflow-wrap:anywhere] text-muted">
              {shown}
              {more > 0 && ` and ${more} more`}
            </span>
          )}
          {flatpak && scan.flatpaks.length > 0 && (
            <span class="mt-0.5 block text-[13px] [overflow-wrap:anywhere]">
              Apps: <span class="mono text-[12.5px]">{scan.flatpaks.map((f) => f.id).join(", ")}</span>
            </span>
          )}
          {flatpak &&
            scan.notes.map((n) => (
              <span key={n} class="mt-0.5 block text-[13px] text-danger">
                {n}
              </span>
            ))}
        </label>
      </div>
      {g.secrets.length > 0 && (
        <ul class="mt-2 mb-0 ml-[26px] list-none p-0" aria-label={`Secrets in ${info.label}`}>
          {g.secrets.map((s) => {
            const sid = `fd-secret-${g.name}-${s}`;
            return (
              <li key={s} class="flex items-center gap-2.5 py-0.5">
                <input type="checkbox" id={sid} checked={include.has(s)} onChange={(e) => onSecret(s, e.currentTarget.checked)} />
                <label for={sid} class="flex min-w-0 flex-wrap items-center gap-x-2">
                  <span class="mono text-[12.5px] [overflow-wrap:anywhere]">{home(s)}</span>
                  <LockBadge label="Secret" />
                </label>
              </li>
            );
          })}
          <li class="pt-0.5 text-[12.5px] text-muted">Secrets stay out unless you tick them.</li>
        </ul>
      )}
    </div>
  );
}
